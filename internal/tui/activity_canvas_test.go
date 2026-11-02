package tui

import (
	"strings"
	"testing"

	"github.com/rivo/tview"
)

// TestCanvasDoesNotReceiveActivityLines guards the shared right-hand
// TextView: while the canvas is showing, activity lines must go to the
// log only and never splice into the rendered file.
func TestCanvasDoesNotReceiveActivityLines(t *testing.T) {
	a := &App{palette: darkPalette()}
	a.activity = tview.NewTextView()
	a.activePanel = "canvas"
	a.activity.SetText("func main() {}")

	a.appendActivity("some tool ran")

	shown := a.activity.GetText(true)
	if strings.Contains(shown, "some tool ran") {
		t.Fatalf("activity bled into the canvas:\n%s", shown)
	}
	if !strings.Contains(a.activityLogText(), "some tool ran") {
		t.Fatalf("activity line was not recorded in the log: %q", a.activityLogText())
	}
}

// TestActivityLogSurvivesCanvasSave guards persistence: saving while the
// canvas is up must store the activity log, not the file on screen.
func TestActivityLogSurvivesCanvasSave(t *testing.T) {
	a := &App{palette: darkPalette()}
	a.activity = tview.NewTextView()
	a.activity.SetText("func main() {}")
	a.activePanel = "canvas"
	a.appendActivity("tool output")

	got := a.activityLogText()
	if strings.Contains(got, "func main()") {
		t.Fatalf("saved activity contains canvas file contents: %q", got)
	}
	if !strings.Contains(got, "tool output") {
		t.Fatalf("saved activity missing the log line: %q", got)
	}
}
