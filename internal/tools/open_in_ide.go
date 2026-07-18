package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tjcoder-labs/cli/internal/client"
)

type openInIDETool struct{}

func (openInIDETool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "open_in_ide",
			Description: `Open a file in your IDE (VS Code by default) so you can co-develop.
Optionally specify a line number to jump to. This is the primary way to invite the user to join you in editing code.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{
						"type":        "string",
						"description": "File path (relative to workspace root or absolute)",
					},
					"line": map[string]any{
						"type":        "integer",
						"description": "Optional line number to jump to",
					},
					"column": map[string]any{
						"type":        "integer",
						"description": "Optional column number",
					},
				},
				"required": []string{"file"},
			},
		},
	}
}

type openIDEArgs struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

func (openInIDETool) Execute(ctx context.Context, args json.RawMessage, env ExecEnv) (Result, error) {
	var a openIDEArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, fmt.Errorf("invalid arguments: %w", err)
	}

	if a.File == "" {
		return Result{}, fmt.Errorf("file path is required")
	}

	// Resolve file path
	filePath := a.File
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(env.WorkspaceRoot, filePath)
	}
	filePath = filepath.Clean(filePath)

	// Verify file exists
	if _, err := os.Stat(filePath); err != nil {
		return Result{}, fmt.Errorf("file not found: %s", filePath)
	}

	// Attempt to open with code CLI
	if err := openInVSCode(filePath, a.Line, a.Column); err == nil {
		msg := formatIDESuccess(filePath, env.WorkspaceRoot, a.Line, a.Column)
		return Result{Content: msg}, nil
	}

	// Fall back to $EDITOR if code is not available
	if editor := os.Getenv("EDITOR"); editor != "" {
		if err := openInEditor(editor, filePath); err == nil {
			msg := fmt.Sprintf("Opened %s in $EDITOR", a.File)
			return Result{Content: msg}, nil
		}
	}

	return Result{}, fmt.Errorf("could not open file: VS Code not found and $EDITOR not set. Install VS Code or set $EDITOR env var")
}

func openInVSCode(filePath string, line, column int) error {
	var cmdArgs []string
	if line > 0 && column > 0 {
		cmdArgs = []string{"--goto", fmt.Sprintf("%s:%d:%d", filePath, line, column)}
	} else if line > 0 {
		cmdArgs = []string{"--goto", fmt.Sprintf("%s:%d", filePath, line)}
	} else {
		cmdArgs = []string{filePath}
	}

	cmd := exec.Command("code", cmdArgs...)
	return cmd.Run()
}

func openInEditor(editor, filePath string) error {
	cmd := exec.Command(editor, filePath)
	return cmd.Run()
}

func formatIDESuccess(filePath, workspaceRoot string, line, column int) string {
	relPath, err := filepath.Rel(workspaceRoot, filePath)
	if err != nil {
		relPath = filePath
	}

	if line > 0 && column > 0 {
		return fmt.Sprintf("Opening %s (line %d, col %d) in VS Code", relPath, line, column)
	} else if line > 0 {
		return fmt.Sprintf("Opening %s (line %d) in VS Code", relPath, line)
	}
	return fmt.Sprintf("Opening %s in VS Code", relPath)
}
