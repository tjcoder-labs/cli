package client

import (
	"context"
	"encoding/json"
)

type Message struct {
	Role            string     `json:"role"`
	Content         string     `json:"content,omitempty"`
	Thinking        string     `json:"thinking,omitempty"`
	ToolName        string     `json:"tool_name,omitempty"`
	ToolCalls       []ToolCall `json:"tool_calls,omitempty"`
	PromptEvalCount int        `json:"prompt_eval_count,omitempty"`
	EvalCount       int        `json:"eval_count,omitempty"`
}

type ToolCall struct {
	Type     string           `json:"type,omitempty"`
	Function ToolFunctionCall `json:"function"`
}

type ToolFunctionCall struct {
	Index     int             `json:"index,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatRequest struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
	Stream   bool             `json:"stream"`
	Think    bool             `json:"think,omitempty"`
}

type StreamEvent struct {
	Commentary string
	Reasoning  string
}

type ModelInfo struct {
	Name          string
	ParameterSize string
	Family        string
}

// ModelDetails captures the provenance / configuration of a model as
// returned by an Ollama-compatible `/api/show` endpoint. Fields are
// populated when the provider supports them; empty strings mean
// "the provider did not report this". The shape mirrors Ollama's
// `ollama show` output so the user can see the modelfile (FROM
// line and any ADAPTER block) at a glance, which is exactly what's
// needed to identify whether a custom model is a fine-tune of an
// upstream open-source base.
type ModelDetails struct {
	Name       string            // echo of the requested model name
	Modelfile  string            // raw modelfile text (FROM, ADAPTER, PARAMETER, etc.)
	Parameters string            // default inference parameters (num_ctx, temperature, ...)
	Template   string            // chat template used by the model
	Details    map[string]string // size, family, quantization level, etc.
	License    string            // license string when reported
	Adapters   []string          // attached adapter names (e.g. LoRA)
}

type Provider interface {
	Chat(context.Context, ChatRequest, func(StreamEvent) error) (Message, error)
	ListModels(context.Context) ([]ModelInfo, error)
	ContextWindow(context.Context, string) (int, error)
	BaseURL() string
}

// ProvenanceProvider is an optional capability that a Provider may
// implement when it can return model provenance / modelfile
// information (e.g. Ollama's /api/show endpoint). It is intentionally
// NOT part of the base Provider interface because not every provider
// has the concept of a modelfile (Gemini, for example) and adding
// required methods to Provider breaks every test mock in the suite.
// Callers should type-assert to this interface when they want to
// surface provenance; absence is treated as "no provenance available".
type ProvenanceProvider interface {
	ShowModel(context.Context, string) (ModelDetails, error)
}
