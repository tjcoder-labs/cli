package client

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestBuildGeminiRequest_SystemMessage(t *testing.T) {
	req := ChatRequest{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello"},
		},
	}
	gr, err := buildGeminiRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gr.SystemInstruction == nil {
		t.Fatal("expected SystemInstruction to be set")
	}
	if len(gr.SystemInstruction.Parts) != 1 || gr.SystemInstruction.Parts[0].Text != "You are a helpful assistant." {
		t.Errorf("unexpected SystemInstruction: %+v", gr.SystemInstruction)
	}
	if len(gr.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(gr.Contents))
	}
	if gr.Contents[0].Role != "user" || gr.Contents[0].Parts[0].Text != "Hello" {
		t.Errorf("unexpected content: %+v", gr.Contents[0])
	}
}

func TestBuildGeminiRequest_MergesConsecutiveSameRole(t *testing.T) {
	// user, tool, tool all map to Gemini "user" role → merge into one content with 3 parts.
	req := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Run both tools"},
			{Role: "tool", ToolName: "tool_a", Content: `{"ok":true}`},
			{Role: "tool", ToolName: "tool_b", Content: "plain text result"},
		},
	}
	gr, err := buildGeminiRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All three map to "user" role and merge into a single content with 3 parts.
	if len(gr.Contents) != 1 {
		t.Fatalf("expected 1 merged content, got %d", len(gr.Contents))
	}
	if gr.Contents[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", gr.Contents[0].Role)
	}
	if len(gr.Contents[0].Parts) != 3 {
		t.Errorf("expected 3 parts (text + 2 fn responses), got %d", len(gr.Contents[0].Parts))
	}
}

func TestBuildGeminiRequest_UserModelAlternation(t *testing.T) {
	// Realistic turn: user → assistant(tool call) → tool result → assistant reply.
	req := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "list files"},
			{Role: "assistant", ToolCalls: []ToolCall{
				{Function: ToolFunctionCall{Name: "list_directory", Arguments: json.RawMessage(`{"path":"."}`)}},
			}},
			{Role: "tool", ToolName: "list_directory", Content: "main.go\ngo.mod"},
			{Role: "assistant", Content: "Found 2 files."},
		},
	}
	gr, err := buildGeminiRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// user / model(fn call) / user(fn response) / model(text) → 4 separate contents
	if len(gr.Contents) != 4 {
		t.Fatalf("expected 4 contents, got %d: %+v", len(gr.Contents), gr.Contents)
	}
	roles := []string{"user", "model", "user", "model"}
	for i, want := range roles {
		if gr.Contents[i].Role != want {
			t.Errorf("contents[%d].Role = %q, want %q", i, gr.Contents[i].Role, want)
		}
	}
}

func TestBuildGeminiRequest_ToolDefinitions(t *testing.T) {
	req := ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: []ToolDefinition{
			{
				Type: "function",
				Function: FunctionDefinition{
					Name:        "read_file",
					Description: "Read a file",
					Parameters: map[string]any{
						"type":     "object",
						"required": []string{"path"},
						"properties": map[string]any{
							"path": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	}
	gr, err := buildGeminiRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gr.Tools) != 1 || len(gr.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected 1 tool declaration, got tools=%v", gr.Tools)
	}
	decl := gr.Tools[0].FunctionDeclarations[0]
	if decl.Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", decl.Name)
	}
}

func TestMessageToGeminiContent_User(t *testing.T) {
	c, err := messageToGeminiContent(Message{Role: "user", Content: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Role != "user" || len(c.Parts) != 1 || c.Parts[0].Text != "ping" {
		t.Errorf("unexpected: %+v", c)
	}
}

func TestMessageToGeminiContent_Assistant(t *testing.T) {
	c, err := messageToGeminiContent(Message{Role: "assistant", Content: "pong"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Role != "model" || len(c.Parts) != 1 || c.Parts[0].Text != "pong" {
		t.Errorf("unexpected: %+v", c)
	}
}

func TestMessageToGeminiContent_AssistantWithToolCalls(t *testing.T) {
	args := json.RawMessage(`{"path":"/tmp/foo"}`)
	c, err := messageToGeminiContent(Message{
		Role: "assistant",
		ToolCalls: []ToolCall{
			{Function: ToolFunctionCall{Name: "read_file", Arguments: args}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Role != "model" {
		t.Errorf("expected role 'model', got %q", c.Role)
	}
	if len(c.Parts) != 1 || c.Parts[0].FunctionCall == nil {
		t.Fatalf("expected 1 functionCall part, got %+v", c.Parts)
	}
	fc := c.Parts[0].FunctionCall
	if fc.Name != "read_file" {
		t.Errorf("unexpected function name: %q", fc.Name)
	}
	if fc.Args["path"] != "/tmp/foo" {
		t.Errorf("unexpected args: %v", fc.Args)
	}
}

func TestMessageToGeminiContent_ToolResultJSON(t *testing.T) {
	c, err := messageToGeminiContent(Message{
		Role:     "tool",
		ToolName: "read_file",
		Content:  `{"lines":42}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Role != "user" {
		t.Errorf("expected role 'user', got %q", c.Role)
	}
	if len(c.Parts) != 1 || c.Parts[0].FunctionResponse == nil {
		t.Fatalf("expected functionResponse part, got %+v", c.Parts)
	}
	fr := c.Parts[0].FunctionResponse
	if fr.Name != "read_file" {
		t.Errorf("unexpected name: %q", fr.Name)
	}
	if !reflect.DeepEqual(fr.Response, map[string]any{"lines": float64(42)}) {
		t.Errorf("unexpected response: %v", fr.Response)
	}
}

func TestMessageToGeminiContent_ToolResultPlainText(t *testing.T) {
	c, err := messageToGeminiContent(Message{
		Role:     "tool",
		ToolName: "run_command",
		Content:  "exit code 0",
	})
	if err != nil {
		t.Fatal(err)
	}
	fr := c.Parts[0].FunctionResponse
	if fr.Response["output"] != "exit code 0" {
		t.Errorf("expected output wrap, got %v", fr.Response)
	}
}

func TestGeminiContextWindow(t *testing.T) {
	g := NewGemini("key", 0)
	ctx := t.Context()

	for model, want := range map[string]int{
		"gemini-2.5-flash":               1048576,
		"gemini-2.5-flash-preview-04-17": 1048576,
		"gemini-1.5-pro":                 2097152,
		"gemini-unknown-model":           0,
	} {
		got, err := g.ContextWindow(ctx, model)
		if err != nil {
			t.Errorf("ContextWindow(%q) error: %v", model, err)
		}
		if got != want {
			t.Errorf("ContextWindow(%q) = %d, want %d", model, got, want)
		}
	}
}

func TestGeminiModelsURLEscapesAPIKey(t *testing.T) {
	got := geminiModelsURL("test&key=malicious=value")

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Query().Get("key") != "test&key=malicious=value" {
		t.Fatalf("unexpected key query value: %q", parsed.Query().Get("key"))
	}
	if strings.Count(parsed.RawQuery, "key=") != 1 {
		t.Fatalf("expected a single key query param, got %q", parsed.RawQuery)
	}
}

func TestGeminiStreamGenerateContentURLEscapesInputs(t *testing.T) {
	got := geminiStreamGenerateContentURL("models/gemini-2.5-flash/preview?beta=1", "test&debug=true")

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Query().Get("key") != "test&debug=true" {
		t.Fatalf("unexpected key query value: %q", parsed.Query().Get("key"))
	}
	if parsed.Query().Get("alt") != "sse" {
		t.Fatalf("unexpected alt query value: %q", parsed.Query().Get("alt"))
	}
	if !strings.Contains(got, "/models/gemini-2.5-flash%2Fpreview%3Fbeta=1:streamGenerateContent?") {
		t.Fatalf("expected escaped model path in %q", got)
	}
}
