package tooling

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

type fakeMCPTool struct{}

func (fakeMCPTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{Type: "function", Function: client.FunctionDefinition{Name: "mcp_remote_ping", Description: "ping"}}
}

func (fakeMCPTool) Execute(context.Context, json.RawMessage, tools.ExecEnv) (tools.Result, error) {
	return tools.Result{}, nil
}

// hookProvider runs onFirst after answering the first request,
// simulating `/mcp add` landing mid-turn via invoke_cli_command.
type hookProvider struct {
	scriptedProvider
	onFirst func()
}

func (p *hookProvider) Chat(ctx context.Context, req client.ChatRequest, onEvent func(client.StreamEvent) error) (client.Message, error) {
	msg, err := p.scriptedProvider.Chat(ctx, req, onEvent)
	if len(p.requests) == 1 && p.onFirst != nil {
		p.onFirst()
	}
	return msg, err
}

func offeredNames(req client.ChatRequest) map[string]bool {
	out := map[string]bool{}
	for _, d := range req.Tools {
		out[d.Function.Name] = true
	}
	return out
}

func TestRunner_LiveEnabledToolsOffersMidTurnMCPTool(t *testing.T) {
	provider := &hookProvider{scriptedProvider: scriptedProvider{responses: []client.Message{
		{Role: "assistant", ToolCalls: []client.ToolCall{{Type: "function", Function: client.ToolFunctionCall{Name: "list_directory", Arguments: json.RawMessage(`{"path":".","depth":1}`)}}}},
		{Role: "assistant", Content: "done"},
	}}}
	registry := tools.NewRegistry(provider)
	provider.onFirst = func() { registry.RegisterTool(fakeMCPTool{}) }
	live := func() []string {
		names := []string{"list_directory"}
		if registry.Has("mcp_remote_ping") {
			names = append(names, "mcp_remote_ping")
		}
		return names
	}
	runner := &Runner{Provider: provider, Registry: registry, WorkspaceRoot: t.TempDir(), MaxSteps: 4, LiveEnabledTools: live}
	if _, err := runner.Run(context.Background(), nil, "go", agent.Config{Name: "t", Prompt: "p"}, "m", []string{"list_directory"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("want 2 requests, got %d", len(provider.requests))
	}
	if offeredNames(provider.requests[0])["mcp_remote_ping"] {
		t.Fatal("first request should not offer the MCP tool yet")
	}
	if !offeredNames(provider.requests[1])["mcp_remote_ping"] {
		t.Fatal("second request did not offer the mid-turn MCP tool")
	}
}
