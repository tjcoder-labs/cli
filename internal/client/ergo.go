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

// ErgoDefaultBaseURL is the public ergo AI gateway. Override per-process
// with NewErgo(baseURL, ...).
const ErgoDefaultBaseURL = "https://api.tjcoder.com"

// ergoContextWindows lists conservative context windows for models served
// through the ergo gateway. The gateway routes to a family of providers;
// when a model is unknown we report 0 (= "unknown") rather than guess.
var ergoContextWindows = map[string]int{
	// OpenAI
	"gpt-4o":       128000,
	"gpt-4o-mini":  128000,
	"gpt-4.1":      1047576,
	"gpt-4.1-mini": 1047576,
	"gpt-4.1-nano": 1047576,
	"o3":           200000,
	"o4-mini":      200000,
	// Anthropic
	"claude-opus-4":     200000,
	"claude-sonnet-4":   200000,
	"claude-3-5-sonnet": 200000,
	"claude-3-5-haiku":  200000,
	// Google (via gateway models)
	"gemini-2.5-pro":   1048576,
	"gemini-2.5-flash": 1048576,
}

// ---- OpenAI-compatible wire types ----

type ergoChatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []ergoToolCall `json:"tool_calls,omitempty"`
}

type ergoToolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type"`
	Function ergoToolCallFunc `json:"function"`
}

type ergoToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ergoChatRequest struct {
	Model       string            `json:"model"`
	Messages    []ergoChatMessage `json:"messages"`
	Tools       []ergoTool        `json:"tools,omitempty"`
	Stream      bool              `json:"stream"`
	StreamOpts  *ergoStreamOpts   `json:"stream_options,omitempty"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Temperature float64           `json:"temperature,omitempty"`
}

type ergoStreamOpts struct {
	// Critical for billing: ask the gateway to append a final chunk with
	// real token usage so ergo's settleUsage can debit accurately.
	IncludeUsage bool `json:"include_usage"`
}

type ergoTool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type ergoUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ergoStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string         `json:"content"`
			ToolCalls []ergoToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *ergoUsage `json:"usage,omitempty"`
}

type ergoAPIError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// ---- billing-aware error types ----

// InsufficientCreditsError is returned when the ergo gateway responds 402:
// the tenant's wallet lacks funds for this call.
type InsufficientCreditsError struct {
	Message string
	// TopupURL is the gateway-provided link to add credits ("" if absent).
	TopupURL string
	// BalanceCents is the wallet balance reported with the 402 (-1 if absent).
	BalanceCents int64
}

// ErgoDefaultTopupURL is used when the 402 body carries no topup_url.
const ErgoDefaultTopupURL = "https://dashboard.tjcoder.com"

// TopupLink returns the URL to show the user.
func (e *InsufficientCreditsError) TopupLink() string {
	if e.TopupURL != "" {
		return e.TopupURL
	}
	return ErgoDefaultTopupURL
}

func (e *InsufficientCreditsError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "insufficient ergo credits"
	}
	return msg + " — top up at " + e.TopupLink()
}

// ErgoAuthError is returned on 401/403: the API key is missing, invalid,
// expired, or revoked. The user should run /login again.
type ErgoAuthError struct{ Message string }

func (e *ErgoAuthError) Error() string {
	if e.Message == "" {
		return "ergo API key invalid or revoked — run /login to re-authenticate"
	}
	return e.Message
}

// IsInsufficientCredits reports whether err is an ergo 402.
func IsInsufficientCredits(err error) bool {
	var ice *InsufficientCreditsError
	return errors.As(err, &ice)
}

// IsErgoAuthError reports whether err is an ergo 401/403.
func IsErgoAuthError(err error) bool {
	var ae *ErgoAuthError
	return errors.As(err, &ae)
}

// ---- provider ----

// Ergo is a Provider backed by the ergo AI gateway. By default it speaks
// the OpenAI-compatible /v1/chat/completions endpoint; UseOllamaProtocol
// switches chat and model listing to the gateway's Ollama-compatible API
// (/api/chat, /api/tags, /api/show). Authentication uses an ergo API key
// with the "ergo-cli" scope (issued by /login); dashboard "ergo-chat" keys
// are rejected by /v1. Every call is metered against the tenant's wallet.
type Ergo struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	ollama     *Ollama // non-nil when speaking the Ollama protocol
}

// ErgoDefaultOllamaPath is the gateway's Ollama-compatible API prefix
// (it is also served under /ollama/api).
const ErgoDefaultOllamaPath = "/api"

// UseOllamaProtocol routes chat, model listing and /show through the
// gateway's Ollama-compatible API at apiPath ("" = /api).
func (e *Ergo) UseOllamaProtocol(apiPath string) *Ergo {
	if strings.TrimSpace(apiPath) == "" {
		apiPath = ErgoDefaultOllamaPath
	}
	o := NewOllamaCloud(e.baseURL, e.apiKey, e.httpClient.Timeout).WithAPIPath(apiPath)
	e.ollama = o
	return e
}

// Protocol reports the wire protocol in use ("openai" or "ollama").
func (e *Ergo) Protocol() string {
	if e.ollama != nil {
		return "ollama"
	}
	return "openai"
}

// NewErgo creates an ergo provider. baseURL should include no trailing
// path (we append /v1/...); pass ErgoDefaultBaseURL in production.
// A negative/zero timeout disables the client deadline (long streams).
func NewErgo(baseURL, apiKey string, timeout time.Duration) *Ergo {
	return &Ergo{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (e *Ergo) BaseURL() string { return e.baseURL }

// Authenticated reports whether an API key is configured. It does not
// validate the key against the gateway.
func (e *Ergo) Authenticated() bool { return e.apiKey != "" }

func (e *Ergo) Chat(ctx context.Context, request ChatRequest, onEvent func(StreamEvent) error) (Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if e.apiKey == "" {
		return Message{}, &ErgoAuthError{Message: "no ergo API key configured — run /login to authenticate"}
	}
	if e.ollama != nil {
		return e.ollama.Chat(ctx, request, onEvent)
	}
	req := ergoChatRequest{
		Model:      request.Model,
		Stream:     true, // always stream; matches the TUI's incremental rendering
		StreamOpts: &ergoStreamOpts{IncludeUsage: true},
	}

	// Map messages. System messages pass through as role "system".
	for _, m := range request.Messages {
		req.Messages = append(req.Messages, toErgoMessage(m))
	}
	if len(request.Tools) > 0 {
		req.Tools = make([]ergoTool, 0, len(request.Tools))
		for _, t := range request.Tools {
			req.Tools = append(req.Tools, ergoTool{Type: "function", Function: t.Function})
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
			return Message{}, fmt.Errorf("ergo chat: request timed out: %w", err)
		}
		return Message{}, fmt.Errorf("ergo chat: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return Message{}, &ErgoAuthError{Message: strings.TrimSpace(decodeErgoError(resp.Body))}
	case resp.StatusCode == 402:
		return Message{}, decodeInsufficientCredits(resp.Body)
	case resp.StatusCode >= 300:
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if mnf := parseErgoModelNotFound(resp.StatusCode, errBody, request.Model); mnf != nil {
			return Message{}, mnf
		}
		return Message{}, fmt.Errorf("ergo chat: %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}

	// ---- consume the SSE stream ----
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
		var chunk ergoStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // tolerate keep-alive/comment frames that fail to parse
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
				// OpenAI streams function arguments as string fragments; append.
				if tc.Function.Arguments != "" {
					slot.Function.Arguments = append(slot.Function.Arguments, tc.Function.Arguments...)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Message{}, fmt.Errorf("ergo stream: %w", err)
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

// toErgoMessage converts the internal Message shape to the OpenAI wire
// format. Tool responses use role "tool" with tool_call_id; ergo (and the
// models behind it) accepts the tool name as the fallback id since our
// runtime keys results by name.
func toErgoMessage(m Message) ergoChatMessage {
	switch m.Role {
	case "tool":
		return ergoChatMessage{Role: "tool", Content: m.Content, ToolCallID: m.ToolName}
	case "assistant":
		out := ergoChatMessage{Role: "assistant", Content: m.Content}
		for _, tc := range m.ToolCalls {
			args := ""
			if len(tc.Function.Arguments) > 0 {
				args = string(tc.Function.Arguments)
			}
			out.ToolCalls = append(out.ToolCalls, ergoToolCall{
				Index:    tc.Function.Index,
				ID:       tc.Function.Name,
				Type:     "function",
				Function: ergoToolCallFunc{Name: tc.Function.Name, Arguments: args},
			})
		}
		return out
	default: // user, system
		return ergoChatMessage{Role: m.Role, Content: m.Content}
	}
}

// ListModels queries the gateway's /v1/models. If the gateway does not
// serve it yet (404), fall back to the known rate-card families so /model
// always shows something meaningful.
func (e *Ergo) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if e.apiKey == "" {
		return nil, &ErgoAuthError{Message: "no ergo API key configured"}
	}
	if e.ollama != nil {
		return e.ollama.ListModels(ctx)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, &ErgoAuthError{Message: strings.TrimSpace(decodeErgoError(resp.Body))}
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ergo list models: %s", resp.Status)
	}
	var decoded struct {
		Data []struct {
			ID       string `json:"id"`
			OwnedBy  string `json:"owned_by"`
			AliasFor string `json:"alias_for"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(decoded.Data))
	for _, m := range decoded.Data {
		if m.AliasFor != "" || strings.TrimSpace(m.ID) == "" {
			continue // legacy ids (ergo, ergo-lite, …) stay valid server-side but are hidden
		}
		family := m.OwnedBy
		if family == "" {
			family = ErgoModelGroup(m.ID)
		}
		models = append(models, ModelInfo{Name: m.ID, Family: family})
	}
	return models, nil
}

// ContextWindow returns a best-effort window from the static map; 0 when
// the model is unknown (the TUI treats 0 as "unknown", never fatal).
func (e *Ergo) ContextWindow(_ context.Context, model string) (int, error) {
	if n, ok := ergoContextWindows[model]; ok {
		return n, nil
	}
	for k, v := range ergoContextWindows {
		if strings.HasPrefix(model, k) {
			return v, nil
		}
	}
	return 0, nil
}

// ShowModel surfaces gateway/rate metadata in the ModelDetails shape used
// by /model show. The gateway does not expose modelfiles; we report the
// billing context window and provider family so the user has provenance.
func (e *Ergo) ShowModel(ctx context.Context, model string) (ModelDetails, error) {
	details := map[string]string{"provider": "ergo", "billing": "ergo wallet"}
	if n, _ := e.ContextWindow(ctx, model); n > 0 {
		details["context_window"] = fmt.Sprintf("%d", n)
	}
	return ModelDetails{Name: model, Details: details}, nil
}

// decodeInsufficientCredits parses the 402 body
// {error:{code:"insufficient_credit", message, balance_cents, topup_url}}.
func decodeInsufficientCredits(body io.Reader) *InsufficientCreditsError {
	raw, _ := io.ReadAll(io.LimitReader(body, 8192))
	out := &InsufficientCreditsError{BalanceCents: -1}
	var parsed struct {
		Error struct {
			Message      string `json:"message"`
			TopupURL     string `json:"topup_url"`
			BalanceCents *int64 `json:"balance_cents"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil {
		out.Message = strings.TrimSpace(parsed.Error.Message)
		if u := strings.TrimSpace(parsed.Error.TopupURL); strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
			out.TopupURL = u
		}
		if parsed.Error.BalanceCents != nil {
			out.BalanceCents = *parsed.Error.BalanceCents
		}
	} else {
		out.Message = strings.TrimSpace(string(raw))
	}
	return out
}

// decodeErgoError extracts the gateway's {error:{message}} body shape,
// tolerating plain-string bodies too.
func decodeErgoError(body io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(body, 8192))
	var apiErr ergoAPIError
	if err := json.Unmarshal(raw, &apiErr); err == nil && apiErr.Error.Message != "" {
		return apiErr.Error.Message
	}
	return string(raw)
}
