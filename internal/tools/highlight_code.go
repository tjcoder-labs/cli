package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/highlight"
)

// HighlightRequest is the payload a tool (or test) pushes into the
// TUI's code panel. It is exposed so other parts of the codebase
// can trigger the same flow without going through the tool layer
// (e.g. a future /code slash command).
type HighlightRequest struct {
	File      string
	StartLine int
	EndLine   int
	Body      string
}

// HighlightSink is the small surface the highlight_code tool needs
// from the TUI: take a request, render it into the code panel, and
// switch focus to that panel. The TUI implements this and the tool
// pulls it off ExecEnv via the HighlightSink accessor.
type HighlightSink interface {
	ShowHighlightedCode(HighlightRequest) error
}

// highlightCodeTool is the public model-facing tool. It mirrors
// open_in_ide but, instead of shelling out to VS Code, it pushes
// the rendered code into the TUI's /code panel. Useful for
// headless / SSH workflows where the user has no IDE available.
type highlightCodeTool struct{}

func (highlightCodeTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "highlight_code",
			Description: `Render a file (or a line range from a file) into the /code panel of the TJ Coder CLI, with line numbers and syntax highlighting. Use this when you want to walk the user through a piece of code without opening an external editor. The file is always read from disk; the segment is just the window to display.`,
			Parameters: objectSchema([]string{"file"}, map[string]any{
				"file":       stringProp("File path, relative to the workspace root or absolute."),
				"start_line": numberProp("First line to render (1-based, inclusive). 0 or omitted means from the beginning of the file."),
				"end_line":   numberProp("Last line to render (1-based, inclusive). 0 or omitted means to the end of the file."),
			}),
		},
	}
}

type highlightCodeArgs struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

func (highlightCodeTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var a highlightCodeArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(a.File) == "" {
		return Result{}, fmt.Errorf("file is required")
	}

	// Resolve and read the file. Relative paths are anchored at
	// the workspace root; absolute paths are honored as-is.
	filePath := a.File
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(env.WorkspaceRoot, filePath)
	}
	filePath = filepath.Clean(filePath)
	body, err := os.ReadFile(filePath)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", filePath, err)
	}

	// Prefer a HighlightSink on ExecEnvPlus so we can push the
	// rendered code into the /code panel. If the env doesn't
	// expose a sink (e.g. headless ask path), we fall back to
	// returning the rendered string in the tool result so the
	// model can still see the highlighted code in its context.
	// If the env exposes a HighlightSink, push the rendered
	// code into the /code panel. Otherwise (e.g. headless ask
	// path) fall back to returning the rendered string in the
	// tool result so the model can still see the highlighted
	// code in its context.
	req := HighlightRequest{
		File:      filePath,
		StartLine: a.StartLine,
		EndLine:   a.EndLine,
		Body:      string(body),
	}

	if sink := env.HighlightSink(); sink != nil {
		if err := sink.ShowHighlightedCode(req); err != nil {
			return Result{}, fmt.Errorf("render: %w", err)
		}
		rel, _ := filepath.Rel(env.WorkspaceRoot, filePath)
		if rel == "" {
			rel = filePath
		}
		msg := fmt.Sprintf("Highlighting %s (lines %s-%s) in the /code panel",
			rel, lineLabel(a.StartLine), lineLabel(a.EndLine))
		return Result{Content: msg, Preview: msg}, nil
	}

	// Headless fallback: render and return the body.
	rendered, err := highlight.Render(highlight.Segment{
		Path: filePath, Start: a.StartLine, End: a.EndLine, Body: string(body),
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Content: rendered, Preview: preview(rendered)}, nil
}

func lineLabel(n int) string {
	if n <= 0 {
		return "auto"
	}
	return strconv.Itoa(n)
}
