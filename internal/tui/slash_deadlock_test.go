package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// Regression tests for the slash-command deadlock: tview v0.42's
// QueueUpdate blocks until the event loop runs the closure, so any
// handler that posted through it from the event loop (submit →
// handleSlashCommand → handleTelemetryCommand → Record) froze the TUI.
// Each test drives a real event loop on a simulation screen and fails
// with a timeout instead of hanging.

const slashDeadlockTimeout = 5 * time.Second

// startSimApp builds an App, attaches a simulation screen, and runs
// the event loop (via runUI, as Run does) on a background goroutine.
func startSimApp(t *testing.T) (*App, tcell.SimulationScreen) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CODER_TELEMETRY", "")
	a := New(client.NewOllama("http://127.0.0.1:1"), tools.NewRegistry(nil), t.TempDir(), "m", "coder", "TJ", "0.0.0")
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(140, 40)
	a.tv.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- a.runUI() }()
	t.Cleanup(func() {
		a.tv.Stop()
		select {
		case <-done:
		case <-time.After(slashDeadlockTimeout):
			t.Error("event loop did not stop")
		}
	})
	// Wait for the loop to come up.
	onLoop(t, a, func() {})
	return a, sim
}

// onLoop runs fn on the event loop and fails (rather than hangs) if
// the loop does not get to it in time — i.e. the loop is wedged.
func onLoop(t *testing.T, a *App, fn func()) {
	t.Helper()
	ran := make(chan struct{})
	go a.tv.QueueUpdate(func() { fn(); close(ran) })
	select {
	case <-ran:
	case <-time.After(slashDeadlockTimeout):
		t.Fatal("UI event loop is deadlocked (queued update never ran)")
	}
}

// injectKey paces injection: the simulation screen's event queue is
// small and silently drops keys when full.
func injectKey(sim tcell.SimulationScreen, k tcell.Key, r rune) {
	sim.InjectKey(k, r, tcell.ModNone)
	time.Sleep(3 * time.Millisecond)
}

func typeLine(sim tcell.SimulationScreen, s string) {
	for _, r := range s {
		injectKey(sim, tcell.KeyRune, r)
	}
	injectKey(sim, tcell.KeyEnter, 0)
}

func waitForText(t *testing.T, a *App, what string, get func() string) {
	t.Helper()
	deadline := time.Now().Add(slashDeadlockTimeout)
	for {
		var txt string
		onLoop(t, a, func() { txt = get() })
		if strings.Contains(txt, what) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q; got:\n%s", what, txt)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSlashTelemetryTypedDoesNotDeadlock types the commands exactly as
// a user would (InputField done handler → submit → handleSlashCommand).
func TestSlashTelemetryTypedDoesNotDeadlock(t *testing.T) {
	a, sim := startSimApp(t)
	transcript := func() string { return a.transcript.GetText(true) }
	activity := func() string { return a.activity.GetText(true) }

	typeLine(sim, "/telemetry")
	waitForText(t, a, "elemetry", transcript) // status line
	typeLine(sim, "/telemetry off")
	waitForText(t, a, "Telemetry off.", transcript)
	typeLine(sim, "/telemetry status")
	onLoop(t, a, func() {})
	typeLine(sim, "/telemetry on")
	waitForText(t, a, "Telemetry on (takes effect next launch).", transcript)
	typeLine(sim, "/balance")
	waitForText(t, a, "Not signed in to ergo", activity)

	// The input still works afterwards.
	for _, r := range "still typing" {
		injectKey(sim, tcell.KeyRune, r)
	}
	waitForText(t, a, "still typing", func() string { return a.input.GetText() })
}

// TestSlashCommandInsideQueuedUpdate covers the old InvokeCLICommand
// shape (a handler running inside a queued update) and the agent's
// invoke_cli_command path from a background goroutine.
func TestSlashCommandInsideQueuedUpdate(t *testing.T) {
	a, _ := startSimApp(t)
	transcript := func() string { return a.transcript.GetText(true) }

	onLoop(t, a, func() { a.handleSlashCommand("/telemetry off") })
	waitForText(t, a, "Telemetry off.", transcript)

	errc := make(chan error, 1)
	go func() { errc <- a.InvokeCLICommand("telemetry on") }()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(slashDeadlockTimeout):
		t.Fatal("InvokeCLICommand deadlocked")
	}
	waitForText(t, a, "Telemetry on (takes effect next launch).", transcript)
	onLoop(t, a, func() { a.handleSlashCommand("/balance") })
}

// TestPostUIOrderedFromGoroutines checks that off-loop posts keep
// submission order and never block the poster.
func TestPostUIOrderedFromGoroutines(t *testing.T) {
	a, _ := startSimApp(t)
	var got []int
	for i := 0; i < 200; i++ {
		i := i
		a.postUI(func() { got = append(got, i) })
	}
	a.postUIWait(func() {})
	onLoop(t, a, func() {
		if len(got) != 200 {
			t.Fatalf("ran %d posts, want 200", len(got))
		}
		for i, v := range got {
			if v != i {
				t.Fatalf("post %d ran out of order (got %d)", i, v)
			}
		}
	})
	// From the loop itself, posts run inline (no deadlock, no reorder).
	onLoop(t, a, func() {
		ran := false
		a.postUIWait(func() { ran = true })
		if !ran {
			t.Fatal("postUIWait on the event loop should run inline")
		}
	})
}
