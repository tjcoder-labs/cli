package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/tjcoder-labs/cli/internal/theme"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// mcpToolPrinter uses the exact activity roles/glyphs from the TUI and the
// existing CLI ANSI painter. It owns no file/history sink. Arbitrary tool
// contents, command arguments and raw errors are intentionally NOT printed:
// filesystem/shell results may contain credentials (including the MCP URL).
type mcpToolPrinter struct {
	env cliEnv
	mu  sync.Mutex
	now func() time.Time
}

func (p *mcpToolPrinter) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *mcpToolPrinter) line(mark, name, text string, start bool) {
	th := theme.Current()
	st := style{th.Bright, true}
	if start {
		st = style{th.Deep, false}
	}
	stamp := p.env.paint(style{th.Dim, false}, p.clock().Format("3:04 PM"))
	label := p.env.paint(st, mark+" "+mcpSafeName(name))
	body := p.env.paint(style{th.Text, false}, text)
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.env.stderr, "%s  %s %s\n", stamp, label, body)
}

func (p *mcpToolPrinter) started(name string, args json.RawMessage) func(tools.Result, error) {
	begin := p.clock()
	p.line("→", name, mcpArgumentSummary(args), true)
	return func(result tools.Result, err error) {
		elapsed := p.clock().Sub(begin).Round(time.Millisecond)
		if elapsed < 0 {
			elapsed = 0
		}
		if err != nil {
			p.line("✗", name, fmt.Sprintf("failed · %s · %s", mcpErrorSummary(err), elapsed), false)
			return
		}
		p.line("✓", name, fmt.Sprintf("completed · %d bytes · %s", len(result.Content), elapsed), false)
	}
}

var mcpLogIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
var mcpLongSecret = regexp.MustCompile(`[A-Za-z0-9_+=%-]{24,}`)
var mcpExitCode = regexp.MustCompile(`(?:^|\s)exit code ([0-9]{1,3})(?:$|\s)`)

func mcpSafeName(name string) string {
	if !mcpLogIdentifier.MatchString(name) || mcpLongSecret.MatchString(name) {
		return "[tool name hidden]"
	}
	return name
}

func mcpSensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, word := range []string{"password", "passwd", "secret", "token", "credential", "authorization", "cookie", "api_key", "apikey", "private_key", "bearer"} {
		if strings.Contains(key, word) {
			return true
		}
	}
	return false
}

// Only paths, a few numeric controls, and known executable names are shown.
// Unknown values are summarized by type/size, not dumped as JSON. This keeps
// editing bodies, search strings, headers, environment, URLs and passwords off
// the operator's terminal even when the tool echoes them in an error/result.
func mcpArgumentSummary(raw json.RawMessage) string {
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return "arguments hidden"
	}
	if len(args) == 0 {
		return "no arguments"
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	summaries := make([]string, 0, 8)
	for _, key := range keys {
		if len(summaries) == 8 {
			summaries = append(summaries, "more arguments hidden")
			break
		}
		label := mcpSafeName(key)
		value := args[key]
		summary := "[hidden]"
		switch {
		case mcpSensitiveKey(key):
			summary = "[redacted]"
		case key == "command":
			if cmd, ok := value.(string); ok {
				summary = mcpExecutableSummary(cmd)
			}
		case key == "path" || key == "directory" || key == "cwd" || key == "workspace" || key == "workspace_root":
			if text, ok := value.(string); ok {
				summary = mcpSafePath(text)
			}
		case key == "start_line" || key == "end_line" || key == "limit" || key == "timeout_seconds" || key == "offset":
			if n, ok := value.(float64); ok {
				summary = fmt.Sprintf("%g", n)
			}
		case key == "background" || key == "recursive":
			if b, ok := value.(bool); ok {
				summary = fmt.Sprintf("%t", b)
			}
		}
		summaries = append(summaries, label+"="+summary)
	}
	return strings.Join(summaries, " · ")
}

func mcpExecutableSummary(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "[hidden]"
	}
	exe := filepath.Base(fields[0])
	switch exe {
	case "git", "ls", "cat", "rg", "sed", "awk", "go", "npm", "pnpm", "yarn", "node", "python", "python3", "curl", "wget", "pwd", "echo", "printf", "find", "mkdir", "make", "sudo":
		return exe + " (arguments hidden)"
	default:
		return "[hidden]"
	}
}

func mcpSafePath(text string) string {
	if len(text) > 160 || mcpSensitiveKey(text) || mcpLongSecret.MatchString(text) || strings.ContainsAny(text, ":?@\\\"'") {
		return "[hidden]"
	}
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "[hidden]"
		}
	}
	return fmt.Sprintf("%q", text)
}

func mcpErrorSummary(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline exceeded"
	case errors.Is(err, os.ErrNotExist):
		return "file not found"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	}
	// Return fixed vocabulary, never a slice of caller/tool-supplied error text.
	text := err.Error()
	if strings.HasPrefix(text, "sudo denied") {
		return "sudo denied (standalone tunnel)"
	}
	if strings.HasPrefix(text, "command failed:") {
		if m := mcpExitCode.FindStringSubmatch(text); m != nil {
			return "command exit code " + m[1]
		}
		return "command failed"
	}
	return "tool error (details withheld)"
}
