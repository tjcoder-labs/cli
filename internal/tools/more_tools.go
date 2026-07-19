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
type reminderTool struct{}

func (reminderTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "set_reminder",
			Description: "Schedule a reminder. Provide a cron expression in 'cron_expr' (standard crontab) and a message. Optionally install into the user's crontab by setting install_cron=true.",
			Parameters: objectSchema([]string{"cron_expr", "message"}, map[string]any{
				"cron_expr":    stringProp("Cron expression (5 fields) describing when to run."),
				"message":      stringProp("Reminder message text."),
				"install_cron": boolProp("If true, attempt to append a crontab entry for this reminder."),
			}),
		},
	}
}

func (reminderTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		CronExpr    string `json:"cron_expr"`
		Message     string `json:"message"`
		InstallCron bool   `json:"install_cron"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if args.CronExpr == "" || args.Message == "" {
		return Result{}, fmt.Errorf("cron_expr and message are required")
	}
	if err := validateCronExpr(args.CronExpr); err != nil {
		return Result{}, err
	}
	// Persist to workspace reminders file
	dir := filepath.Join(env.WorkspaceRoot, ".ergo-cli-go")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, err
	}
	path := filepath.Join(dir, "reminders.json")
	var list []map[string]any
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &list)
	}
	entry := map[string]any{
		"cron_expr":  args.CronExpr,
		"message":    args.Message,
		"created_at": time.Now().Format(time.RFC3339),
	}
	list = append(list, entry)
	out, _ := json.MarshalIndent(list, "", "  ")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return Result{}, err
	}

	if args.InstallCron {
		if strings.ContainsAny(args.Message, "\n\r") {
			return Result{}, fmt.Errorf("reminder message must not contain newlines when installing cron")
		}
		// Attempt to append a crontab line that writes the message to reminders.log
		cronLine := fmt.Sprintf("%s echo '%s' >> %s/reminders.log", args.CronExpr, strings.ReplaceAll(args.Message, "'", "'\\''"), dir)
		// read existing crontab
		cmd := exec.Command("crontab", "-l")
		existing, err := cmd.Output()
		if err != nil {
			existing = []byte{}
		}
		newCrontab := string(existing) + "\n" + cronLine + "\n"
		set := exec.Command("crontab", "-")
		set.Stdin = strings.NewReader(newCrontab)
		if err := set.Run(); err != nil {
			return Result{}, fmt.Errorf("failed to install crontab: %v", err)
		}
	}

	msg := fmt.Sprintf("scheduled reminder and saved to %s", path)
	return Result{Content: msg, Preview: msg}, nil
}
