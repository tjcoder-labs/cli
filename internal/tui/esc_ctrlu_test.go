package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/providers"
)

// Esc must close every modal form (same as its Cancel button) and hand
// focus back to the chat input, so subsequent typing lands there.
func TestModalForms_EscClosesAndRefocusesInput(t *testing.T) {
	for _, cmd := range []string{"/trigger", "/reminder", "/task", "/memory", "/config", "/login"} {
		t.Run(cmd, func(t *testing.T) {
			a, sim := startSimApp(t)
			typeLine(sim, cmd)
			waitFor(t, a, cmd+" modal open", func() bool { return a.leftPane != nil && a.tv.GetFocus() != a.input })
			// Type into the form's first field, as a user would, then Esc.
			// (/config's first item is a dropdown, where typing opens its
			// option list; that case is TestConfigModal_EscClosesOpenDropDownFirst.)
			if cmd != "/config" {
				injectKey(sim, tcell.KeyRune, 'q')
			}
			injectKey(sim, tcell.KeyEscape, 0)
			waitFor(t, a, cmd+" modal closed", func() bool { return a.leftPane == nil && a.tv.GetFocus() == a.input })
			for _, r := range "hi" {
				injectKey(sim, tcell.KeyRune, r)
			}
			waitFor(t, a, "typing reaches chat input", func() bool { return a.input.GetText() == "hi" })
		})
	}
}

func TestModalForms_EscProviderForms(t *testing.T) {
	a, _ := startSimApp(t)
	esc := func() {
		a.tv.GetFocus().InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(p tview.Primitive) { a.tv.SetFocus(p) })
	}
	var apiClosed, editorBackToList bool
	onLoop(t, a, func() {
		a.openAPIKeyLogin(providers.Config{Name: "x"})
		esc()
		apiClosed = a.leftPane == nil && a.tv.GetFocus() == a.input

		// The provider editor's Cancel returns to the provider list;
		// Esc must do the same.
		a.openProviderEditor(providers.Config{})
		esc()
		_, isList := a.tv.GetFocus().(*tview.List)
		editorBackToList = a.leftPane != nil && isList
	})
	if !apiClosed {
		t.Error("Esc did not close the API key form")
	}
	if !editorBackToList {
		t.Error("Esc in the provider editor did not return to the provider list")
	}
}

// Esc on an open dropdown (e.g. /config's Agent picker) closes just the
// option list; a second Esc closes the form.
func TestConfigModal_EscClosesOpenDropDownFirst(t *testing.T) {
	a, sim := startSimApp(t)
	typeLine(sim, "/config")
	waitFor(t, a, "config open", func() bool { return a.leftPane != nil && a.tv.GetFocus() != a.input })
	injectKey(sim, tcell.KeyEnter, 0) // open the focused Agent dropdown
	waitFor(t, a, "dropdown open", func() bool { _, ok := a.tv.GetFocus().(*tview.List); return ok })
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "dropdown closed, config still open", func() bool {
		_, ok := a.tv.GetFocus().(*tview.DropDown)
		return ok && a.leftPane != nil
	})
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "config closed", func() bool { return a.leftPane == nil && a.tv.GetFocus() == a.input })
}

func TestChatInput_CtrlUKillsToLineStart(t *testing.T) {
	a, sim := startSimApp(t)
	for _, r := range "hello world" {
		injectKey(sim, tcell.KeyRune, r)
	}
	for i := 0; i < 5; i++ {
		injectKey(sim, tcell.KeyLeft, 0)
	}
	injectKey(sim, tcell.KeyCtrlU, 0)
	waitFor(t, a, "Ctrl-U keeps text after cursor", func() bool { return a.input.GetText() == "world" })

	injectKey(sim, tcell.KeyEnd, 0)
	injectKey(sim, tcell.KeyCtrlU, 0)
	waitFor(t, a, "Ctrl-U at end clears input", func() bool { return a.input.GetText() == "" })

	// Ctrl-U on an empty input is a no-op (and must not scroll/eat keys).
	injectKey(sim, tcell.KeyCtrlU, 0)
	injectKey(sim, tcell.KeyRune, 'z')
	waitFor(t, a, "typing after Ctrl-U", func() bool { return a.input.GetText() == "z" })
}

func waitFor(t *testing.T, a *App, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(slashDeadlockTimeout)
	for time.Now().Before(deadline) {
		var ok bool
		onLoop(t, a, func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
