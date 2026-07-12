package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alpha-tjcoder/coder-cli/internal/client"
)

// uiControlTool allows the model to control TUI panels (show/hide/switch).
// Arguments: {"action":"show|hide|toggle","panel":"tasks|activity|articles"}
type uiControlTool struct{}

func (uiControlTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "ui_control",
			Description: "Control the TUI panels: show, hide or toggle named panels (tasks, activity, articles).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "description": "show|hide|toggle"},
					"panel":  map[string]any{"type": "string", "description": "panel name: tasks, activity, articles"},
				},
				"required": []string{"action", "panel"},
			},
		},
	}
}

type uiControlArgs struct {
	Action string `json:"action"`
	Panel  string `json:"panel"`
}

func (uiControlTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var a uiControlArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Action == "" || a.Panel == "" {
		return Result{}, fmt.Errorf("action and panel are required")
	}

	// Return a simple marker in Content so the TUI can act on it via onEvent.
	// Format: "panel:<panel>:<action>"
	return Result{Content: fmt.Sprintf("panel:%s:%s", a.Panel, a.Action)}, nil
}
