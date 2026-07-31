package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

const maxOutput = 50000

func readOutput(path string, limit int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(data)
	if limit > 0 && len(text) > limit {
		text = text[len(text)-limit:]
	}
	return text, nil
}

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
			Description: "Run a shell command inside the workspace with timeout protection. Set background=true for long-running or parallel work that should continue after the current turn, then use background_job to read its status and output.",
			Parameters: objectSchema([]string{"command"}, map[string]any{
				"command":         stringProp("Shell command to execute."),
				"cwd":             stringProp("Optional working directory."),
				"timeout_seconds": numberProp("Optional timeout in seconds. Default 20, max 1800."),
				"background":      boolProp("If true, start the command in the background and return immediately with a job id. For interactive scans, prefer a command's native timeout and set timeout_seconds slightly higher."),
				"use_pty":         boolProp("If true, run the command in a pseudo-terminal (PTY). This is required for tools that detect if they are running in a real terminal, like bluetoothctl."),
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
		UsePty         bool   `json:"use_pty"`
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
	if args.TimeoutSeconds > 1800 {
		args.TimeoutSeconds = 1800
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
		msg := fmt.Sprintf("Started background job %s. Use background_job with job_id %s to read its status and output.", jobID, jobID)
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

	if args.UsePty {
		ptmx, tty, err := pty.Open()
		if err != nil {
			return Result{}, fmt.Errorf("pty open: %w", err)
		}
		defer ptmx.Close()

		cmd.Stdout = tty
		cmd.Stdin = tty
		cmd.Stderr = tty
		if err := cmd.Start(); err != nil {
			tty.Close()
			return Result{}, fmt.Errorf("pty start: %w", err)
		}
		// Once the child has the slave fd, we can close it in the parent
		// so reads on ptmx EOF when the child exits.
		tty.Close()

		var buf bytes.Buffer
		done := make(chan error, 1)
		go func() {
			_, err := io.Copy(&buf, ptmx)
			done <- err
		}()

		waitErr := make(chan error, 1)
		go func() { waitErr <- cmd.Wait() }()

		select {
		case <-runCtx.Done():
			// Context timeout or cancellation. Best-effort SIGKILL the
			// child; closing the PTY alone does not stop the process.
			_ = cmd.Process.Kill()
			<-waitErr
		case err := <-waitErr:
			// Process exited; the io.Copy goroutine will close ptmx
			// once it sees EOF, which it will because the slave fd is
			// gone. Wait for the drain so the buffer is complete.
			_ = err
			<-done
		}

		text := truncateOutput(buf.String())
		if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
			return Result{Content: text, Preview: preview(text)}, fmt.Errorf("command failed: exit code %d", cmd.ProcessState.ExitCode())
		}
		return Result{Content: text, Preview: preview(text)}, nil
	}

	out, err := cmd.CombinedOutput()
	text := truncateOutput(string(out))
	if err != nil {
		return Result{Content: text, Preview: preview(text)}, fmt.Errorf("command failed: %w", err)
	}
	return Result{Content: text, Preview: preview(text)}, nil
}

type backgroundJobTool struct{}

func (backgroundJobTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "background_job",
			Description: "Read the status and captured output of a background run_command job.",
			Parameters: objectSchema([]string{"job_id"}, map[string]any{
				"job_id":        stringProp("The job id returned by run_command."),
				"max_bytes":     numberProp("Optional maximum number of output bytes to return from the end of the log. Default 50000."),
				"include_empty": boolProp("If true, return an empty output field when the job has not produced output yet."),
			}),
		},
	}
}

func (backgroundJobTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		JobID        string `json:"job_id"`
		MaxBytes     int    `json:"max_bytes"`
		IncludeEmpty bool   `json:"include_empty"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(args.JobID) == "" {
		return Result{}, fmt.Errorf("job_id is required")
	}
	if args.MaxBytes <= 0 || args.MaxBytes > maxOutput {
		args.MaxBytes = maxOutput
	}
	if env.SessionState == nil {
		return Result{}, fmt.Errorf("background job state is unavailable")
	}
	var job *session.BackgroundJob
	for i := range env.SessionState.BackgroundJobs {
		if env.SessionState.BackgroundJobs[i].ID == args.JobID {
			job = &env.SessionState.BackgroundJobs[i]
			break
		}
	}
	if job == nil {
		return Result{}, fmt.Errorf("background job %q not found", args.JobID)
	}

	output, err := readOutput(job.OutputPath, args.MaxBytes)
	if err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("read background job output: %w", err)
	}
	if err != nil && !args.IncludeEmpty {
		output = "(no output yet)"
	}
	content := fmt.Sprintf("job_id: %s\nstatus: %s\nstarted_at: %s", job.ID, job.Status, job.StartedAt)
	if job.CompletedAt != "" {
		content += "\ncompleted_at: " + job.CompletedAt
	}
	if job.Error != "" {
		content += "\nerror: " + job.Error
	}
	content += "\noutput:\n" + output
	return Result{Content: content, Preview: preview(content)}, nil
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
