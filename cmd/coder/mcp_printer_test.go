package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/mcpserver"
	"github.com/tjcoder-labs/cli/internal/theme"
	"github.com/tjcoder-labs/cli/internal/tools"
)

func TestMCPPrinterUsesCurrentTUIThemeAndGlyphs(t *testing.T) {
	previous := theme.Current()
	th := theme.MustNew("#1c99ab", true)
	theme.SetCurrent(th)
	t.Cleanup(func() { theme.SetCurrent(previous) })
	for _, color := range []bool{false, true} {
		t.Run(fmt.Sprint(color), func(t *testing.T) {
			var out bytes.Buffer
			env := cliEnv{stderr: &out, color: color}
			p := &mcpToolPrinter{cwd: "/work/project", env: env, now: func() time.Time { return time.Date(2026, 11, 7, 1, 49, 0, 0, time.FixedZone("MST", -7*3600)) }}
			finish := p.started("read_file", json.RawMessage(`{"path":"README.md","start_line":1}`))
			finish(tools.Result{Content: "hello"}, nil)
			p.started("run_command", json.RawMessage(`{"command":"git status --short"}`))(tools.Result{}, errors.New("command failed: exit code 1"))
			got := out.String()
			for _, want := range []string{
				env.paint(style{th.Dim, false}, "1:49:00 AM MST"),
				env.paint(style{th.Deep, false}, "→ read_file"),
				env.paint(style{th.Bright, true}, "✓ read_file"),
				env.paint(style{th.Bright, true}, "✗ run_command"),
				`cwd="/work/project"`, "last call=1:49:00 AM MST run_command", `path="README.md"`, "start_line=1", "completed · 5 bytes · 0s", "command=git (arguments hidden)", "failed · command exit code 1",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in %q", want, got)
				}
			}
			if !color && strings.Contains(got, "\x1b") {
				t.Fatal("ANSI in plain output")
			}
			if strings.Contains(got, "--short") || strings.Contains(got, "hello") {
				t.Fatal("raw command/result leaked")
			}
		})
	}
}

func TestMCPPrinterWithholdsSecretsURLsAndTerminalControls(t *testing.T) {
	const capability = "https://api.tjcoder.com/mcp/hosts/abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	const password = "fake-private-password"
	var out bytes.Buffer
	p := &mcpToolPrinter{env: cliEnv{stderr: &out}}
	raw, _ := json.Marshal(map[string]any{
		"command":  "curl -H 'Authorization: Bearer " + password + "' " + capability,
		"password": password, "token": password, "headers": map[string]string{"Authorization": "Bearer " + password},
		"path": capability, "content": password, "environment": map[string]string{"PASSWORD": password},
	})
	p.started("run_command", raw)(tools.Result{Content: capability + password, Preview: password}, errors.New(capability+password))
	p.started("read_file", json.RawMessage(`{"path":"\u001b]52;private\u0007","start_line":2}`))(tools.Result{Content: password}, nil)
	p.started(capability, raw)(tools.Result{}, nil)
	got := out.String()
	for _, secret := range []string{password, capability, "Authorization: Bearer", "\x1b", "\x07", "abcdefghijklmnopqrstuvwxyz"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret/control leaked: %q", got)
		}
	}
	for _, want := range []string{"command=curl (arguments hidden)", "password=[redacted]", "path=[hidden]", "tool error (details withheld)", "[tool name hidden]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestMCPPrinterArgumentSummaries(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{}`, "no arguments"},
		{`not-json`, "arguments hidden"},
		{`{"command":"TOKEN=secret curl example.com"}`, "command=[hidden]"},
		{`{"path":"/tmp/secret-token-value"}`, "path=[hidden]"},
		{`{"path":"abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"}`, "path=[hidden]"},
		{`{"path":"normal.go","recursive":true,"limit":10}`, `limit=10 · path="normal.go" · recursive=true`},
		{`{"query":"arbitrary-secret","body":"not logged"}`, "body=[hidden] · query=[hidden]"},
	} {
		if got := mcpArgumentSummary(json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestMCPPrinterErrorSummaries(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.Canceled, "cancelled"}, {context.DeadlineExceeded, "deadline exceeded"},
		{os.ErrNotExist, "file not found"}, {os.ErrPermission, "permission denied"},
		{errors.New("sudo denied: unsafe details"), "sudo denied (standalone tunnel)"},
		{errors.New("command failed: signal: killed private"), "command failed"},
		{errors.New("private arbitrary error"), "tool error (details withheld)"},
	} {
		if got := mcpErrorSummary(tc.err); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestMCPPrinterObservesActualCallsWithoutAlteringProtocol(t *testing.T) {
	root := t.TempDir()
	secret := "password=fake-private-password"
	if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := mcpserver.NewLocalServer(root, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if server.Env.Prompter != nil || server.Env.Sudo != nil {
		t.Fatal("standalone sudo gate changed")
	}
	var activity bytes.Buffer
	p := &mcpToolPrinter{cwd: server.Env.WorkspaceRoot, env: cliEnv{stderr: &activity}}
	server.OnToolCall = p.started
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"fixture.txt"}}}`)
	reply := server.Handle(context.Background(), req)
	if !bytes.Contains(reply, []byte(secret)) {
		t.Fatalf("protocol result altered: %s", reply)
	}
	if !strings.Contains(activity.String(), "cwd="+mcpSafePath(server.Env.WorkspaceRoot)) {
		t.Fatal(activity.String())
	}
	if strings.Contains(activity.String(), secret) || !strings.Contains(activity.String(), "→ read_file") || !strings.Contains(activity.String(), "✓ read_file") {
		t.Fatal(activity.String())
	}
	// The stdio path has no observer and remains exclusively newline-delimited RPC.
	server.OnToolCall = nil
	activity.Reset()
	var protocol bytes.Buffer
	if err := server.ServeStdio(context.Background(), strings.NewReader(string(req)+"\n"), &protocol); err != nil {
		t.Fatal(err)
	}
	if activity.Len() != 0 || !json.Valid(bytes.TrimSpace(protocol.Bytes())) {
		t.Fatal("stdio stream polluted")
	}
}

func TestMCPPrinterHonorsColorEnvironment(t *testing.T) {
	for _, tc := range []struct{ key, value string }{{"NO_COLOR", "1"}, {"TERM", "dumb"}} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			var out bytes.Buffer
			p := &mcpToolPrinter{env: cliEnv{stderr: &out, color: colorEnabled(os.Stderr)}}
			p.started("read_file", json.RawMessage(`{}`))(tools.Result{}, nil)
			if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "✓ read_file") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestMCPPrinterLastInvocationSurvivesOlderCompletion(t *testing.T) {
	var out bytes.Buffer
	p := &mcpToolPrinter{cwd: "/work/project", env: cliEnv{stderr: &out}, now: func() time.Time { return time.Date(2026, 11, 7, 3, 14, 0, 0, time.FixedZone("MST", -7*3600)) }}
	first := p.started("read_file", json.RawMessage(`{}`))
	p.started("run_command", json.RawMessage(`{}`))
	first(tools.Result{}, nil)
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		for _, want := range []string{"3:14:00 AM MST", `cwd="/work/project"`, "last call="} {
			if !strings.Contains(line, want) {
				t.Fatalf("missing %q in %q", want, line)
			}
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(out.String()), "last call=3:14:00 AM MST run_command") {
		t.Fatal(out.String())
	}
}

func TestMCPPrinterHidesUnsafeSessionCWD(t *testing.T) {
	for _, cwd := range []string{"https://example.com/mcp/hosts/private", "/work/api_token", "/work/abcdefghijklmnopqrstuvwxyz0123456789", "/work/\x1b[2J"} {
		var out bytes.Buffer
		p := &mcpToolPrinter{cwd: cwd, env: cliEnv{stderr: &out}}
		p.started("read_file", json.RawMessage(`{}`))(tools.Result{}, nil)
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			if !strings.Contains(line, "cwd=[hidden]") || strings.Contains(line, cwd) {
				t.Fatal(line)
			}
		}
	}
}

func TestMCPPrinterNonTTYHasNoANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	var out bytes.Buffer
	f, err := os.CreateTemp(t.TempDir(), "non-tty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := &mcpToolPrinter{cwd: "/work/project", env: cliEnv{stderr: &out, color: colorEnabled(f)}}
	p.started("read_file", json.RawMessage(`{}`))(tools.Result{}, nil)
	if strings.Contains(out.String(), "\x1b") {
		t.Fatal(out.String())
	}
}
