package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

// CLICommandSink is the interface that the TUI implements to allow tools
// to invoke slash commands on behalf of the agent. Examples: /tasks, /config,
// /clear, /agent, /model, etc. The TUI queues these for execution in the
// event loop to ensure thread safety.
type CLICommandSink interface {
	InvokeCLICommand(command string) error
}

// CLICommandSink returns the CLI command sink wired to this env, or nil
// if none is configured (e.g. headless mode).
func (e ExecEnv) CLICommandSink() CLICommandSink {
	return e.CommandSink
}

// invokeCliCommandTool allows the agent to proactively invoke CLI commands
// (slash commands) on behalf of the user. This enables workflows like:
// - After creating a task, invoke /tasks to display the tasks pane
// - Switch models via /model
// - Toggle panels to show results (e.g., /articles, /code)
type invokeCliCommandTool struct{}

func (invokeCliCommandTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "invoke_cli_command",
			Description: `Run a TUI slash command for the user (without the leading slash) to open an in-TUI feature, option picker, or setting instead of telling them what to type. Use ui_control, not this, to switch presentation panes. Examples:
- config: open settings
- model / model <name>: open the model picker or switch model
- agent / agent <name>: open the agent picker or switch agent
- providers, mcp, tools, environment, dirs, balance, memories, reminders, tasks, help
Do not run destructive or state-discarding commands (quit, clear, compact, logout, update) unless the user explicitly asks.`,
			Parameters: objectSchema([]string{"command"}, map[string]any{
				"command": stringProp("The slash command to invoke, without the leading slash. Examples: 'config', 'model', 'agent software-engineer', 'providers', 'tasks'"),
			}),
		},
	}
}

type invokeCliCommandArgs struct {
	Command string `json:"command"`
}

func (invokeCliCommandTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args invokeCliCommandArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("invalid arguments: %w", err)
	}

	command := strings.TrimSpace(args.Command)
	if command == "" {
		return Result{}, fmt.Errorf("command is required")
	}

	// Check if we have a sink (TUI mode) or if we're in headless mode
	sink := env.CLICommandSink()
	if sink == nil {
		// Headless mode: just report what command would be invoked
		msg := fmt.Sprintf("Would invoke: /%s (headless mode)", command)
		return Result{Content: msg, Preview: msg}, nil
	}

	// Invoke the command through the sink
	if err := sink.InvokeCLICommand(command); err != nil {
		return Result{}, fmt.Errorf("failed to invoke command /%s: %w", command, err)
	}

	msg := fmt.Sprintf("Invoked: /%s", command)
	return Result{Content: msg, Preview: msg}, nil
}
