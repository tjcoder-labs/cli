package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

// uiControlTool allows the model to control TUI panels (show/hide/switch).
//
// The agent is expected to proactively drive the right-hand panel so the
// presentation always reflects what it is doing: tasks while planning,
// canvas while reading or drafting a file, activity otherwise.
//
// Arguments:
//
//	{"action":"show|hide|toggle","panel":"tasks|activity|articles|canvas",
//	 "path":"relative/or/abs/path","start_line":12,"end_line":40}
//
// path/start_line/end_line are only meaningful for the "canvas" panel and
// are optional; when omitted the canvas shows a placeholder.
type uiControlTool struct{}

func (uiControlTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "ui_control",
			Description: "Control the TUI right-hand panel to keep the display in sync with what you are doing. " +
				"Panels: 'tasks' (task tracker), 'activity' (tool/activity log), 'articles', and 'canvas' " +
				"(render a file, optionally a line range, for the user to read while you discuss or draft it). " +
				"Show 'canvas' with path/start_line/end_line when presenting code; show 'tasks' when planning; " +
				"otherwise show 'activity'. Re-evaluate every turn which panel best serves the user.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":     map[string]any{"type": "string", "description": "show|hide|toggle"},
					"panel":      map[string]any{"type": "string", "description": "panel name: tasks, activity, articles, canvas"},
					"path":       map[string]any{"type": "string", "description": "canvas only: file to render (relative to workspace or absolute)"},
					"start_line": map[string]any{"type": "integer", "description": "canvas only: first line to highlight (1-based, optional)"},
					"end_line":   map[string]any{"type": "integer", "description": "canvas only: last line to highlight (1-based, optional)"},
				},
				"required": []string{"action", "panel"},
			},
		},
	}
}

type uiControlArgs struct {
	Action    string `json:"action"`
	Panel     string `json:"panel"`
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func (uiControlTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var a uiControlArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Action == "" || a.Panel == "" {
		return Result{}, fmt.Errorf("action and panel are required")
	}

	panel := strings.ToLower(strings.TrimSpace(a.Panel))
	action := strings.ToLower(strings.TrimSpace(a.Action))

	// Encode a marker in Content that the TUI parses in its EventToolResult
	// handler. Base form: "panel:<panel>:<action>". The canvas panel appends
	// the (optional) start:end:path tail. path is placed last so it may
	// contain colons without breaking the SplitN parse on the TUI side.
	marker := fmt.Sprintf("panel:%s:%s", panel, action)
	preview := fmt.Sprintf("%s %s", action, panel)
	if panel == "canvas" && strings.TrimSpace(a.Path) != "" {
		marker = fmt.Sprintf("panel:canvas:%s:%s:%s:%s",
			action, strconv.Itoa(a.StartLine), strconv.Itoa(a.EndLine), a.Path)
		preview = fmt.Sprintf("%s canvas %s", action, a.Path)
		if a.StartLine > 0 {
			preview += fmt.Sprintf(":%d", a.StartLine)
			if a.EndLine > a.StartLine {
				preview += fmt.Sprintf("-%d", a.EndLine)
			}
		}
	}

	return Result{Content: marker, Preview: preview}, nil
}
