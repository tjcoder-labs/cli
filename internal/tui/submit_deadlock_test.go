package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// Regression: submit() holds the non-reentrant a.mu around saveSession,
// which reaches contextWindowFor via sessionContextUsage. contextWindowFor
// used to take a.mu as well, so every plain chat message self-deadlocked
// the event loop. These tests drive a real event loop and time out
// (rather than hang) if that comes back.

func TestSubmitPlainMessageDoesNotDeadlock(t *testing.T) {
	a, sim := startSimApp(t)
	typeLine(sim, "hello there")
	waitForText(t, a, "hello there", func() string { return a.transcript.GetText(true) })
	// The turn fails fast (provider at 127.0.0.1:1); the loop must stay
	// responsive and render the error.
	waitForText(t, a, "error", func() string { return a.transcript.GetText(true) })
	for _, r := range "next" {
		injectKey(sim, tcell.KeyRune, r)
	}
	waitForText(t, a, "next", func() string { return a.input.GetText() })
}

// TestSubmitWhileBusyDoesNotDeadlock covers the steering branch, which
// calls refreshContextBar (→ contextWindowFor) while holding a.mu.
func TestSubmitWhileBusyDoesNotDeadlock(t *testing.T) {
	a, _ := startSimApp(t)
	onLoop(t, a, func() {
		a.mu.Lock()
		a.busy = true
		a.mu.Unlock()
		a.input.SetText("steer me")
		a.submit()
		a.mu.Lock()
		a.busy = false
		a.mu.Unlock()
	})
	waitForText(t, a, "steering", func() string { return a.activity.GetText(true) })
}

// TestSaveSessionUnderMuDoesNotDeadlock pins the lock contract directly:
// saveSession must be callable with a.mu held (submit, submitScheduled).
func TestSaveSessionUnderMuDoesNotDeadlock(t *testing.T) {
	a, _ := startSimApp(t)
	onLoop(t, a, func() {
		a.currentModel = "some-uncached-model"
		a.mu.Lock()
		_ = a.saveSession()
		a.mu.Unlock()
	})
}
