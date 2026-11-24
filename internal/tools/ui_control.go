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
//
// The canvas also has a watch mode that streams a shell command's live,
// colourised output (the same canvas surface, a different content type):
//
//	{"action":"show","panel":"canvas","command":"go test ./...","interval_seconds":0}
//	{"action":"stop","panel":"canvas"}   // end the watch
//
// panel "watch" is accepted as an alias for the canvas in watch mode.
//
// Watch commands pass the same checks as run_command (CheckWatchCommand)
// before a marker is emitted, and the TUI re-checks them before starting.
type uiControlTool struct{}

func (uiControlTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "ui_control",
			Description: "Switch the TUI right-hand presentation pane to match what you are doing; re-evaluate every turn. " +
				"Panels: 'canvas' (render a file, optionally a highlighted line range, while you discuss, edit, or draft code/config/docs), " +
				"'tasks' (after task changes or while planning), 'memories' (after memory changes), 'articles', and 'activity' (default). " +
				"Canvas watch mode: pass command (instead of path) to stream that shell command's live, colourised output in the canvas; interval_seconds > 0 re-runs it like watch -n; action 'stop' ends the watch. " +
				"Use it so the user can follow a dev server, log tail, or test run; use run_command when you need the output yourself. sudo is not allowed in a watch. " +
				"For canvas, size start_line/end_line to canvas_width_cols × canvas_visible_lines from tui_context, counting wrapped long lines, " +
				"and prefer the relevant function/block/config section over the whole file. " +
				"To open a feature, picker, or setting (model, agent, config), use invoke_cli_command instead.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":           map[string]any{"type": "string", "description": "show|hide|toggle (canvas watch mode also: stop)"},
					"panel":            map[string]any{"type": "string", "description": "panel name: canvas, tasks, memories, articles, activity"},
					"command":          map[string]any{"type": "string", "description": "canvas only: shell command whose live output the canvas streams (watch mode; runs in the active local workspace)"},
					"interval_seconds": map[string]any{"type": "integer", "description": "canvas watch mode only: re-run the command every N seconds (1-3600) instead of streaming one run"},
					"path":             map[string]any{"type": "string", "description": "canvas only: file to render (relative to workspace or absolute)"},
					"start_line":       map[string]any{"type": "integer", "description": "canvas only: first line to highlight (1-based, optional)"},
					"end_line":         map[string]any{"type": "integer", "description": "canvas only: last line to highlight (1-based, optional)"},
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
	Command   string `json:"command"`
	Interval  int    `json:"interval_seconds"`
}

// CheckWatchCommand applies run_command's safety checks to a command the
// TUI's watch canvas is about to run: the disallowed-pattern list, and no
// sudo at all (a watch has no way to show the sudo authorization card, and
// its process is detached from the terminal).
func CheckWatchCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("watch needs a command")
	}
	if err := safeCommand(command); err != nil {
		return err
	}
	if IsSudoCommand(command) {
		return fmt.Errorf("%w: sudo is not supported in a watch; use run_command, which asks for authorization", errSudoDenied)
	}
	return nil
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
	// NOTE: the runner emits result.Preview as EventToolResult.Text, so the
	// marker must be carried in Preview as well as Content; the TUI handler
	// intercepts the marker and logs its own succinct line.
	if panel == "watch" || (panel == "canvas" && (strings.TrimSpace(a.Command) != "" || action == "stop")) {
		return watchMarker(action, a)
	}
	marker := fmt.Sprintf("panel:%s:%s", panel, action)
	if panel == "canvas" && strings.TrimSpace(a.Path) != "" {
		marker = fmt.Sprintf("panel:canvas:%s:%s:%s:%s",
			action, strconv.Itoa(a.StartLine), strconv.Itoa(a.EndLine), a.Path)
	}

	return Result{Content: marker, Preview: marker}, nil
}

// watchMarker encodes the canvas watch-mode marker
// "panel:canvas:watch:<action>[:<interval>:<command>]"; the command is
// last so it may contain colons.
func watchMarker(action string, a uiControlArgs) (Result, error) {
	switch action {
	case "show", "toggle":
		if strings.TrimSpace(a.Command) == "" {
			marker := "panel:canvas:watch:" + action
			return Result{Content: marker, Preview: marker}, nil
		}
		if err := CheckWatchCommand(a.Command); err != nil {
			return Result{}, err
		}
		if a.Interval < 0 || a.Interval > 3600 {
			return Result{}, fmt.Errorf("interval_seconds must be 0-3600")
		}
		marker := fmt.Sprintf("panel:canvas:watch:%s:%d:%s", action, a.Interval, strings.TrimSpace(a.Command))
		return Result{Content: marker, Preview: marker}, nil
	case "hide", "stop":
		marker := "panel:canvas:watch:" + action
		return Result{Content: marker, Preview: marker}, nil
	}
	return Result{}, fmt.Errorf("unknown watch action %q (show, hide, toggle, stop)", action)
}
