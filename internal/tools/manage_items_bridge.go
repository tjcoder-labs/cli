package tools

import (
	"context"
	"encoding/json"

	"github.com/alpha-tjcoder/coder-cli/internal/client"
)

// manageItemsBridge adapts the ManageItems helper into the Tool interface
// expected by the runtime registry.
type ManageItemsBridge struct {
	Impl *ManageItems
}

func (m ManageItemsBridge) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "manage_items",
			Description: "Manage trackable objects (tasks, articles, reminders, etc.).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string"},
					"type":   map[string]any{"type": "string"},
					"id":     map[string]any{"type": "string"},
					"data":   map[string]any{"type": "object"},
				},
				"required": []string{"action", "type"},
			},
		},
	}
}

func (m ManageItemsBridge) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	res, err := m.Impl.Call(args)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: res}, nil
}
