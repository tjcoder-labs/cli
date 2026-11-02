package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestZZHistoryProbe(t *testing.T) {
	a, sim, _ := startGatedTurn(t)
	onLoop(t, a, func() {
		a.inputHistory = []string{"first msg", "/model", "second msg"}
		a.inputHistoryIdx = -1
	})
	// Up once
	injectKey(sim, tcell.KeyUp, 0)
	onLoop(t, a, func() { t.Logf("after Up1: text=%q idx=%d", a.input.GetText(), a.inputHistoryIdx) })
	injectKey(sim, tcell.KeyUp, 0)
	onLoop(t, a, func() { t.Logf("after Up2: text=%q idx=%d", a.input.GetText(), a.inputHistoryIdx) })
	injectKey(sim, tcell.KeyDown, 0)
	onLoop(t, a, func() { t.Logf("after Down1: text=%q idx=%d", a.input.GetText(), a.inputHistoryIdx) })
}
