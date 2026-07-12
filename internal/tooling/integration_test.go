package tooling

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alpha-tjcoder/coder-cli/internal/agent"
	"github.com/alpha-tjcoder/coder-cli/internal/client"
	"github.com/alpha-tjcoder/coder-cli/internal/tools"
)

type integrationScriptedProvider struct {
	requests  []client.ChatRequest
	responses []client.Message
}

func (p *integrationScriptedProvider) Chat(_ context.Context, req client.ChatRequest, onEvent func(client.StreamEvent) error) (client.Message, error) {
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

func (p *integrationScriptedProvider) ListModels(context.Context) ([]client.ModelInfo, error) {
	return nil, nil
}

func (p *integrationScriptedProvider) ContextWindow(context.Context, string) (int, error) {
	return 8192, nil
}

func (p *integrationScriptedProvider) BaseURL() string {
	return "http://localhost:11434"
}

func TestToolInvocationLoopWithQwenMarkdownFormat(t *testing.T) {
	// Simulate qwen2.5-coder outputting markdown JSON instead of proper tool calls
	provider := &integrationScriptedProvider{
		responses: []client.Message{
			{
				Role:    "assistant",
				Content: "I'll search the codebase for error handlers.\n\n```json\n{\"name\":\"search_code\",\"arguments\":{\"pattern\":\"func.*Error\",\"globs\":\"*.go\"}}\n```\n\nThis will find all error handling functions.",
			},
			{
				Role:    "assistant",
				Content: "Great! I found the patterns. Now let me read the main error handler.",
			},
		},
	}

	registry := tools.NewRegistry(provider)
	runner := &Runner{
		Provider:      provider,
		Registry:      registry,
		WorkspaceRoot: t.TempDir(),
		MaxSteps:      2,
	}

	history, err := runner.Run(
		context.Background(),
		nil,
		"find error handling patterns",
		agent.Config{
			Name:   "code-reviewer",
			Prompt: "Review code for error handling.",
		},
		"qwen2.5-coder:7b",
		[]string{"search_code", "read_file"},
		nil,
	)

	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if len(history) == 0 {
		t.Fatal("expected non-empty history")
	}

	// Verify the markdown tool call was extracted
	found := false
	for _, msg := range history {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.Function.Name == "search_code" {
					found = true
					var args map[string]interface{}
					_ = json.Unmarshal(tc.Function.Arguments, &args)
					if pattern, ok := args["pattern"].(string); !ok || pattern != "func.*Error" {
						t.Errorf("want pattern=func.*Error, got %v", args["pattern"])
					}
				}
			}
		}
	}

	if !found {
		t.Fatal("expected search_code tool to be extracted from markdown content")
	}
}

func TestToolInvocationWithPlainJSON(t *testing.T) {
	// Simulate model outputting naked JSON (no markdown fence)
	provider := &integrationScriptedProvider{
		responses: []client.Message{
			{
				Role:    "assistant",
				Content: "Let me check the project structure:\n{\"name\":\"list_directory\",\"arguments\":{\"path\":\".\",\"depth\":2}}\n\nThis shows all files and subdirectories.",
			},
			{
				Role:    "assistant",
				Content: "The project looks well organized.",
			},
		},
	}

	registry := tools.NewRegistry(provider)
	runner := &Runner{
		Provider:      provider,
		Registry:      registry,
		WorkspaceRoot: t.TempDir(),
		MaxSteps:      2,
	}

	history, err := runner.Run(
		context.Background(),
		nil,
		"inspect project",
		agent.Config{
			Name:   "software-engineer",
			Prompt: "Review the project structure.",
		},
		"gemma4:8b",
		[]string{"list_directory"},
		nil,
	)

	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	found := false
	for _, msg := range history {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.Function.Name == "list_directory" {
					found = true
				}
			}
		}
	}

	if !found {
		t.Fatal("expected list_directory tool to be extracted from plain JSON")
	}
}

func TestMultipleToolInvocationsInSingleMessage(t *testing.T) {
	// Simulate model invoking multiple tools in a single message
	provider := &integrationScriptedProvider{
		responses: []client.Message{
			{
				Role:    "assistant",
				Content: "I'll analyze the code. First, let me search for TODOs:\n```json\n{\"name\":\"search_code\",\"arguments\":{\"pattern\":\"TODO\",\"globs\":\"*.go\"}}\n```\n\nThen read the main file:\n```json\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"main.go\"}}\n```\n\nThis will give me all the context I need.",
			},
			{
				Role:    "assistant",
				Content: "Analysis complete.",
			},
		},
	}

	registry := tools.NewRegistry(provider)
	runner := &Runner{
		Provider:      provider,
		Registry:      registry,
		WorkspaceRoot: t.TempDir(),
		MaxSteps:      2,
	}

	history, err := runner.Run(
		context.Background(),
		nil,
		"analyze codebase",
		agent.Config{
			Name:   "software-engineer",
			Prompt: "Analyze the code.",
		},
		"qwen3-coder",
		[]string{"search_code", "read_file"},
		nil,
	)

	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	toolCount := 0
	for _, msg := range history {
		if msg.Role == "assistant" {
			toolCount += len(msg.ToolCalls)
		}
	}

	if toolCount != 2 {
		t.Errorf("want 2 tools extracted, got %d", toolCount)
	}
}
