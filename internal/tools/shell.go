package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
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
			Description: "Run a shell command inside the workspace with timeout protection. Set background=true for long-running or parallel work that should continue after the current turn.",
			Parameters: objectSchema([]string{"command"}, map[string]any{
				"command":         stringProp("Shell command to execute."),
				"cwd":             stringProp("Optional working directory."),
				"timeout_seconds": numberProp("Optional timeout in seconds. Default 20, max 300."),
				"background":      boolProp("If true, start the command in the background and return immediately with a job id."),
			}),
		},
	}
}

func (runCommandTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Command        string `json:"command"`
		Cwd            string `json:"cwd"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Background     bool   `json:"background"`
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
	if args.Background {
		if env.SessionState == nil || env.PersistSession == nil {
			return Result{Content: "Background execution unavailable: session persistence is not configured.", Preview: "background unavailable"}, nil
		}
		jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
		jobDir := filepath.Join(env.WorkspaceRoot, ".ergo-cli-go", "jobs", jobID)
		if err := os.MkdirAll(jobDir, 0o700); err != nil {
			return Result{}, fmt.Errorf("create job dir: %w", err)
		}
		logPath := filepath.Join(jobDir, "output.log")
		job := session.BackgroundJob{
			ID:          jobID,
			Command:     args.Command,
			Cwd:         args.Cwd,
			Status:      "running",
			StartedAt:   time.Now().UTC().Format(time.RFC3339),
			OutputPath:  logPath,
			SessionPath: filepath.Join(jobDir, "session.json"),
		}
		env.SessionState.BackgroundJobs = append(env.SessionState.BackgroundJobs, job)
		if err := env.PersistSession(); err != nil {
			return Result{}, fmt.Errorf("persist background job: %w", err)
		}

		// Background jobs must outlive the current conversation turn, so
		// they are detached from the turn context. Tying them to ctx
		// caused the job to be cancelled the moment the turn ended,
		// defeating the purpose of background execution. A generous
		// independent timeout still guards against runaway processes.
		bgTimeout := time.Duration(args.TimeoutSeconds) * time.Second
		if bgTimeout < time.Hour {
			bgTimeout = time.Hour
		}
		runCtx, cancel := context.WithTimeout(context.Background(), bgTimeout)
		cmd := exec.CommandContext(runCtx, "bash", "-lc", args.Command)
		if args.Cwd != "" {
			cmd.Dir = resolvePath(env.WorkspaceRoot, args.Cwd)
		} else {
			cmd.Dir = filepath.Clean(env.WorkspaceRoot)
		}
		logFile, err := os.Create(logPath)
		if err != nil {
			cancel()
			job.Status = "failed"
			job.Error = fmt.Sprintf("create log file: %v", err)
			job.CompletedAt = time.Now().UTC().Format(time.RFC3339)
			updateBackgroundJob(env, jobID, job)
			return Result{}, fmt.Errorf("create log file: %w", err)
		}
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			cancel()
			logFile.Close()
			job.Status = "failed"
			job.Error = fmt.Sprintf("start command: %v", err)
			job.CompletedAt = time.Now().UTC().Format(time.RFC3339)
			updateBackgroundJob(env, jobID, job)
			return Result{}, fmt.Errorf("start background command: %w", err)
		}
		go func() {
			defer logFile.Close()
			defer cancel()
			err := cmd.Wait()
			if err != nil {
				job.Status = "failed"
				job.Error = err.Error()
			} else {
				job.Status = "completed"
			}
			job.CompletedAt = time.Now().UTC().Format(time.RFC3339)
			updateBackgroundJob(env, jobID, job)
		}()
		msg := fmt.Sprintf("Started background job %s", jobID)
		return Result{Content: msg, Preview: msg}, nil
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

func updateBackgroundJob(env ExecEnv, id string, job session.BackgroundJob) {
	if env.SessionState == nil || env.PersistSession == nil {
		return
	}
	for i := range env.SessionState.BackgroundJobs {
		if env.SessionState.BackgroundJobs[i].ID == id {
			env.SessionState.BackgroundJobs[i] = job
			_ = env.PersistSession()
			return
		}
	}
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
