package tooling

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

type mockProvider struct{}

func (m *mockProvider) Chat(ctx context.Context, req client.ChatRequest, onEvent func(client.StreamEvent) error) (client.Message, error) {
	return client.Message{}, nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]client.ModelInfo, error) {
	return nil, nil
}

func (m *mockProvider) ContextWindow(ctx context.Context, s string) (int, error) {
	return 8192, nil
}

func (m *mockProvider) BaseURL() string {
	return ""
}

func TestExtractFallbackToolCall_MarkdownJSON(t *testing.T) {
	mockProvider := &mockProvider{}
	registry := tools.NewRegistry(mockProvider)

	tests := []struct {
		name      string
		input     string
		wantCalls int
		wantName  string
	}{
		{
			name:      "markdown json with spaces",
			input:     "Here's my solution:\n```json\n{ \"name\": \"read_file\", \"arguments\": { \"path\": \".\" } }\n```",
			wantCalls: 1,
			wantName:  "read_file",
		},
		{
			name:      "markdown json compact",
			input:     "```json\n{\"name\":\"write_file\",\"arguments\":{\"path\":\"test.txt\",\"content\":\"hello\"}}\n```",
			wantCalls: 1,
			wantName:  "write_file",
		},
		{
			name:      "invalid tool name ignored",
			input:     "```json\n{\"name\":\"invalid_tool\",\"arguments\":{}}\n```",
			wantCalls: 0,
			wantName:  "",
		},
		{
			name:      "multiple markdown json blocks",
			input:     "First:\n```json\n{\"name\":\"list_directory\",\"arguments\":{\"path\":\".\"}} \n```\n\nSecond:\n```json\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"main.go\"}}\n```",
			wantCalls: 2,
			wantName:  "list_directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, _ := ExtractFallbackToolCall(tt.input, registry)
			if len(calls) != tt.wantCalls {
				t.Errorf("want %d calls, got %d", tt.wantCalls, len(calls))
			}
			if tt.wantCalls > 0 && calls[0].Function.Name != tt.wantName {
				t.Errorf("want name %s, got %s", tt.wantName, calls[0].Function.Name)
			}
		})
	}
}

func TestExtractFallbackToolCall_WithArguments(t *testing.T) {
	mockProvider := &mockProvider{}
	registry := tools.NewRegistry(mockProvider)

	input := "```json\n{\"name\":\"edit_file\",\"arguments\":{\"path\":\"main.go\",\"old_text\":\"foo\",\"new_text\":\"bar\"}}\n```"
	calls, _ := ExtractFallbackToolCall(input, registry)

	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}

	var args map[string]interface{}
	if err := json.Unmarshal(calls[0].Function.Arguments, &args); err != nil {
		t.Fatalf("failed to unmarshal arguments: %v", err)
	}

	if path, ok := args["path"].(string); !ok || path != "main.go" {
		t.Errorf("want path=main.go, got %v", args["path"])
	}
	if oldText, ok := args["old_text"].(string); !ok || oldText != "foo" {
		t.Errorf("want old_text=foo, got %v", args["old_text"])
	}
	if newText, ok := args["new_text"].(string); !ok || newText != "bar" {
		t.Errorf("want new_text=bar, got %v", args["new_text"])
	}
}

func TestExtractFallbackToolCall_NoRegistryValidation(t *testing.T) {
	// Test with nil registry - should accept any tool name
	input := "```json\n{\"name\":\"unknown_tool\",\"arguments\":{}}\n```"
	calls, _ := ExtractFallbackToolCall(input, nil)

	if len(calls) != 1 {
		t.Fatalf("with nil registry, want 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "unknown_tool" {
		t.Errorf("want name unknown_tool, got %s", calls[0].Function.Name)
	}
}

func TestExtractFallbackToolCall_RemovesEmbeddedJSONLine(t *testing.T) {
	mockProvider := &mockProvider{}
	registry := tools.NewRegistry(mockProvider)

	input := "   {\"name\": \"run_command\", \"arguments\": {\"command\": \"df -BG /run/media/tj/drive | awk 'NR==2 {print $4}'\"}}\nThere is 23 GB of free space on /run/media/tj/drive."
	calls, cleaned := ExtractFallbackToolCall(input, registry)

	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "run_command" {
		t.Fatalf("want name run_command, got %s", calls[0].Function.Name)
	}
	if strings.Contains(cleaned, "run_command") || strings.Contains(cleaned, "arguments") {
		t.Fatalf("expected embedded tool JSON to be removed, got cleaned=%q", cleaned)
	}
	if strings.TrimSpace(cleaned) != "There is 23 GB of free space on /run/media/tj/drive." {
		t.Fatalf("unexpected cleaned content: %q", cleaned)
	}
}

func TestExtractFallbackToolCall_QwenModelFormats(t *testing.T) {
	mockProvider := &mockProvider{}
	registry := tools.NewRegistry(mockProvider)

	tests := []struct {
		name      string
		input     string
		wantCalls int
		wantName  string
	}{
		{
			name:      "qwen2.5-coder inline markdown",
			input:     "I'll search for that pattern.\n\n```json\n{\n  \"name\": \"search_code\",\n  \"arguments\": {\n    \"pattern\": \"func.*Handler\",\n    \"globs\": \"*.go\"\n  }\n}\n```\n\nThis will find all handlers.",
			wantCalls: 1,
			wantName:  "search_code",
		},
		{
			name:      "qwen3-coder multiline with comments",
			input:     "Let me check the file structure:\n\n```json\n{\n  \"name\": \"list_directory\",\n  \"arguments\": {\n    \"path\": \".\",\n    \"depth\": 2\n  }\n}\n```\n\nThis shows the project layout.",
			wantCalls: 1,
			wantName:  "list_directory",
		},
		{
			name:      "gemma4:8b markdown with extra whitespace",
			input:     "```json\n\n{\n\t\"name\":\"read_file\",\n\t\"arguments\":{\n\t\t\"path\":\"go.mod\"\n\t}\n}\n\n```",
			wantCalls: 1,
			wantName:  "read_file",
		},
		{
			name:      "condensed single-line markdown",
			input:     "```json{\"name\":\"run_command\",\"arguments\":{\"command\":\"go test ./...\"}}```",
			wantCalls: 1,
			wantName:  "run_command",
		},
		{
			name:      "mixed content with multiple tool blocks",
			input:     "First, I'll read the file:\n```json\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"main.go\"}}\n```\n\nThen edit it:\n```json\n{\"name\":\"edit_file\",\"arguments\":{\"path\":\"main.go\",\"old_text\":\"old\",\"new_text\":\"new\"}}\n```\n\nDone.",
			wantCalls: 2,
			wantName:  "read_file",
		},
		{
			name:      "markdown with type specifier",
			input:     "I found the issue. Here's the fix:\n\n```json\n{\n  \"name\": \"write_file\",\n  \"arguments\": {\n    \"path\": \"config.yaml\",\n    \"content\": \"version: 1\\nkey: value\"\n  }\n}\n```",
			wantCalls: 1,
			wantName:  "write_file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, cleaned := ExtractFallbackToolCall(tt.input, registry)
			if len(calls) != tt.wantCalls {
				t.Errorf("want %d calls, got %d", tt.wantCalls, len(calls))
				return
			}
			if tt.wantCalls > 0 && calls[0].Function.Name != tt.wantName {
				t.Errorf("want name %s, got %s", tt.wantName, calls[0].Function.Name)
			}
			if tt.wantCalls > 0 && strings.Contains(cleaned, "```json") {
				t.Errorf("markdown blocks not fully removed from cleaned content")
			}
		})
	}
}

func TestExtractFallbackToolCall_EdgeCases(t *testing.T) {
	mockProvider := &mockProvider{}
	registry := tools.NewRegistry(mockProvider)

	tests := []struct {
		name      string
		input     string
		wantCalls int
	}{
		{
			name:      "empty arguments",
			input:     "```json\n{\"name\":\"git_status\",\"arguments\":{}}\n```",
			wantCalls: 1,
		},
		{
			name:      "nested object arguments",
			input:     "```json\n{\"name\":\"run_test\",\"arguments\":{\"test\":{\"file\":\"main_test.go\"}}}\n```",
			wantCalls: 1,
		},
		{
			name:      "array in arguments",
			input:     "```json\n{\"name\":\"edit_file\",\"arguments\":{\"path\":\"file.go\",\"old_text\":\"a\",\"new_text\":\"b\"}}\n```",
			wantCalls: 1,
		},
		{
			name:      "escaped quotes in arguments",
			input:     "```json\n{\"name\":\"write_file\",\"arguments\":{\"path\":\"test.txt\",\"content\":\"line 1\\nline 2\"}}\n```",
			wantCalls: 1,
		},
		{
			name:      "no code fence - plain JSON",
			input:     "{\"name\":\"list_directory\",\"arguments\":{\"path\":\".\"}}\n",
			wantCalls: 1,
		},
		{
			name:      "invalid JSON ignored",
			input:     "```json\n{invalid json}\n```",
			wantCalls: 0,
		},
		{
			name:      "missing name field",
			input:     "```json\n{\"arguments\":{}}\n```",
			wantCalls: 0,
		},
		{
			name:      "text surrounding tool call",
			input:     "Here's what I'll do. First, I'll search:\n\n```json\n{\"name\":\"search_code\",\"arguments\":{\"pattern\":\"TODO\"}}\n```\n\nThen I'll report back.",
			wantCalls: 1,
		},
		{
			name:      "embedded plain JSON tool call removed",
			input:     "   {\"name\": \"run_command\", \"arguments\": {\"command\": \"df -BG /run/media/tj/drive | awk 'NR==2 {print $4}'\"}}\nThere is 23 GB of free space on /run/media/tj/drive.",
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, _ := ExtractFallbackToolCall(tt.input, registry)
			if len(calls) != tt.wantCalls {
				t.Errorf("want %d calls, got %d (input: %q)", tt.wantCalls, len(calls), tt.input)
			}
		})
	}
}

func TestExtractFallbackToolCall_EscapedAngleWrapper(t *testing.T) {
	mockProvider := &mockProvider{}
	registry := tools.NewRegistry(mockProvider)

	// Models like minimax-m3 sometimes emit tool calls inside commentary using
	// single-angle-quote (U+2039 / U+203A) wrappers around <tool_call>...</tool_call>.
	tests := []struct {
		name      string
		input     string
		wantCalls int
		wantName  string
	}{
		{
			name:      "single-angle-quote wrapper with inline JSON",
			input:     "Let me run that.\n\u2039tool_call\u203a{\"name\":\"run_command\",\"arguments\":{\"command\":\"uname -a\"}}\u2039/tool_call\u203a\nThanks.",
			wantCalls: 1,
			wantName:  "run_command",
		},
		{
			name:      "literal angle bracket wrapper",
			input:     "<tool_call>{\"name\":\"list_directory\",\"arguments\":{\"path\":\".\"}}</tool_call>",
			wantCalls: 1,
			wantName:  "list_directory",
		},
		{
			name:      "wrapper with fenced JSON inside",
			input:     "\u2039tool_call\u203a```json\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"go.mod\"}}\n```\u2039/tool_call\u203a",
			wantCalls: 1,
			wantName:  "read_file",
		},
		{
			name:      "wrapper with unknown tool ignored",
			input:     "\u2039tool_call\u203a{\"name\":\"not_a_real_tool\",\"arguments\":{}}\u2039/tool_call\u203a",
			wantCalls: 0,
		},
		{
			name:      "wrapper removed from cleaned content",
			input:     "Here you go:\n\u2039tool_call\u203a{\"name\":\"write_file\",\"arguments\":{\"path\":\"a.txt\",\"content\":\"x\"}}\u2039/tool_call\u203a\nAll done.",
			wantCalls: 1,
			wantName:  "write_file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, cleaned := ExtractFallbackToolCall(tt.input, registry)
			if len(calls) != tt.wantCalls {
				t.Errorf("want %d calls, got %d (input: %q)", tt.wantCalls, len(calls), tt.input)
			}
			if tt.wantCalls > 0 && calls[0].Function.Name != tt.wantName {
				t.Errorf("want name %s, got %s", tt.wantName, calls[0].Function.Name)
			}
			if tt.wantCalls > 0 && (strings.Contains(cleaned, "tool_call") || strings.Contains(cleaned, "‹tool_call") || strings.Contains(cleaned, "\u2039tool_call")) {
				t.Errorf("wrapper not removed from cleaned content: %q", cleaned)
			}
		})
	}
}
