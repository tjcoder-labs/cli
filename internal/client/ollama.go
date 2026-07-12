package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const DefaultRequestTimeout = 30 * time.Minute

type Ollama struct {
	baseURL    string
	httpClient *http.Client
	mu         sync.Mutex
	ctxWindow  map[string]int
}

type chatChunk struct {
	Message         Message `json:"message"`
	Done            bool    `json:"done"`
	Error           string  `json:"error,omitempty"`
	PromptEvalCount int     `json:"prompt_eval_count,omitempty"`
	EvalCount       int     `json:"eval_count,omitempty"`
}

type tagsResponse struct {
	Models []struct {
		Name    string `json:"name"`
		Details struct {
			Family        string `json:"family"`
			ParameterSize string `json:"parameter_size"`
		} `json:"details"`
	} `json:"models"`
}

type showResponse struct {
	ModelInfo map[string]any `json:"model_info"`
	// The fields below are populated by Ollama (and most Ollama-compatible
	// providers) but were previously discarded. They are the parts of
	// `ollama show` a user actually wants to see when figuring out what a
	// model is: the modelfile (FROM / ADAPTER lines), the chat template,
	// and the license / quantization metadata.
	Modelfile  string `json:"modelfile,omitempty"`
	Parameters string `json:"parameters,omitempty"`
	Template   string `json:"template,omitempty"`
	License    string `json:"license,omitempty"`
	Details    struct {
		Family            string `json:"family,omitempty"`
		ParameterSize     string `json:"parameter_size,omitempty"`
		QuantizationLevel string `json:"quantization_level,omitempty"`
		ParentModel       string `json:"parent_model,omitempty"`
	} `json:"details,omitempty"`
}

// ShowModel queries the Ollama-compatible `/api/show` endpoint and
// returns the modelfile / template / parameters that describe the
// model. The returned ModelDetails is always non-nil on success; an
// error is returned only if the network call fails or the provider
// returns a non-2xx response. Empty fields in the result simply mean
// the provider did not report that aspect of the model.
func (o *Ollama) ShowModel(ctx context.Context, model string) (ModelDetails, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	body, err := json.Marshal(map[string]any{"model": model, "verbose": true})
	if err != nil {
		return ModelDetails{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return ModelDetails{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return ModelDetails{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return ModelDetails{}, fmt.Errorf("ollama show: %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}
	var decoded showResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return ModelDetails{}, err
	}
	// Walk the modelfile and pull out any ADAPTER lines. The modelfile
	// is a human-readable list of instructions (FROM, ADAPTER, PARAMETER,
	// TEMPLATE, LICENSE, ...). Adapters are the second piece of
	// provenance — they tell the user this is a fine-tune layered on
	// top of an existing model, not a from-scratch weights.
	var adapters []string
	for _, line := range strings.Split(decoded.Modelfile, "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "ADAPTER ") {
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 {
				adapters = append(adapters, fields[1])
			}
		}
	}
	return ModelDetails{
		Name:       model,
		Modelfile:  strings.TrimSpace(decoded.Modelfile),
		Parameters: strings.TrimSpace(decoded.Parameters),
		Template:   strings.TrimSpace(decoded.Template),
		License:    strings.TrimSpace(decoded.License),
		Adapters:   adapters,
		Details: map[string]string{
			"family":             decoded.Details.Family,
			"parameter_size":     decoded.Details.ParameterSize,
			"quantization_level": decoded.Details.QuantizationLevel,
			"parent_model":       decoded.Details.ParentModel,
		},
	}, nil
}

func NewOllama(baseURL string) *Ollama {
	return NewOllamaWithTimeout(baseURL, DefaultRequestTimeout)
}

func NewOllamaWithTimeout(baseURL string, requestTimeout time.Duration) *Ollama {
	if requestTimeout <= 0 {
		requestTimeout = 0
	}
	return &Ollama{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
		ctxWindow: map[string]int{},
	}
}

func (o *Ollama) BaseURL() string {
	return o.baseURL
}

func (o *Ollama) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama tags: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var decoded tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(decoded.Models))
	for _, model := range decoded.Models {
		models = append(models, ModelInfo{
			Name:          model.Name,
			ParameterSize: model.Details.ParameterSize,
			Family:        model.Details.Family,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, nil
}

func (o *Ollama) Chat(ctx context.Context, request ChatRequest, onEvent func(StreamEvent) error) (Message, error) {
	msg, err := o.chatOnce(ctx, request, onEvent)
	if err != nil && request.Think && isThinkingUnsupported(err) {
		request.Think = false
		if onEvent != nil {
			_ = onEvent(StreamEvent{
				Reasoning: "\n[model note] Thinking mode is unsupported for this model; retrying without thinking.\n",
			})
		}
		msg, err = o.chatOnce(ctx, request, onEvent)
	}
	if err != nil && isRetryableTimeout(err) && (ctx == nil || ctx.Err() == nil) {
		if onEvent != nil {
			_ = onEvent(StreamEvent{
				Reasoning: "\n[model note] Request timed out; retrying once.\n",
			})
		}
		return o.chatOnce(ctx, request, onEvent)
	}
	return msg, err
}

func (o *Ollama) ContextWindow(ctx context.Context, model string) (int, error) {
	if model == "" {
		return 0, nil
	}
	o.mu.Lock()
	if n, ok := o.ctxWindow[model]; ok {
		o.mu.Unlock()
		return n, nil
	}
	o.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("ollama show: %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}
	var decoded showResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, err
	}
	ctxLen := 0
	for key, value := range decoded.ModelInfo {
		lower := strings.ToLower(key)
		if !strings.Contains(lower, "context_length") && !strings.Contains(lower, "num_ctx") && !strings.Contains(lower, ".ctx") {
			continue
		}
		switch typed := value.(type) {
		case float64:
			if int(typed) > ctxLen {
				ctxLen = int(typed)
			}
		case int:
			if typed > ctxLen {
				ctxLen = typed
			}
		}
	}
	o.mu.Lock()
	o.ctxWindow[model] = ctxLen
	o.mu.Unlock()
	return ctxLen, nil
}

func (o *Ollama) chatOnce(ctx context.Context, request ChatRequest, onEvent func(StreamEvent) error) (Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request.Stream = true
	body, err := json.Marshal(request)
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Message{}, fmt.Errorf("ollama chat: %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}

	var content strings.Builder
	var thinking strings.Builder
	var toolCalls []ToolCall
	promptEvalCount := 0
	evalCount := 0

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return Message{}, fmt.Errorf("decode ollama stream: %w", err)
		}
		if chunk.Error != "" {
			return Message{}, fmt.Errorf("ollama: %s", chunk.Error)
		}
		if chunk.Message.Thinking != "" {
			thinking.WriteString(chunk.Message.Thinking)
			if onEvent != nil {
				if err := onEvent(StreamEvent{Reasoning: chunk.Message.Thinking}); err != nil {
					return Message{}, err
				}
			}
		}
		if chunk.Message.Content != "" {
			content.WriteString(chunk.Message.Content)
			if onEvent != nil {
				if err := onEvent(StreamEvent{Commentary: chunk.Message.Content}); err != nil {
					return Message{}, err
				}
			}
		}
		if len(chunk.Message.ToolCalls) > 0 {
			toolCalls = chunk.Message.ToolCalls
		}
		if chunk.PromptEvalCount > 0 {
			promptEvalCount = chunk.PromptEvalCount
		}
		if chunk.EvalCount > 0 {
			evalCount = chunk.EvalCount
		}
		if chunk.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return Message{}, err
	}

	return Message{
		Role:            "assistant",
		Content:         content.String(),
		Thinking:        thinking.String(),
		ToolCalls:       toolCalls,
		PromptEvalCount: promptEvalCount,
		EvalCount:       evalCount,
	}, nil
}

func isThinkingUnsupported(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "does not support thinking") ||
		strings.Contains(text, "support thinking") ||
		strings.Contains(text, "unsupported think") ||
		strings.Contains(text, "thinking is not supported")
}

func isRetryableTimeout(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "client.timeout exceeded while awaiting headers")
}

func ResolveBaseURL(raw string) string {
	if raw == "" {
		return ""
	}
	if _, err := url.Parse(raw); err != nil {
		return raw
	}
	return raw
}
