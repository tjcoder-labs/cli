package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/client"
)

// TestSlashModeForEveryCommand pins the mid-turn policy for every
// command in the palette (and the aliases handleSlashCommand accepts).
// Adding a command without deciding its policy fails here.
func TestSlashModeForEveryCommand(t *testing.T) {
	want := map[string]slashMode{
		"agent": slashImmediate, "agentinfo": slashImmediate, "tools": slashImmediate,
		"model": slashImmediate, "scroll": slashImmediate, "quit": slashImmediate,
		"trigger": slashImmediate, "event": slashImmediate, "reminder": slashImmediate,
		"reminders": slashImmediate, "task": slashImmediate, "tasks": slashImmediate,
		"memory": slashImmediate, "memories": slashImmediate, "config": slashImmediate,
		"environment": slashImmediate, "env": slashImmediate, "balance": slashImmediate,
		"activity": slashImmediate, "canvas": slashImmediate, "code": slashImmediate,
		"reference": slashImmediate, "about": slashImmediate, "telemetry": slashImmediate,
		"test": slashImmediate, "articles": slashImmediate, "dirs": slashImmediate,
		"add-dir": slashImmediate, "nosuchcommand": slashImmediate,

		"clear": slashBlocked,

		"compact": slashQueued, "login": slashQueued, "logout": slashQueued,
		"update": slashQueued, "providers": slashQueued, "mcp": slashImmediate,
	}
	for _, sc := range slashCommands {
		if _, ok := want[sc.cmd]; !ok {
			t.Errorf("/%s has no mid-turn policy in this test; decide immediate/queued/blocked", sc.cmd)
		}
	}
	for cmd, m := range want {
		if got := slashModeFor([]string{cmd}); got != m {
			t.Errorf("/%s: mode %v, want %v", cmd, got, m)
		}
	}
	sub := []struct {
		line string
		want slashMode
	}{
		{"providers list", slashImmediate},
		{"providers use other", slashQueued},
		{"mcp list", slashImmediate},
		{"mcp add x http://h", slashQueued},
		{"mcp remove x", slashQueued},
		{"MODEL", slashImmediate},
		{"Clear", slashBlocked},
	}
	for _, c := range sub {
		if got := slashModeFor(strings.Fields(c.line)); got != c.want {
			t.Errorf("/%s: mode %v, want %v", c.line, got, c.want)
		}
	}
}

func TestIsSlashCommandLine(t *testing.T) {
	for in, want := range map[string]bool{
		"/model": true, "/canvas a.go 1-3": true, "hello": false,
		"/not\na command": false, " /x": false,
	} {
		if got := isSlashCommandLine(in); got != want {
			t.Errorf("isSlashCommandLine(%q) = %v, want %v", in, got, want)
		}
	}
}

// gatedProvider holds each user turn's Chat until released, recording
// the model every request was sent with. The startup cognition recap
// returns at once so it does not consume the gate.
type gatedProvider struct {
	started chan string
	release chan struct{}
}

func newGatedProvider() *gatedProvider {
	return &gatedProvider{started: make(chan string, 8), release: make(chan struct{}, 8)}
}

func (p *gatedProvider) Chat(ctx context.Context, req client.ChatRequest, _ func(client.StreamEvent) error) (client.Message, error) {
	last := ""
	if n := len(req.Messages); n > 0 {
		last = req.Messages[n-1].Content
	}
	if strings.Contains(last, "cognition pane") {
		return client.Message{Role: "assistant", Content: "recap"}, nil
	}
	p.started <- req.Model
	select {
	case <-p.release:
	case <-ctx.Done():
		return client.Message{}, ctx.Err()
	}
	return client.Message{Role: "assistant", Content: "done"}, nil
}

func (p *gatedProvider) ListModels(context.Context) ([]client.ModelInfo, error) {
	return []client.ModelInfo{{Name: "m"}, {Name: "next-model"}}, nil
}
func (p *gatedProvider) ContextWindow(context.Context, string) (int, error) { return 8192, nil }
func (p *gatedProvider) BaseURL() string                                    { return "http://gated.test" }

func (p *gatedProvider) waitStarted(t *testing.T) string {
	t.Helper()
	select {
	case m := <-p.started:
		return m
	case <-time.After(slashDeadlockTimeout):
		t.Fatal("turn never reached the provider")
	}
	return ""
}

func startGatedTurn(t *testing.T) (*App, tcell.SimulationScreen, *gatedProvider) {
	t.Helper()
	p := newGatedProvider()
	a, sim := startSimAppWith(t, p, func(s tcell.SimulationScreen) tcell.Screen { return s })
	t.Cleanup(func() { close(p.release) })
	typeLine(sim, "first turn")
	if m := p.waitStarted(t); m != "m" {
		t.Fatalf("first request model %q, want m", m)
	}
	onLoop(t, a, func() {
		if !a.busy {
			t.Fatal("turn not in flight")
		}
	})
	return a, sim, p
}

func submitLine(t *testing.T, a *App, line string) {
	t.Helper()
	onLoop(t, a, func() {
		a.input.SetText(line)
		a.submit()
	})
}

func assertNothingSteered(t *testing.T, a *App) {
	t.Helper()
	onLoop(t, a, func() {
		if n := len(a.steeringCh); n != 0 {
			t.Errorf("%d message(s) steered into the turn; slash commands must not be", n)
		}
		if strings.Contains(a.activity.GetText(true), "steering") {
			t.Errorf("activity shows a steering notice:\n%s", a.activity.GetText(true))
		}
	})
}

// /model mid-turn opens the picker at once; the choice applies to the
// next request while the in-flight one keeps the model it started with.
func TestSlashMidTurn_ModelImmediateAppliesNextRequest(t *testing.T) {
	a, sim, p := startGatedTurn(t)
	submitLine(t, a, "/model")
	assertNothingSteered(t, a)
	waitUntil(t, a, "model picker loaded", func() bool { return a.leftPane != nil && len(a.models) == 2 })
	onLoop(t, a, func() {
		if !a.busy {
			t.Error("/model ended the in-flight turn")
		}
	})
	injectKey(sim, tcell.KeyDown, 0)
	injectKey(sim, tcell.KeyEnter, 0)
	waitUntil(t, a, "model switched", func() bool { return a.currentModel == "next-model" && a.leftPane == nil })

	p.release <- struct{}{} // finish the first turn
	waitUntil(t, a, "first turn settled", func() bool { return !a.busy })
	typeLine(sim, "second turn")
	if m := p.waitStarted(t); m != "next-model" {
		t.Fatalf("next request model %q, want next-model", m)
	}
}

// /canvas <file> mid-turn opens the canvas without ending the turn.
func TestSlashMidTurn_CanvasImmediate(t *testing.T) {
	a, _, p := startGatedTurn(t)
	var root string
	onLoop(t, a, func() { root = a.workspaceRoot })
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("canvas body line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	submitLine(t, a, "/canvas note.txt")
	assertNothingSteered(t, a)
	onLoop(t, a, func() {
		if a.activePanel != "canvas" {
			t.Errorf("active panel %q, want canvas", a.activePanel)
		}
		if !a.busy {
			t.Error("/canvas ended the in-flight turn")
		}
	})
	waitForText(t, a, "canvas body line", func() string { return a.activity.GetText(true) })
	p.release <- struct{}{}
	waitUntil(t, a, "turn settled", func() bool { return !a.busy })
}

// /clear is refused mid-turn; /compact waits for the turn to finish.
func TestSlashMidTurn_ClearBlockedCompactQueued(t *testing.T) {
	a, _, p := startGatedTurn(t)
	var before int
	onLoop(t, a, func() { before = len(a.history) })
	submitLine(t, a, "/clear")
	submitLine(t, a, "/compact")
	assertNothingSteered(t, a)
	onLoop(t, a, func() {
		act := a.activity.GetText(true)
		if !strings.Contains(act, "/clear is unavailable while a turn is running") {
			t.Errorf("no blocked notice for /clear:\n%s", act)
		}
		if !strings.Contains(act, "/compact queued") {
			t.Errorf("no queued notice for /compact:\n%s", act)
		}
		if len(a.history) != before {
			t.Errorf("history changed mid-turn: %d → %d", before, len(a.history))
		}
		if len(a.idleQueue) != 1 {
			t.Errorf("idle queue has %d items, want 1", len(a.idleQueue))
		}
	})
	p.release <- struct{}{}
	// The short history makes the queued /compact a logged no-op.
	waitForText(t, a, "nothing to compact", func() string { return a.activity.GetText(true) })
	onLoop(t, a, func() {
		if len(a.idleQueue) != 0 {
			t.Errorf("idle queue not drained: %d", len(a.idleQueue))
		}
	})
}

// Plain text mid-turn still steers.
func TestSlashMidTurn_PlainTextStillSteers(t *testing.T) {
	a, _, p := startGatedTurn(t)
	submitLine(t, a, "please also check tests")
	waitForText(t, a, "steering", func() string { return a.activity.GetText(true) })
	p.release <- struct{}{}
}
