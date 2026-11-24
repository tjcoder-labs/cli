//go:build !windows

package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/tooling"
)

func TestParseWatchArgs(t *testing.T) {
	cases := []struct {
		in       string
		sub, cmd string
		interval int
		noPTY    bool
		err      bool
	}{
		{in: "", sub: ""},
		{in: "stop", sub: "stop"},
		{in: "RERUN", sub: "restart"},
		{in: "go test ./...", sub: "start", cmd: "go test ./..."},
		{in: "-n 2 git status --short", sub: "start", cmd: "git status --short", interval: 2},
		{in: "-n5  ls  -la 'a  b'", sub: "start", cmd: "ls  -la 'a  b'", interval: 5},
		{in: "--interval=3 --no-pty date", sub: "start", cmd: "date", interval: 3, noPTY: true},
		{in: "-- -n is a command", sub: "start", cmd: "-n is a command"},
		{in: "stop the world", sub: "start", cmd: "stop the world"},
		{in: "-n x date", err: true},
		{in: "-n 99999 date", err: true},
		{in: "-n 2", err: true},
	}
	for _, c := range cases {
		got, err := parseWatchArgs(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%q: expected error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil || got.sub != c.sub || got.command != c.cmd || got.interval != c.interval || got.noPTY != c.noPTY {
			t.Errorf("%q: got %+v, %v", c.in, got, err)
		}
	}
	if r := slashRest("/Watch  -n 1 echo hi", "Watch"); r != "-n 1 echo hi" {
		t.Errorf("slashRest = %q", r)
	}
}

func watchBody(a *App) string  { return a.activity.GetText(false) }
func watchTitle(a *App) string { return a.activityTitle.GetText(true) }
func watchShown(a *App) bool   { return a.activePanel == "canvas" && a.canvasMode == canvasModeWatch }

// /watch streams colourised output into the canvas (watch mode), reports
// the exit, toggles, shares the canvas with files, and stop kills a long
// runner.
func TestWatchPaneLifecycle(t *testing.T) {
	a, sim := startSimApp(t)
	t.Cleanup(a.shutdownWatch)

	typeLine(sim, `/watch printf '\033[31mred\033[0m [x]\n'`)
	waitFor(t, a, "watch canvas open", func() bool { return watchShown(a) })
	waitForText(t, a, "exit 0", func() string { return watchTitle(a) + watchBody(a) })
	onLoop(t, a, func() {
		body := watchBody(a)
		if !strings.Contains(body, "[maroon") || !strings.Contains(body, "red[-:-:-] [x[]") {
			t.Errorf("body not colour-translated/escaped: %q", body)
		}
		if !strings.Contains(a.activity.GetText(true), "red [x]") {
			t.Errorf("rendered text = %q", a.activity.GetText(true))
		}
		if a.right.GetItemCount() != 1 || a.right.GetItem(0) != a.activityPanel {
			t.Error("watch should render in the full-height canvas surface")
		}
		if title := watchTitle(a); !strings.Contains(title, "CANVAS") || !strings.Contains(title, "watch") {
			t.Errorf("title = %q", title)
		}
		if strings.Contains(a.activityLog.String(), "red [x]") {
			t.Error("watch output leaked into the activity log")
		}
	})

	// /watch with no args toggles the canvas; the watch survives.
	typeLine(sim, "/watch")
	waitFor(t, a, "canvas closed", func() bool { return a.activePanel == "activity" })
	typeLine(sim, "/watch")
	waitFor(t, a, "canvas reopened", func() bool { return watchShown(a) })
	waitForText(t, a, "red [x]", func() string { return a.activity.GetText(true) })

	// A file in the canvas switches the mode; Alt+C-style showPanel keeps
	// it; /watch brings the watch back into the same canvas.
	path := a.workspaceRoot + "/notes.txt"
	if err := os.WriteFile(path, []byte("hello canvas\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	typeLine(sim, "/canvas notes.txt")
	waitForText(t, a, "hello canvas", func() string { return a.activity.GetText(true) })
	onLoop(t, a, func() {
		if a.canvasMode != canvasModeFile || a.activePanel != "canvas" {
			t.Errorf("mode=%q panel=%q", a.canvasMode, a.activePanel)
		}
	})
	typeLine(sim, "/watch")
	waitForText(t, a, "red [x]", func() string { return a.activity.GetText(true) })

	// A long runner: stop ends it and keeps the output.
	typeLine(sim, "/watch echo begin; sleep 300")
	waitForText(t, a, "running", func() string { return watchTitle(a) })
	waitForText(t, a, "begin", func() string { return watchBody(a) })
	sess := a.watch.sess
	typeLine(sim, "/watch stop")
	select {
	case <-sess.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not end the watch")
	}
	waitForText(t, a, "stopped", func() string { return watchTitle(a) })
	waitForText(t, a, "stopped after", func() string { return a.activity.GetText(true) })

	// Ctrl+E focuses the body; s/r keys; Esc returns focus.
	injectKey(sim, tcell.KeyCtrlE, 0)
	waitFor(t, a, "canvas focused", func() bool { return a.tv.GetFocus() == a.activity })
	injectKey(sim, tcell.KeyRune, 'f')
	waitForText(t, a, "paused", func() string { return watchTitle(a) })
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "focus back", func() bool { return a.tv.GetFocus() == a.input })

	// close stops and returns to activity.
	typeLine(sim, "/watch close")
	waitFor(t, a, "closed", func() bool { return a.activePanel == "activity" })
}

// Watches pass run_command's gate: sudo and banned patterns never start.
func TestWatchGating(t *testing.T) {
	a, sim := startSimApp(t)
	t.Cleanup(a.shutdownWatch)
	typeLine(sim, "/watch sudo journalctl -f")
	waitForText(t, a, "sudo is not supported in a watch", a.capturedActivity)
	typeLine(sim, "/watch echo ok; rm -rf /")
	waitForText(t, a, "disallowed pattern", a.capturedActivity)
	onLoop(t, a, func() {
		if a.watch.sess != nil {
			t.Fatal("a gated command started a watch")
		}
	})
}

// The agent drives the pane through ui_control markers and gets the
// agent time limit; agent-run slash commands do too.
func TestWatchFromUIControlMarker(t *testing.T) {
	a, _ := startSimApp(t)
	t.Cleanup(a.shutdownWatch)
	a.handleEvent(tooling.Event{Type: tooling.EventToolResult, ToolName: "ui_control", Text: "panel:canvas:watch:show:0:echo from:agent"})
	waitFor(t, a, "agent watch open", func() bool { return watchShown(a) && a.watch.sess != nil })
	waitForText(t, a, "from:agent", func() string { return a.activity.GetText(true) })
	onLoop(t, a, func() {
		if got := a.watch.sess.Options().MaxDuration; got != watchAgentMaxDuration {
			t.Errorf("agent watch MaxDuration = %v", got)
		}
	})
	a.handleEvent(tooling.Event{Type: tooling.EventToolResult, ToolName: "ui_control", Text: "panel:canvas:watch:hide"})
	waitFor(t, a, "hidden", func() bool { return a.activePanel == "activity" })

	if err := a.InvokeCLICommand("watch -n 1 echo tick"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a, "slash watch", func() bool { return a.watch.sess != nil && a.watch.sess.Options().Interval == time.Second })
	onLoop(t, a, func() {
		if !a.watch.fromAgent || a.watch.sess.Options().MaxDuration != watchAgentMaxDuration {
			t.Error("invoke_cli_command watch should carry the agent limit")
		}
		if a.slashFromAgent {
			t.Error("slashFromAgent left set")
		}
	})
}

func TestWatchArgHints(t *testing.T) {
	a := &App{}
	if _, items, ok := a.watchArgHints("/watch st"); !ok || len(items) != 2 {
		t.Fatalf("st -> %v %v", items, ok)
	}
	if _, _, ok := a.watchArgHints("/watch go test"); ok {
		t.Fatal("free-form command should drop the palette")
	}
}
