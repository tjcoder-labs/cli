package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/reminders"
)

// validateFetchURL rejects URLs that resolve to loopback, private,
// link-local, or otherwise non-public addresses. Without this guard a
// prompt-injected model could use the fetch tool as an SSRF primitive
// to reach localhost services or the cloud metadata endpoint
// (169.254.169.254) and exfiltrate credentials.
func validateFetchURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q (only http/https allowed)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("refusing to fetch non-public address %s", ip)
		}
	}
	return nil
}

// fetchTool performs a simple HTTP fetch and returns body (truncated).
type fetchTool struct{}

func (fetchTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "fetch",
			Description: "Fetch a URL via HTTP GET. Returns the response body (or a truncated preview).",
			Parameters: objectSchema([]string{"url"}, map[string]any{
				"url":       stringProp("URL to fetch"),
				"max_bytes": numberProp("Maximum bytes to read from the response (optional)."),
			}),
		},
	}
}

func (fetchTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		URL      string `json:"url"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if args.URL == "" {
		return Result{}, fmt.Errorf("url is required")
	}
	if err := validateFetchURL(args.URL); err != nil {
		return Result{}, err
	}
	client := http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(args.URL)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	limit := int64(10000)
	if args.MaxBytes > 0 {
		limit = int64(args.MaxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return Result{}, err
	}
	out := string(data)
	preview := preview(out)
	return Result{Content: out, Preview: preview}, nil
}

// appendFileTool appends text to a file, creating parents if needed.
type appendFileTool struct{}

func (appendFileTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "append_file",
			Description: "Append content to a file (creates file if missing).",
			Parameters: objectSchema([]string{"path", "content"}, map[string]any{
				"path":    stringProp("File path relative to workspace."),
				"content": stringProp("Content to append."),
			}),
		},
	}
}

func (appendFileTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if args.Path == "" {
		return Result{}, fmt.Errorf("path required")
	}
	path, err := resolveInWorkspace(env.WorkspaceRoot, args.Path)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	if _, err := f.WriteString(args.Content); err != nil {
		return Result{}, err
	}
	msg := fmt.Sprintf("appended %s", path)
	return Result{Content: msg, Preview: msg}, nil
}

// deleteFileTool safely deletes a file.
type deleteFileTool struct{}

func (deleteFileTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "delete_file",
			Description: "Delete a file at the given path. Fails if missing unless force=true.",
			Parameters: objectSchema([]string{"path"}, map[string]any{
				"path":  stringProp("File path"),
				"force": boolProp("If true, do not error when file is missing."),
			}),
		},
	}
}

func (deleteFileTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path  string `json:"path"`
		Force bool   `json:"force"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if args.Path == "" {
		return Result{}, fmt.Errorf("path required")
	}
	path, err := resolveInWorkspace(env.WorkspaceRoot, args.Path)
	if err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if args.Force {
			return Result{Content: "missing (ignored)", Preview: "missing (ignored)"}, nil
		}
		return Result{}, fmt.Errorf("file not found")
	}
	if err := os.Remove(path); err != nil {
		return Result{}, err
	}
	msg := fmt.Sprintf("deleted %s", path)
	return Result{Content: msg, Preview: msg}, nil
}

// moveFileTool renames or moves a file.
type moveFileTool struct{}

func (moveFileTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "move_file",
			Description: "Move or rename a file from src to dst (relative to workspace).",
			Parameters: objectSchema([]string{"src", "dst"}, map[string]any{
				"src": stringProp("Source path."),
				"dst": stringProp("Destination path."),
			}),
		},
	}
}

func (moveFileTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if args.Src == "" || args.Dst == "" {
		return Result{}, fmt.Errorf("src and dst required")
	}
	src, err := resolveInWorkspace(env.WorkspaceRoot, args.Src)
	if err != nil {
		return Result{}, err
	}
	dst, err := resolveInWorkspace(env.WorkspaceRoot, args.Dst)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.Rename(src, dst); err != nil {
		return Result{}, err
	}
	msg := fmt.Sprintf("moved %s -> %s", src, dst)
	return Result{Content: msg, Preview: msg}, nil
}

// gitLogTool exposes a read-only git log summary.
type gitLogTool struct{}

func (gitLogTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "git_log",
			Description: "Return recent git log entries from the repository (read-only).",
			Parameters: objectSchema([]string{"limit"}, map[string]any{
				"limit": numberProp("Number of commits to return; default 20."),
			}),
		},
	}
}

func (gitLogTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal(raw, &args)
	lim := "20"
	if args.Limit > 0 {
		lim = fmt.Sprintf("%d", args.Limit)
	}
	cmd := exec.Command("git", "-C", env.WorkspaceRoot, "log", "-n", lim, "--pretty=format:%h %ad %s", "--date=short")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("git log failed: %v: %s", err, string(out))
	}
	s := string(out)
	return Result{Content: s, Preview: preview(s)}, nil
}

// validateCronExpr ensures cron_expr is a standard 5-field crontab
// schedule and contains no shell metacharacters or newlines. This
// prevents crontab injection: cron_expr is written verbatim into a
// crontab line, so an unvalidated value could smuggle an arbitrary
// command onto its own line and gain persistent code execution.
var cronFieldRe = regexp.MustCompile(`^[0-9*/,\-]+$`)

func validateCronExpr(expr string) error {
	if strings.ContainsAny(expr, "\n\r") {
		return fmt.Errorf("cron_expr must not contain newlines")
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("cron_expr must have exactly 5 fields, got %d", len(fields))
	}
	for _, f := range fields {
		if !cronFieldRe.MatchString(f) {
			return fmt.Errorf("cron_expr field %q contains invalid characters", f)
		}
	}
	return nil
}

// reminderTool stores a reminder and optionally installs a cron entry.
// When a prompt is supplied, the reminder becomes a *schedule*: each
// cron tick re-invokes the binary headlessly via `coder schedule run
// <id>` instead of echoing a message to a log file.
type reminderTool struct{}

func (reminderTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "set_reminder",
			Description: "Schedule a reminder or a self-reinvoking schedule. Provide a cron expression in 'cron_expr' (standard 5-field crontab). For a simple reminder, provide 'message'. For a schedule that re-runs the agent headlessly each tick, provide 'prompt' (and optionally 'agent'). 'platform' + 'daily_cap' optionally cap like actions per day. Set install_cron=true to append a crontab entry. When the TUI is open, a due schedule's prompt is injected into the live conversation as an attributed message and runs in-chat; when closed, it runs headlessly via cron.",
			Parameters: objectSchema([]string{"cron_expr"}, map[string]any{
				"cron_expr":    stringProp("Cron expression (5 fields) describing when to run."),
				"message":      stringProp("Reminder message text (for a passive reminder)."),
				"prompt":       stringProp("Prompt to run the agent with each tick (turns this into a schedule)."),
				"agent":        stringProp("Agent to use when re-invoking for a schedule (default: social-researcher)."),
				"platform":     stringProp("Social platform whose like actions the cap counts (e.g. linkedin)."),
				"daily_cap":    numberProp("Max like actions per day for this schedule before it skips (0 = unlimited)."),
				"install_cron": boolProp("If true, attempt to append a crontab entry for this reminder/schedule."),
			}),
		},
	}
}

func (reminderTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		CronExpr    string `json:"cron_expr"`
		Message     string `json:"message"`
		Prompt      string `json:"prompt"`
		Agent       string `json:"agent"`
		Platform    string `json:"platform"`
		DailyCap    int    `json:"daily_cap"`
		InstallCron bool   `json:"install_cron"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if args.CronExpr == "" {
		return Result{}, fmt.Errorf("cron_expr is required")
	}
	if args.Message == "" && args.Prompt == "" {
		return Result{}, fmt.Errorf("message or prompt is required")
	}
	if err := validateCronExpr(args.CronExpr); err != nil {
		return Result{}, err
	}
	if args.Prompt != "" && strings.ContainsAny(args.Prompt, "\n\r") {
		return Result{}, fmt.Errorf("prompt must not contain newlines when used in a schedule")
	}
	if args.Agent == "" {
		args.Agent = "social-researcher"
	}

	store := reminders.NewStore()
	_, entry, err := store.Create(env.WorkspaceRoot, reminders.CreateInput{
		CronExpr:    args.CronExpr,
		Message:     args.Message,
		InstallCron: args.InstallCron,
		Prompt:      args.Prompt,
		Agent:       args.Agent,
		Platform:    args.Platform,
		DailyCap:    args.DailyCap,
	})
	if err != nil {
		return Result{}, err
	}

	if args.InstallCron {
		if err := installCron(env, entry); err != nil {
			return Result{}, err
		}
	}

	path := filepath.Join(env.WorkspaceRoot, ".ergo-cli-go", "reminders.json")
	kind := "reminder"
	if entry.Prompt != "" {
		kind = "schedule"
	}
	msg := fmt.Sprintf("scheduled %s %s (cron=%s) and saved to %s", kind, entry.ID, entry.CronExpr, path)
	if entry.Prompt != "" {
		msg += fmt.Sprintf("; each tick runs: coder schedule run %s", entry.ID)
	}
	if entry.DailyCap > 0 {
		msg += fmt.Sprintf("; daily like cap: %d", entry.DailyCap)
	}
	return Result{Content: msg, Preview: msg}, nil
}

// installCron appends a crontab line for the given reminder/schedule.
// For a passive reminder it echoes the message into reminders.log (the
// legacy behavior). For a schedule it re-invokes the binary headlessly
// via `coder schedule run <id>`. The binary path is resolved from the
// running process so the cron line is correct regardless of how the
// program was launched.
func installCron(env ExecEnv, entry reminders.Entry) error {
	dir := filepath.Join(env.WorkspaceRoot, ".ergo-cli-go")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	var cronLine string
	if entry.Prompt != "" {
		bin, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}
		// Quote the workspace root for safe shell interpolation.
		ws := strings.ReplaceAll(env.WorkspaceRoot, "'", "'\\''")
		cronLine = fmt.Sprintf("%s cd '%s' && '%s' schedule run %s >> '%s/schedule.log' 2>&1",
			entry.CronExpr, ws, bin, entry.ID, dir)
	} else {
		if strings.ContainsAny(entry.Message, "\n\r") {
			return fmt.Errorf("reminder message must not contain newlines when installing cron")
		}
		cronLine = fmt.Sprintf("%s echo '%s' >> %s/reminders.log", entry.CronExpr, strings.ReplaceAll(entry.Message, "'", "'\\''"), dir)
	}

	cmd := exec.Command("crontab", "-l")
	existing, err := cmd.Output()
	if err != nil {
		existing = []byte{}
	}
	newCrontab := string(existing) + "\n" + cronLine + "\n"
	set := exec.Command("crontab", "-")
	set.Stdin = strings.NewReader(newCrontab)
	if err := set.Run(); err != nil {
		return fmt.Errorf("failed to install crontab: %v", err)
	}
	return nil
}
