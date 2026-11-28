package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/mcpserver"
	"github.com/tjcoder-labs/cli/internal/tools"
)

func mcpNoLogin(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ERGO_NO_KEYRING", "1")
	t.Setenv("ERGO_API_KEY", "")
}

func TestMCPTipsAdaptAndEscapeNames(t *testing.T) {
	mcpNoLogin(t)
	a := newTestApp(t)
	a.input = tview.NewInputField()
	// "/mcp" alone is the command palette; once a space is typed the /add-style
	// argument palette takes over. Free-form arguments keep the usage tip.
	for _, tc := range []struct{ input, mode, want string }{
		{"/mcp", "command", "/mcp"},
		{"/mcp add", "wsargs", "add <name> <url|command>"},
		{"/mcp remove", "wsargs", "remove <name>"},
		{"/mcp list", "wsargs", "configured servers"},
		{"/mcp status", "wsargs", "last errors"},
		{"/mcp tunnel", "wsargs", "tunnel stop"},
		{"/mcp tunnel stop", "wsargs", "none running"},
		{"/mcp add x ", "mcp", "<name> <url|command> [args...]"},
		{"/mcp tunnel stop now", "mcp", "no tunnel is running"},
	} {
		a.input.SetText(tc.input)
		a.updateInputHint(tc.input)
		if a.hintMode != tc.mode || !strings.Contains(a.refreshContextBar(), tview.Escape(tc.want)) {
			t.Errorf("%s: %s %s", tc.input, a.hintMode, a.refreshContextBar())
		}
	}
	a.config.MCPServers = map[string]MCPServerConfig{"[red]demo": {Enabled: true}}
	a.input.SetText("/mcp remove ")
	a.updateInputHint("/mcp remove ")
	if got := a.refreshContextBar(); !strings.Contains(got, "[red[]demo") {
		t.Errorf("unescaped name: %q", got)
	}
	if got := a.mcpTip("/mcp"); !strings.Contains(got, "/mcp remove [red]demo") {
		t.Error(got)
	}
	a.mcpTunnel = &interactiveMCPTunnel{}
	if got := a.mcpTip("/mcp tunnel"); !strings.Contains(got, "connecting") {
		t.Error(got)
	}
	a.mcpTunnel.ready = true
	if got := a.mcpTip("/mcp tunnel"); !strings.Contains(got, "revokes") || !strings.Contains(got, "user-gated") {
		t.Error(got)
	}
	a.mcpTunnel.stopping = true
	if got := a.mcpTip("/mcp tunnel"); !strings.Contains(got, "stopping") {
		t.Error(got)
	}
	a.mcpTunnel = nil
	t.Setenv("ERGO_API_KEY", "test-key")
	if got := a.mcpTip("/mcp tunnel"); !strings.Contains(got, "TUI approval") || strings.Contains(got, "Run /login first") {
		t.Error(got)
	}
}

func TestMCPTunnelUsageAndStop(t *testing.T) {
	mcpNoLogin(t)
	a := newTestApp(t)
	a.handleMCPCommand([]string{"mcp"})
	a.handleMCPCommand([]string{"mcp", "tunnel", "unexpected"})
	a.handleMCPCommand([]string{"mcp", "tunnel"})
	a.handleMCPCommand([]string{"mcp", "tunnel", "stop"})
	for _, want := range []string{"remove|tunnel", "/mcp tunnel [stop|save|show|hide|copy]", "Run /login first", "No MCP tunnel is running"} {
		if !strings.Contains(a.capturedActivity(), want) {
			t.Error(a.capturedActivity())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mcpTunnel = &interactiveMCPTunnel{cancel: cancel}
	a.handleMCPTunnel(nil)
	a.handleMCPTunnel([]string{"stop"})
	if ctx.Err() == nil || !a.mcpTunnel.stopping || !strings.Contains(a.capturedActivity(), "already running") {
		t.Error("start/stop state missing")
	}
	if slashModeFor([]string{"mcp", "tunnel", "stop"}) != slashImmediate {
		t.Fatal("stop must work during a tool call")
	}
}

func TestMCPTunnelLinkNotPersisted(t *testing.T) {
	a, _ := startSimApp(t)
	const secret = "https://api.tjcoder.com/mcp/hosts/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	onLoop(t, a, func() {
		a.mcpTunnel = &interactiveMCPTunnel{}
		a.setMCPTunnelLink(mcpserver.Publication{URL: secret, ExpiresAt: "2026-11-07T09:00:00Z"})
		if strings.Contains(a.activityLogText(), secret) {
			t.Error("secret persisted in activity")
		}
		a.clearMCPTunnelBar()
	})
}

// A fake sudo never elevates: it reports an uncached ticket, accepts stdin
// validation without echoing it, and emits only a constant command result.
func TestInteractiveMCPSudoUsesTUIGateAndKeepsSecretPrivate(t *testing.T) {
	noArmDelay(t)
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "-n" ] && [ "$2" = "-v" ]; then exit 1; fi
if [ "$1" = "-S" ]; then read password; exit 0; fi
if [ "$1" = "-k" ]; then exit 0; fi
printf 'command-output\n'
`
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	a, sim := startPasteSimApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := &interactiveMCPRegistry{app: a, ctx: ctx}
	server := &mcpserver.Server{Registry: registry, Version: "test"}
	result := make(chan []byte, 1)
	go func() {
		result <- server.Handle(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_command","arguments":{"command":"sudo true"}}}`))
	}()
	waitFor(t, a, "sudo authorization", func() bool { return cardFocused(a) })
	// Deny first: the consumer gets a denial, never an auto-approval.
	injectKey(sim, tcell.KeyEscape, 0)
	select {
	case b := <-result:
		if !strings.Contains(string(b), "denied") {
			t.Fatal(string(b))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("denial did not unblock MCP")
	}
	waitFor(t, a, "slot released", func() bool { return !a.mcpExecuting })
	go func() {
		result <- server.Handle(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_command","arguments":{"command":"sudo true"}}}`))
	}()
	waitFor(t, a, "sudo authorization", func() bool { return cardFocused(a) })
	injectKey(sim, tcell.KeyEnter, 0) // Allow once (first option).
	waitFor(t, a, "password card", func() bool { return a.livePrompt != nil && a.livePrompt.view.st.secret })
	simPaste(sim, "fake-private-password")
	injectKey(sim, tcell.KeyEnter, 0)
	select {
	case b := <-result:
		var response map[string]any
		if json.Unmarshal(b, &response) != nil || !strings.Contains(string(b), "command-output") {
			t.Fatalf("response=%s", b)
		}
		for _, private := range []string{"fake-private-password", "allowed once", "session approval", "by the user"} {
			if strings.Contains(string(b), private) {
				t.Fatalf("private sudo detail leaked: %s", b)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sudo command did not unblock MCP")
	}
	onLoop(t, a, func() {
		if strings.Contains(a.activityLogText(), "fake-private-password") || strings.Contains(a.transcript.GetText(true), "fake-private-password") {
			t.Error("password persisted")
		}
	})
}

func TestStripMCPSudoDecision(t *testing.T) {
	if got := stripMCPSudoDecision("[sudo: allowed once by the user]\noutput"); got != "output" {
		t.Fatal(got)
	}
	if got := stripMCPSudoDecision("[sudo on test: allowed for this session by the user]\noutput"); got != "output" {
		t.Fatal(got)
	}
	if got := stripMCPSudoDecision("ordinary output\nsecond line"); got != "ordinary output\nsecond line" {
		t.Fatal(got)
	}

}

func TestInteractiveMCPTunnelBusyAndCancellation(t *testing.T) {
	noArmDelay(t)
	a, _ := startSimApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := &interactiveMCPRegistry{app: a, ctx: ctx}
	onLoop(t, a, func() { a.busy = true })
	// Non-sudo run_command runs concurrently even while busy.
	if _, err := registry.Execute(ctx, "run_command", json.RawMessage(`{"command":"printf ok"}`), tools.ExecEnv{}); err != nil {
		t.Fatalf("non-sudo run_command blocked while busy: %v", err)
	}
	// Sudo-bearing run_command is interactive and must be refused while busy.
	if _, err := registry.Execute(ctx, "run_command", json.RawMessage(`{"command":"sudo printf no"}`), tools.ExecEnv{}); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("sudo busy err=%v", err)
	}
	onLoop(t, a, func() { a.busy = false })
	result := make(chan error, 1)
	go func() {
		_, err := registry.Execute(ctx, "ask_user", json.RawMessage(`{"question":"Confirm?","options":[{"label":"Yes"},{"label":"No"}]}`), tools.ExecEnv{})
		result <- err
	}()
	waitFor(t, a, "MCP approval card", func() bool { return cardFocused(a) })
	cancel()
	select {
	case <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled MCP prompt stayed blocked")
	}
	waitFor(t, a, "MCP execution slot released", func() bool { return !a.mcpExecuting && !a.busy })
}

func TestMCPTunnelLifecycleWithoutPublicSession(t *testing.T) {
	a, _ := startSimApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tunnel := &interactiveMCPTunnel{cancel: cancel, done: make(chan struct{})}
	revoked := make(chan struct{})
	onLoop(t, a, func() {
		a.mcpTunnel = tunnel
		a.runMCPTunnel(ctx, tunnel, func(ctx context.Context, announce func(mcpserver.Publication)) error {
			announce(mcpserver.Publication{URL: "https://api.tjcoder.com/mcp/hosts/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExpiresAt: "2026-11-07T09:00:00Z"})
			<-ctx.Done()
			close(revoked)
			return nil
		})
	})
	waitFor(t, a, "tunnel ready", func() bool { return a.mcpTunnel != nil && a.mcpTunnel.ready })
	onLoop(t, a, func() {
		a.handleMCPTunnel([]string{"stop"})
	})
	select {
	case <-revoked:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel relay")
	}
	waitFor(t, a, "tunnel stopped", func() bool { return a.mcpTunnel == nil })
}

func TestMCPTunnelShutdownWithoutUI(t *testing.T) {
	a := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	tunnel := &interactiveMCPTunnel{cancel: cancel, done: make(chan struct{})}
	a.mcpTunnel = tunnel
	a.runMCPTunnel(ctx, tunnel, func(ctx context.Context, _ func(mcpserver.Publication)) error { <-ctx.Done(); return nil })
	a.shutdownMCPTunnel()
	if ctx.Err() == nil {
		t.Fatal("TUI exit did not cancel tunnel")
	}
	select {
	case <-tunnel.done:
	default:
		t.Fatal("shutdown did not wait for revocation")
	}
}

func TestSavedTunnelReusable(t *testing.T) {
	now := time.Date(2026, 11, 28, 16, 0, 0, 0, time.UTC)
	if _, ok := savedTunnelReusable(nil, now); ok {
		t.Fatal("nil saved")
	}
	if _, ok := savedTunnelReusable(&SavedMCPTunnel{SessionID: "s"}, now); ok {
		t.Fatal("missing token")
	}
	if _, ok := savedTunnelReusable(&SavedMCPTunnel{SessionID: "s", RelayToken: "t", ExpiresAt: "not-a-date"}, now); ok {
		t.Fatal("bad expiry")
	}
	expired := &SavedMCPTunnel{SessionID: "s", RelayToken: "t", ExpiresAt: now.Add(-time.Minute).Format(time.RFC3339)}
	if _, ok := savedTunnelReusable(expired, now); ok {
		t.Fatal("expired")
	}
	saved := &SavedMCPTunnel{SessionID: "s", RelayToken: "t", URL: testBarURL, ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)}
	got, ok := savedTunnelReusable(saved, now)
	if !ok || got.ID != "s" || got.RelayToken != "t" || got.URL != testBarURL {
		t.Fatal("valid save not reusable")
	}
}

func TestMCPTunnelSavePersistsReattachAndSurvivesExit(t *testing.T) {
	t.Setenv("ERGO_API_KEY", "test-key")
	a, _ := startSimApp(t)
	pub := mcpserver.Publication{
		ID:         "sess-save-test",
		RelayToken: "tok-save-test-relay",
		URL:        testBarURL,
		ExpiresAt:  time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}
	var mu sync.Mutex
	var kinds []string
	var lastCause error
	hook := func(ctx context.Context, saved *mcpserver.Publication, announce func(mcpserver.Publication)) error {
		kind := "create"
		out := pub
		if saved != nil {
			kind = "reattach"
			out = *saved
		}
		mu.Lock()
		kinds = append(kinds, kind)
		mu.Unlock()
		announce(out)
		<-ctx.Done()
		mu.Lock()
		lastCause = context.Cause(ctx)
		mu.Unlock()
		return nil
	}

	onLoop(t, a, func() { a.handleMCPTunnel([]string{"save"}) })
	onLoop(t, a, func() {
		if !strings.Contains(a.activityLogText(), "No active MCP tunnel to save") {
			t.Fatal("save without tunnel")
		}
		if a.config.MCPTunnel != nil {
			t.Fatal("save without tunnel wrote credentials")
		}
	})

	onLoop(t, a, func() {
		a.mcpTunnelStart = hook
		a.handleMCPTunnel(nil)
	})
	waitFor(t, a, "tunnel ready", func() bool { return a.mcpTunnel != nil && a.mcpTunnel.ready })

	onLoop(t, a, func() { a.handleMCPTunnel([]string{"save"}) })
	var root string
	onLoop(t, a, func() {
		if !strings.Contains(a.activityLogText(), "MCP tunnel saved") {
			t.Fatal("save confirmation missing")
		}
		root = a.workspaceRoot
		loaded := loadConfigFromDisk(root)
		got, ok := savedTunnelReusable(loaded.MCPTunnel, time.Now())
		if !ok || got.ID != pub.ID || got.RelayToken != pub.RelayToken || got.URL != pub.URL {
			t.Fatal("config did not persist reusable credentials")
		}
		if strings.Contains(a.activityLogText(), pub.RelayToken) || strings.Contains(a.activityLogText(), testBarToken) {
			t.Fatal("secret leaked into activity")
		}
	})

	onLoop(t, a, func() {
		if a.mcpTunnel == nil {
			t.Fatal("tunnel missing")
		}
		a.mcpTunnel.cancel()
	})
	waitFor(t, a, "tunnel dropped", func() bool { return a.mcpTunnel == nil })
	onLoop(t, a, func() {
		loaded := loadConfigFromDisk(root)
		if _, ok := savedTunnelReusable(loaded.MCPTunnel, time.Now()); !ok {
			t.Fatal("disconnect cleared the save")
		}
		a.handleMCPTunnel(nil)
	})
	waitFor(t, a, "reattached", func() bool { return a.mcpTunnel != nil && a.mcpTunnel.ready })
	onLoop(t, a, func() {
		mu.Lock()
		got := append([]string(nil), kinds...)
		mu.Unlock()
		if len(got) != 2 || got[0] != "create" || got[1] != "reattach" {
			t.Fatalf("unexpected run kinds")
		}
		if !strings.Contains(a.activityLogText(), "Attempting to reattach") {
			t.Fatal("reattach notice missing")
		}
		if a.mcpTunnel.publication.ID != pub.ID || a.mcpTunnel.publication.RelayToken != pub.RelayToken {
			t.Fatal("reattach used different credentials")
		}
	})

	a.shutdownMCPTunnel()
	mu.Lock()
	cause := lastCause
	mu.Unlock()
	if !errors.Is(cause, mcpserver.ErrLeaveOpen) {
		t.Fatal("clean exit revoked a saved session")
	}
	loaded := loadConfigFromDisk(root)
	if _, ok := savedTunnelReusable(loaded.MCPTunnel, time.Now()); !ok {
		t.Fatal("clean exit cleared the save")
	}
	waitFor(t, a, "shutdown finished", func() bool { return a.mcpTunnel == nil })

	onLoop(t, a, func() { a.handleMCPTunnel(nil) })
	waitFor(t, a, "reattach after exit", func() bool { return a.mcpTunnel != nil && a.mcpTunnel.ready })
	onLoop(t, a, func() {
		mu.Lock()
		got := append([]string(nil), kinds...)
		mu.Unlock()
		if len(got) != 3 || got[2] != "reattach" {
			t.Fatal("did not reattach after exit")
		}
		a.handleMCPTunnel([]string{"stop"})
	})
	waitFor(t, a, "stopped", func() bool { return a.mcpTunnel == nil })
	if _, ok := savedTunnelReusable(loadConfigFromDisk(root).MCPTunnel, time.Now()); ok {
		t.Fatal("stop left credentials in place")
	}
}

func TestMCPTunnelShowTips(t *testing.T) {
	mcpNoLogin(t)
	a := newTestApp(t)
	if got := a.mcpTip("/mcp tunnel show"); !strings.Contains(got, "needs a running tunnel") {
		t.Fatal(got)
	}
	a.mcpTunnel = &interactiveMCPTunnel{ready: true}
	if got := a.mcpTip("/mcp tunnel show"); !strings.Contains(got, "reveal") || !strings.Contains(got, "F7") {
		t.Fatal(got)
	}
	if got := a.mcpTip("/mcp tunnel copy"); !strings.Contains(got, "F8") {
		t.Fatal(got)
	}
	if got := a.mcpTip("/mcp tunnel"); !strings.Contains(got, "without function keys") || !strings.Contains(got, "revokes") || !strings.Contains(got, "user-gated") {
		t.Fatal(got)
	}
}
