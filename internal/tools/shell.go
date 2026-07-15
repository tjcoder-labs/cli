package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tjcoder-labs/coder-cli/internal/client"
)

const maxOutput = 50000

func truncateOutput(text string) string {
	if len(text) <= maxOutput {
		return text
	}
	head := text[:35000]
	tail := text[len(text)-10000:]
	return head + "\n...[truncated]...\n" + tail
}

func safeCommand(command string) error {
	lower := strings.ToLower(command)
	for _, banned := range []string{
		"sudo ",
		" rm -rf /",
		"rm -rf /",
		"shutdown",
		"reboot",
		"mkfs",
		"dd if=",
		"killall",
		"pkill",
		"${var@p}",
		"${!var}",
		"eval ",
	} {
		if strings.Contains(lower, banned) {
			return fmt.Errorf("command contains disallowed pattern %q", banned)
		}
	}
	return nil
}

type runCommandTool struct{}

func (runCommandTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "run_command",
			Description: "Run a shell command inside the workspace with timeout protection.",
			Parameters: objectSchema([]string{"command"}, map[string]any{
				"command":         stringProp("Shell command to execute."),
				"cwd":             stringProp("Optional working directory."),
				"timeout_seconds": numberProp("Optional timeout in seconds. Default 20, max 300."),
			}),
		},
	}
}

func (runCommandTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Command        string `json:"command"`
		Cwd            string `json:"cwd"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if err := safeCommand(args.Command); err != nil {
		return Result{}, err
	}
	if args.TimeoutSeconds <= 0 {
		args.TimeoutSeconds = 20
	}
	if args.TimeoutSeconds > 300 {
		args.TimeoutSeconds = 300
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(args.TimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "bash", "-lc", args.Command)
	if args.Cwd != "" {
		cmd.Dir = resolvePath(env.WorkspaceRoot, args.Cwd)
	} else {
		cmd.Dir = filepath.Clean(env.WorkspaceRoot)
	}
	out, err := cmd.CombinedOutput()
	text := truncateOutput(string(out))
	if err != nil {
		return Result{Content: text, Preview: preview(text)}, fmt.Errorf("command failed: %w", err)
	}
	return Result{Content: text, Preview: preview(text)}, nil
}

type gitStatusTool struct{}

func (gitStatusTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "git_status",
			Description: "Show concise git status and diff stat for the current workspace.",
			Parameters: objectSchema(nil, map[string]any{
				"path": stringProp("Optional repository path."),
			}),
		},
	}
}

func (gitStatusTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	cwd := env.WorkspaceRoot
	if args.Path != "" {
		cwd = resolvePath(env.WorkspaceRoot, args.Path)
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", "git --no-pager status --short --branch && echo '---' && git --no-pager diff --stat")
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	text := truncateOutput(string(out))
	if err != nil {
		return Result{Content: text, Preview: preview(text)}, fmt.Errorf("git status: %w", err)
	}
	return Result{Content: text, Preview: preview(text)}, nil
}

type runTestTool struct{}

func (runTestTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "run_test",
			Description: "Run an explicit test command such as `go test ./...` or `npm test -- auth`.",
			Parameters: objectSchema([]string{"command"}, map[string]any{
				"command":         stringProp("Test command to execute."),
				"cwd":             stringProp("Optional working directory."),
				"timeout_seconds": numberProp("Optional timeout in seconds."),
			}),
		},
	}
}

func (runTestTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	return runCommandTool{}.Execute(ctx, raw, env)
}
