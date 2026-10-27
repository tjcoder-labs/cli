package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatible is a Provider backed by any OpenAI-compatible HTTP API:
// GET  /v1/models            → model enumeration
// POST /v1/chat/completions  → streaming (SSE) or JSON chat completions
// The exact endpoint base (host), API key, and model naming are supplied
// per instance, so this one type serves self-hosted and third-party
// gateways (LM Studio, vLLM, Together, Groq, custom ergo-compatible
// proxies, ...) without a bespoke client per vendor.
type OpenAICompatible struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	// ctxWindows is an optional static override; when a model is absent we
	// report 0 (= "unknown") rather than guess.
	ctxWindows map[string]int
}

// NewOpenAICompatible builds an OpenAI-compatible provider. baseURL is the
// scheme+host (no trailing "/v1" — it is appended). apiKey is sent as a
// Bearer token; pass "" for unauthenticated local endpoints. timeout<=0
// disables the client deadline (long streams).
func NewOpenAICompatible(baseURL, apiKey string, timeout time.Duration) *OpenAICompatible {
	return &OpenAICompatible{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
		ctxWindows: map[string]int{},
	}
}

// SetContextWindows seeds a static context-window table so ContextWindow can
// answer without a network round-trip. Optional; empty map is fine.
func (o *OpenAICompatible) SetContextWindows(windows map[string]int) {
	o.ctxWindows = windows
}

func (o *OpenAICompatible) BaseURL() string { return o.baseURL }

func (o *OpenAICompatible) authorize(req *http.Request) {
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
}

// ---- model enumeration ----

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (o *OpenAICompatible) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	o.authorize(req)
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai list models: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var decoded openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(decoded.Data))
	for _, m := range decoded.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			models = append(models, ModelInfo{Name: id, Family: "openai"})
		}
	}
	return models, nil
}

// ContextWindow returns the static window for model when known, else 0.
func (o *OpenAICompatible) ContextWindow(_ context.Context, model string) (int, error) {
	if n, ok := o.ctxWindows[model]; ok {
		return n, nil
	}
	for k, v := range o.ctxWindows {
		if strings.HasPrefix(model, k) {
			return v, nil
		}
	}
	return 0, nil
}

// ShowModel reports the provider provenance we know. OpenAI-compatible
// endpoints generally have no modelfile concept, so we surface the context
// window (when known) and leave the rest empty.
func (o *OpenAICompatible) ShowModel(ctx context.Context, model string) (ModelDetails, error) {
	details := map[string]string{"provider": "openai-compatible"}
	if n, _ := o.ContextWindow(ctx, model); n > 0 {
		details["context_window"] = fmt.Sprintf("%d", n)
	}
	return ModelDetails{Name: model, Details: details}, nil
}

// ---- chat ----

// Chat implements the streaming OpenAI chat-completions protocol, reusing the
// same wire shape and SSE parsing as the ergo gateway (which is itself
// OpenAI-compatible). It supports tool calls and the optional final usage
// chunk (stream_options.include_usage).
func (o *OpenAICompatible) Chat(ctx context.Context, request ChatRequest, onEvent func(StreamEvent) error) (Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req := openAIChatRequest{
		Model:      request.Model,
		Messages:   toOpenAIMessages(request.Messages),
		Stream:     true,
		StreamOpts: &openAIStreamOpts{IncludeUsage: true},
	}
	for _, t := range request.Tools {
		req.Tools = append(req.Tools, openAITool{Type: "function", Function: t.Function})
	}

	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	o.authorize(httpReq)

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
			return Message{}, fmt.Errorf("openai chat: request timed out: %w", err)
		}
		return Message{}, fmt.Errorf("openai chat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Message{}, fmt.Errorf("openai chat: %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}

	var contentBuf strings.Builder
	toolCalls := map[int]*ToolCall{}
	promptTokens, outTokens := 0, 0

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			promptTokens = chunk.Usage.PromptTokens
			outTokens = chunk.Usage.CompletionTokens
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				contentBuf.WriteString(choice.Delta.Content)
				if onEvent != nil {
					if err := onEvent(StreamEvent{Commentary: choice.Delta.Content}); err != nil {
						return Message{}, err
					}
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				slot, ok := toolCalls[tc.Index]
				if !ok {
					toolCalls[tc.Index] = &ToolCall{Type: "function"}
					slot = toolCalls[tc.Index]
					slot.Function.Index = tc.Index
				}
				if tc.Function.Name != "" {
					slot.Function.Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					slot.Function.Arguments = append(slot.Function.Arguments, tc.Function.Arguments...)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Message{}, fmt.Errorf("openai stream: %w", err)
	}

	ordered := make([]ToolCall, 0, len(toolCalls))
	for i := 0; i < len(toolCalls); i++ {
		if tc, ok := toolCalls[i]; ok {
			ordered = append(ordered, *tc)
		}
	}

	return Message{
		Role:            "assistant",
		Content:         contentBuf.String(),
		ToolCalls:       ordered,
		PromptEvalCount: promptTokens,
		EvalCount:       outTokens,
	}, nil
}

// ---- wire types (mirrors the ergo OpenAI-compatible shapes) ----

type openAIChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type"`
	Function openAIToolCallFunc `json:"function"`
}

type openAIToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatRequest struct {
	Model      string              `json:"model"`
	Messages   []openAIChatMessage `json:"messages"`
	Tools      []openAITool        `json:"tools,omitempty"`
	Stream     bool                `json:"stream"`
	StreamOpts *openAIStreamOpts   `json:"stream_options,omitempty"`
}

type openAIStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage,omitempty"`
}

// toOpenAIMessages converts internal Message shapes to the OpenAI wire
// format. Tool responses use role "tool" with tool_call_id.
func toOpenAIMessages(messages []Message) []openAIChatMessage {
	out := make([]openAIChatMessage, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "tool":
			out = append(out, openAIChatMessage{Role: "tool", Content: m.Content, ToolCallID: m.ToolName})
		case "assistant":
			msg := openAIChatMessage{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				args := ""
				if len(tc.Function.Arguments) > 0 {
					args = string(tc.Function.Arguments)
				}
				msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
					Index:    tc.Function.Index,
					ID:       tc.Function.Name,
					Type:     "function",
					Function: openAIToolCallFunc{Name: tc.Function.Name, Arguments: args},
				})
			}
			out = append(out, msg)
		default:
			out = append(out, openAIChatMessage{Role: m.Role, Content: m.Content})
		}
	}
	return out
}
