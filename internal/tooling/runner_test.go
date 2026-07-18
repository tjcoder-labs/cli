package tooling

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

type scriptedProvider struct {
	requests  []client.ChatRequest
	responses []client.Message
}

func (p *scriptedProvider) Chat(_ context.Context, req client.ChatRequest, onEvent func(client.StreamEvent) error) (client.Message, error) {
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return client.Message{}, context.DeadlineExceeded
	}
	msg := p.responses[0]
	p.responses = p.responses[1:]
	if onEvent != nil && msg.Content != "" {
		_ = onEvent(client.StreamEvent{Commentary: msg.Content})
	}
	return msg, nil
}

func (p *scriptedProvider) ListModels(context.Context) ([]client.ModelInfo, error) {
	return nil, nil
}

func (p *scriptedProvider) ContextWindow(context.Context, string) (int, error) {
	return 8192, nil
}

func (p *scriptedProvider) BaseURL() string {
	return "http://localhost:11434"
}

func TestRunner_LoadsCoderPrompt(t *testing.T) {
	provider := &scriptedProvider{responses: []client.Message{{Role: "assistant", Content: "ok"}}}
	registry := tools.NewRegistry(provider)
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "CODER.md"), []byte("CODER instructions"), 0o600); err != nil {
		t.Fatalf("failed to write CODER.md: %v", err)
	}

	runner := &Runner{
		Provider:      provider,
		Registry:      registry,
		WorkspaceRoot: workspaceRoot,
		MaxSteps:      1,
	}

	_, err := runner.Run(context.Background(), nil, "hello", agent.Config{Name: "test-agent", Prompt: "base prompt"}, "model", nil, nil)
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}

	if len(provider.requests) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(provider.requests))
	}

	if got := provider.requests[0].Messages[0].Content; !strings.Contains(got, "CODER instructions") {
		t.Fatalf("expected injected CODER.md in system prompt, got %q", got)
	}
}

func TestRunner_MaxStepsGeneratesCheckpointResponse(t *testing.T) {
	provider := &scriptedProvider{
		responses: []client.Message{
			{
				Role: "assistant",
				ToolCalls: []client.ToolCall{
					{
						Type: "function",
						Function: client.ToolFunctionCall{
							Name:      "list_directory",
							Arguments: json.RawMessage(`{"path":".","depth":1}`),
						},
					},
				},
			},
			{
				Role:    "assistant",
				Content: "Tool budget reached. I can continue in another turn if you want.",
			},
		},
	}

	registry := tools.NewRegistry(provider)
	runner := &Runner{
		Provider:      provider,
		Registry:      registry,
		WorkspaceRoot: t.TempDir(),
		MaxSteps:      1,
	}

	history, err := runner.Run(
		context.Background(),
		nil,
		"inspect the workspace",
		agent.Config{Name: "test-agent", Prompt: "You are a test agent."},
		"test-model",
		[]string{"list_directory"},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("expected 2 provider calls (normal + checkpoint), got %d", len(provider.requests))
	}

	if len(provider.requests[1].Tools) != 0 {
		t.Fatalf("expected checkpoint request to disable tools, got %d tool defs", len(provider.requests[1].Tools))
	}

	if got := provider.requests[1].Messages[0].Content; got == "" || !strings.Contains(got, "tool-call budget") || !strings.Contains(got, "Do not call tools") {
		t.Fatalf("expected checkpoint system instruction in second request, got %q", got)
	}

	if len(history) == 0 {
		t.Fatal("expected non-empty history")
	}
	last := history[len(history)-1]
	if last.Role != "assistant" {
		t.Fatalf("expected final message role=assistant, got %q", last.Role)
	}
	if last.Content == "" {
		t.Fatal("expected non-empty checkpoint response content")
	}
}

// TestRunner_ScrubMessageStripsLeakedToolCallMarkup is the unit-level
// guard for the T4 fix: when a model emits `‹tool_call›...‹/tool_call›`
// or `<tool_call>...</tool_call>` wrappers in its prose — even without
// native tool defs — the runner must strip that markup from the
// message content before it ever reaches the transcript or history.
func TestRunner_ScrubMessageStripsLeakedToolCallMarkup(t *testing.T) {
	provider := &scriptedProvider{}
	runner := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), MaxSteps: 1}

	// U+2039 (single left-pointing angle quotation mark) and
	// U+203A (single right-pointing angle quotation mark) are the
	// brackets minimax-m3 emits. We use the literal runes here so
	// the test is unambiguous about what characters we mean.
	openBracket := "\u2039"
	closeBracket := "\u203A"

	cases := []struct {
		name        string
		input       string
		mustNotHave []string
	}{
		{
			name:        "angle-quote wrapper",
			input:       "I will look that up. " + openBracket + "tool_call" + closeBracket + "\n{\"name\":\"list_directory\",\"arguments\":{\"path\":\".\"}}\n" + openBracket + "/tool_call" + closeBracket,
			mustNotHave: []string{openBracket + "tool_call", openBracket + "/tool_call", "list_directory"},
		},
		{
			name:        "literal angle wrapper",
			input:       "<tool_call>\n{\"name\":\"list_directory\",\"arguments\":{}}\n</tool_call>",
			mustNotHave: []string{"<tool_call>", "</tool_call>", "list_directory"},
		},
		{
			name:        "json code fence",
			input:       "Here is what I found:\n```json\n{\"name\":\"list_directory\",\"arguments\":{\"path\":\".\"}}\n```\nDone.",
			mustNotHave: []string{"```", "list_directory"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := runner.scrubMessage(client.Message{Role: "assistant", Content: tc.input})
			for _, bad := range tc.mustNotHave {
				if strings.Contains(out.Content, bad) {
					t.Errorf("scrubbed content still contains %q: %q", bad, out.Content)
				}
			}
		})
	}
}

// TestRunner_CheckpointResponseStripsLeakedToolCalls is the end-to-end
// guard for T4: when the runner hits MaxSteps and falls through to
// the budget-reached checkpoint branch, the final assistant message
// appended to history must NOT contain any leaked `‹tool_call›…` or
// `<tool_call>…</tool_call>` markup. This is the regression that the
// T4 fix addresses.
func TestRunner_CheckpointResponseStripsLeakedToolCalls(t *testing.T) {
	openBracket := "\u2039"
	closeBracket := "\u203A"

	provider := &scriptedProvider{
		responses: []client.Message{
			// Step 1: native tool call consumes the first step.
			{
				Role: "assistant",
				ToolCalls: []client.ToolCall{
					{
						Type: "function",
						Function: client.ToolFunctionCall{
							Name:      "list_directory",
							Arguments: json.RawMessage(`{"path":".","depth":1}`),
						},
					},
				},
			},
			// Step 2: budget-reached checkpoint response — and the
			// model still emits embedded tool markup in its prose
			// even though no native tool defs were sent.
			{
				Role:    "assistant",
				Content: "Tool budget reached. To continue I would call " + openBracket + "tool_call" + closeBracket + "\n{\"name\":\"run_command\",\"arguments\":{}}\n" + openBracket + "/tool_call" + closeBracket + " but I will wait for confirmation.",
			},
		},
	}

	registry := tools.NewRegistry(provider)
	runner := &Runner{
		Provider:      provider,
		Registry:      registry,
		WorkspaceRoot: t.TempDir(),
		MaxSteps:      1,
	}

	history, err := runner.Run(
		context.Background(),
		nil,
		"inspect the workspace",
		agent.Config{Name: "test-agent", Prompt: "You are a test agent."},
		"test-model",
		[]string{"list_directory"},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}

	if len(history) == 0 {
		t.Fatal("expected non-empty history")
	}
	last := history[len(history)-1]
	if last.Role != "assistant" {
		t.Fatalf("expected final message role=assistant, got %q", last.Role)
	}
	for _, bad := range []string{openBracket + "tool_call", openBracket + "/tool_call", "<tool_call>", "</tool_call>", "run_command"} {
		if strings.Contains(last.Content, bad) {
			t.Errorf("checkpoint response still contains leaked markup %q: %q", bad, last.Content)
		}
	}
}
