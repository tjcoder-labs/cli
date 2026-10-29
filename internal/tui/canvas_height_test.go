package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitUntil polls cond on the event loop until it holds or times out.
func waitUntil(t *testing.T, a *App, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(slashDeadlockTimeout)
	for time.Now().Before(deadline) {
		ok := false
		onLoop(t, a, func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestCanvasFillsRightColumnAndRestores checks that the canvas uses the
// whole right-column height (cognition hidden), re-wraps on resize, and
// that switching back to activity restores the cognition/activity split.
func TestCanvasFillsRightColumnAndRestores(t *testing.T) {
	a, sim := startSimApp(t)
	long := strings.Repeat("word ", 80)
	src := "package main\n\n// " + long + "\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(a.workspaceRoot, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{140, 40}, {200, 50}} {
		sim.SetSize(size[0], size[1])
		onLoop(t, a, func() { a.openCanvas("main.go", 3, 3) })
		a.tv.Draw()
		waitUntil(t, a, "canvas laid out", func() bool {
			go a.tv.Draw()
			_, _, w, _ := a.activity.GetInnerRect()
			return w > 0 && a.canvasWidth == w
		})
		onLoop(t, a, func() {
			if n := a.right.GetItemCount(); n != 1 {
				t.Errorf("%dx%d: right column has %d items with canvas open, want 1", size[0], size[1], n)
			}
			_, ry, _, rh := a.right.GetRect()
			_, py, _, ph := a.activityPanel.GetRect()
			if py != ry || ph != rh {
				t.Errorf("%dx%d: canvas rect y=%d h=%d, right column y=%d h=%d", size[0], size[1], py, ph, ry, rh)
			}
			text := a.activity.GetText(true)
			if !strings.Contains(text, "┆") {
				t.Errorf("%dx%d: long line was not wrapped in the canvas", size[0], size[1])
			}
			for _, l := range strings.Split(text, "\n") {
				if w := len([]rune(l)); w > a.canvasWidth {
					t.Errorf("%dx%d: canvas line %d cells > width %d: %q", size[0], size[1], w, a.canvasWidth, l)
				}
			}
		})
	}
	// Resize while the canvas is open: the after-draw hook re-wraps.
	onLoop(t, a, func() {})
	before := 0
	onLoop(t, a, func() { before = a.canvasWidth })
	sim.SetSize(160, 45)
	a.tv.Draw()
	waitUntil(t, a, "canvas re-wrapped after resize", func() bool {
		_, _, w, _ := a.activity.GetInnerRect()
		return w > 0 && w != before && a.canvasWidth == w
	})
	onLoop(t, a, func() { a.showPanel("activity") })
	a.tv.Draw()
	onLoop(t, a, func() {
		if n := a.right.GetItemCount(); n != 2 {
			t.Errorf("after closing canvas right column has %d items, want 2 (cognition + activity)", n)
		}
		_, _, _, ch := a.reasoningPanel.GetRect()
		if ch <= 0 {
			t.Errorf("cognition pane not restored (height %d)", ch)
		}
	})
}
