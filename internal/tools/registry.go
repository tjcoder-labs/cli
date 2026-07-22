package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

type Result struct {
	Content string
	Preview string
}

type ExecEnv struct {
	WorkspaceRoot string
	Provider      client.Provider
	// Sink, when non-nil, lets tools push rendered code into the
	// TUI's /code panel. nil means "headless" — the highlight_code
	// tool will return the rendered string as its tool result
	// instead of streaming it to a panel.
	Sink HighlightSink
	// CommandSink, when non-nil, lets tools invoke slash commands on
	// behalf of the agent. nil means "headless" — the invoke_cli_command
	// tool will return a message about what would have been invoked.
	CommandSink CLICommandSink
	// SessionState and PersistSession let tools persist background jobs and
	// other session-scoped metadata outside the active turn.
	SessionState   *session.State
	PersistSession func() error
}

// HighlightSink returns the /code panel sink wired to this env, or
// nil if none is configured.
func (e ExecEnv) HighlightSink() HighlightSink {
	return e.Sink
}

type Tool interface {
	Definition() client.ToolDefinition
	Execute(context.Context, json.RawMessage, ExecEnv) (Result, error)
}

type Registry struct {
	provider client.Provider
	tools    map[string]Tool
}

func NewRegistry(provider client.Provider) *Registry {
	r := &Registry{
		provider: provider,
		tools:    map[string]Tool{},
	}
	for _, tool := range []Tool{
		searchCodeTool{},
		readFileTool{},
		listDirectoryTool{},
		runCommandTool{},
		editFileTool{},
		createFileTool{},
		writeFileTool{},
		// additional core tools
		fetchTool{},
		appendFileTool{},
		deleteFileTool{},
		moveFileTool{},
		gitLogTool{},
		// existing tools
		gitStatusTool{},
		runTestTool{},
		inspectProjectTool{},
		listAvailableModelsTool{},
		// scheduler / reminders
		reminderTool{},
		// IDE integration
		openInIDETool{},
		// UI control
		uiControlTool{},
		invokeCliCommandTool{},
	} {
		r.tools[tool.Definition().Function.Name] = tool
	}
	return r
}

// RegisterTool adds a runtime tool to the registry. If a tool with the
// same name already exists it is overwritten.
func (r *Registry) RegisterTool(t Tool) {
	if t == nil {
		return
	}
	name := t.Definition().Function.Name
	r.tools[name] = t
}

// UnregisterTool removes a single tool by name. No-op if not present.
func (r *Registry) UnregisterTool(name string) {
	delete(r.tools, name)
}

// UnregisterPrefix removes every tool whose name starts with the
// given prefix. Used by the MCP bridge to wipe `mcp_{server}_*`
// entries when a server is removed.
func (r *Registry) UnregisterPrefix(prefix string) {
	if prefix == "" {
		return
	}
	for name := range r.tools {
		if strings.HasPrefix(name, prefix) {
			delete(r.tools, name)
		}
	}
}

func (r *Registry) Definitions(names []string) []client.ToolDefinition {
	defs := make([]client.ToolDefinition, 0, len(names))
	for _, name := range names {
		tool, ok := r.tools[name]
		if ok {
			defs = append(defs, tool.Definition())
		}
	}
	return defs
}

func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage, env ExecEnv) (Result, error) {
	tool, ok := r.tools[name]
	if !ok {
		return Result{}, fmt.Errorf("unknown tool %q", name)
	}
	return tool.Execute(ctx, args, env)
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Has(name string) bool {
	_, ok := r.tools[name]
	return ok
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type":       "object",
		"required":   required,
		"properties": properties,
	}
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func numberProp(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func boolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func preview(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= 280 {
		return text
	}
	return text[:280] + "..."
}
