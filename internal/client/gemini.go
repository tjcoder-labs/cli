package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const GeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// geminiContextWindows lists known context windows for common Gemini models.
var geminiContextWindows = map[string]int{
	"gemini-2.5-pro":        1048576,
	"gemini-2.5-flash":      1048576,
	"gemini-2.0-flash":      1048576,
	"gemini-2.0-flash-lite": 1048576,
	"gemini-1.5-pro":        2097152,
	"gemini-1.5-flash":      1048576,
	"gemini-1.5-flash-8b":   1048576,
}

// ---- wire types ----

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type geminiToolSet struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	Tools             []geminiToolSet `json:"tools,omitempty"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---- Gemini provider ----

type Gemini struct {
	apiKey     string
	httpClient *http.Client
}

func NewGemini(apiKey string, timeout time.Duration) *Gemini {
	if timeout < 0 {
		timeout = 0
	}
	return &Gemini{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (g *Gemini) BaseURL() string {
	return GeminiBaseURL
}

func geminiModelsURL(apiKey string) string {
	base := strings.TrimSuffix(GeminiBaseURL, "/") + "/models"
	query := url.Values{}
	query.Set("key", apiKey)
	return base + "?" + query.Encode()
}

func geminiStreamGenerateContentURL(model, apiKey string) string {
	base := strings.TrimSuffix(GeminiBaseURL, "/") + "/models/" + url.PathEscape(strings.TrimPrefix(model, "models/")) + ":streamGenerateContent"
	query := url.Values{}
	query.Set("alt", "sse")
	query.Set("key", apiKey)
	return base + "?" + query.Encode()
}

func (g *Gemini) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geminiModelsURL(g.apiKey), nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("gemini list models: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var decoded struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	var models []ModelInfo
	for _, m := range decoded.Models {
		capable := false
		for _, method := range m.SupportedGenerationMethods {
			if method == "generateContent" {
				capable = true
				break
			}
		}
		if !capable {
			continue
		}
		models = append(models, ModelInfo{
			Name:   strings.TrimPrefix(m.Name, "models/"),
			Family: "gemini",
		})
	}
	return models, nil
}

// ShowModel returns the closest thing the Gemini API has to a
// model-show endpoint. There is no per-model modelfile or template
// here, so we surface the version / display name / supported
// generation methods so the user has at least *something* concrete
// to identify which API model they're hitting. The OK error return
// is intentional — callers that need to know "no provenance"
// should check the License / Modelfile fields for emptiness.
func (g *Gemini) ShowModel(_ context.Context, model string) (ModelDetails, error) {
	model = strings.TrimPrefix(model, "models/")
	details := map[string]string{
		"family":   "gemini",
		"provider": "google",
	}
	if n, ok := geminiContextWindows[model]; ok {
		details["context_window"] = fmt.Sprintf("%d", n)
	} else {
		for k, v := range geminiContextWindows {
			if strings.HasPrefix(model, k) {
				details["context_window"] = fmt.Sprintf("%d", v)
				break
			}
		}
	}
	return ModelDetails{
		Name:    model,
		Details: details,
	}, nil
}

func (g *Gemini) ContextWindow(_ context.Context, model string) (int, error) {
	model = strings.TrimPrefix(model, "models/")
	if n, ok := geminiContextWindows[model]; ok {
		return n, nil
	}
	// Prefix match for dated/preview variants (e.g. "gemini-2.5-flash-preview-04-17").
	for k, v := range geminiContextWindows {
		if strings.HasPrefix(model, k) {
			return v, nil
		}
	}
	return 0, nil
}

func (g *Gemini) Chat(ctx context.Context, request ChatRequest, onEvent func(StreamEvent) error) (Message, error) {
	msg, err := g.chatStream(ctx, request, onEvent)
	if err != nil && isRetryableTimeout(err) && (ctx == nil || ctx.Err() == nil) {
		if onEvent != nil {
			_ = onEvent(StreamEvent{Reasoning: "\n[model note] Request timed out; retrying once.\n"})
		}
		return g.chatStream(ctx, request, onEvent)
	}
	return msg, err
}

func (g *Gemini) chatStream(ctx context.Context, request ChatRequest, onEvent func(StreamEvent) error) (Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	gr, err := buildGeminiRequest(request)
	if err != nil {
		return Message{}, err
	}
	body, err := json.Marshal(gr)
	if err != nil {
		return Message{}, err
	}
	model := strings.TrimPrefix(request.Model, "models/")
	url := geminiStreamGenerateContentURL(model, g.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Message{}, fmt.Errorf("gemini chat: %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}

	var contentBuf strings.Builder
	var toolCalls []ToolCall
	promptTokens, outTokens := 0, 0

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk geminiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Message{}, fmt.Errorf("decode gemini stream: %w", err)
		}
		if chunk.Error != nil {
			return Message{}, fmt.Errorf("gemini: %d: %s", chunk.Error.Code, chunk.Error.Message)
		}
		if chunk.UsageMetadata.PromptTokenCount > 0 {
			promptTokens = chunk.UsageMetadata.PromptTokenCount
		}
		if chunk.UsageMetadata.CandidatesTokenCount > 0 {
			outTokens = chunk.UsageMetadata.CandidatesTokenCount
		}
		for _, candidate := range chunk.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					contentBuf.WriteString(part.Text)
					if onEvent != nil {
						if err := onEvent(StreamEvent{Commentary: part.Text}); err != nil {
							return Message{}, err
						}
					}
				}
				if part.FunctionCall != nil {
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, ToolCall{
						Function: ToolFunctionCall{
							Name:      part.FunctionCall.Name,
							Arguments: json.RawMessage(argsJSON),
						},
					})
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Message{}, err
	}
	return Message{
		Role:            "assistant",
		Content:         contentBuf.String(),
		ToolCalls:       toolCalls,
		PromptEvalCount: promptTokens,
		EvalCount:       outTokens,
	}, nil
}

// buildGeminiRequest converts a ChatRequest into the Gemini REST request body.
func buildGeminiRequest(req ChatRequest) (geminiRequest, error) {
	gr := geminiRequest{}

	// Collect system messages and separate remaining messages.
	var systemParts []string
	var msgs []Message
	for _, m := range req.Messages {
		if m.Role == "system" {
			systemParts = append(systemParts, m.Content)
		} else {
			msgs = append(msgs, m)
		}
	}
	if len(systemParts) > 0 {
		gr.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: strings.Join(systemParts, "\n\n")}},
		}
	}

	// Convert messages, merging consecutive same-role contents as required by Gemini.
	for _, m := range msgs {
		content, err := messageToGeminiContent(m)
		if err != nil {
			return gr, err
		}
		if len(gr.Contents) > 0 && gr.Contents[len(gr.Contents)-1].Role == content.Role {
			gr.Contents[len(gr.Contents)-1].Parts = append(
				gr.Contents[len(gr.Contents)-1].Parts, content.Parts...)
		} else {
			gr.Contents = append(gr.Contents, content)
		}
	}

	// Convert tool definitions.
	if len(req.Tools) > 0 {
		decls := make([]geminiFunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, geminiFunctionDeclaration{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			})
		}
		gr.Tools = []geminiToolSet{{FunctionDeclarations: decls}}
	}

	return gr, nil
}

// messageToGeminiContent converts a single Message to a Gemini content object.
func messageToGeminiContent(m Message) (geminiContent, error) {
	switch m.Role {
	case "assistant":
		var parts []geminiPart
		if m.Content != "" {
			parts = append(parts, geminiPart{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			var args map[string]any
			if len(tc.Function.Arguments) > 0 {
				if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
					return geminiContent{}, fmt.Errorf("parse tool call args: %w", err)
				}
			}
			parts = append(parts, geminiPart{
				FunctionCall: &geminiFunctionCall{Name: tc.Function.Name, Args: args},
			})
		}
		if len(parts) == 0 {
			parts = []geminiPart{{Text: ""}}
		}
		return geminiContent{Role: "model", Parts: parts}, nil

	case "tool":
		// Wrap string content as JSON object for Gemini's functionResponse.
		var response map[string]any
		if err := json.Unmarshal([]byte(m.Content), &response); err != nil {
			response = map[string]any{"output": m.Content}
		}
		return geminiContent{
			Role: "user",
			Parts: []geminiPart{{
				FunctionResponse: &geminiFunctionResponse{
					Name:     m.ToolName,
					Response: response,
				},
			}},
		}, nil

	default: // "user"
		return geminiContent{
			Role:  "user",
			Parts: []geminiPart{{Text: m.Content}},
		}, nil
	}
}
