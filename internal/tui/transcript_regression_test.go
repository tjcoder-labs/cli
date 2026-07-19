package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/rivo/tview"
)

// TestUserBubbleDoesNotLeakPurpleBackground is the regression test for
// the "all purple background, transparent lettering" symptom reported
// by the user. After a user message is rendered, the next assistant
// label line and the next assistant body line must NOT carry the
// bubble's purple background — tview's tag parser would otherwise
// inherit the previous region's `bg=purple` into the next region's
// "no-bg-specified" tag and produce purple-on-purple (effectively
// invisible) text.
//
// We assert this by checking that the raw color-tag stream written
// to the transcript contains an explicit reset (`[-:-:-]`) between
// the bubble and the assistant label, AND that the assistant label
// opens with an explicit background tag (e.g. `[purple:root:b]`).
func TestUserBubbleDoesNotLeakPurpleBackground(t *testing.T) {
	app := &App{
		transcript: tview.NewTextView(),
		palette:    darkPalette(),
	}
	app.appendUserMessage("hello world")
	app.appendAssistantTurnLabel()

	got := app.transcript.GetText(false)
	if !strings.Contains(got, "[-:-:-]") {
		t.Fatalf("expected explicit `[-:-:-]` reset between user bubble and assistant label, got %q", got)
	}
	// The assistant label should open with an explicit background
	// channel set to root, not the shorthand `[purple::b]` (which
	// would inherit the previous region's bg).
	if matched, _ := regexp.MatchString(`\[[^\[\]:]+::b\]Coder is thinking`, got); matched {
		t.Fatalf("assistant label is using shorthand `[purple::b]` (bg not reset); got %q", got)
	}
	if !strings.Contains(got, "Coder is thinking") {
		t.Fatalf("expected assistant label 'Coder is thinking', got %q", got)
	}
}

// TestUpdateAssistantTurnLabelPreservesColorTags is the regression test
// for the "colorization shifts after the assistant responds" symptom.
// updateAssistantTurnLabel used to call GetText(true) which stripped
// every color tag from the transcript before re-emitting it. The fix
// is to call GetText(false) so the user bubble's purple-background
// tags survive the round-trip.
func TestUpdateAssistantTurnLabelPreservesColorTags(t *testing.T) {
	app := &App{
		transcript: tview.NewTextView(),
		palette:    darkPalette(),
	}
	app.appendUserMessage("hello world")
	app.appendAssistantTurnLabel()

	before := app.transcript.GetText(false)
	if !strings.Contains(before, app.palette.HexPurple) {
		t.Fatalf("precondition: expected purple hex tag in transcript, got %q", before)
	}

	app.assistantState = "replied"
	app.assistantStamp = "5:04:04 PM"
	app.updateAssistantTurnLabel()

	after := app.transcript.GetText(false)
	if !strings.Contains(after, app.palette.HexPurple) {
		t.Fatalf("expected purple hex tag to survive updateAssistantTurnLabel, got %q", after)
	}
	if !strings.Contains(after, "Coder replied:") {
		t.Fatalf("expected label to switch from 'thinking' to 'replied', got %q", after)
	}
}

// TestUpdateAssistantTurnLabelNoDuplicateTimestamp is the regression
// test for the "assistant timestamp printed twice" symptom. Within a
// single turn, updateAssistantTurnLabel is invoked more than once —
// first when commentary starts streaming and again when the turn
// completes — each time with a fresh stamp. The timestamp is rendered
// on the line above the label, so the second call must replace the
// first timestamp rather than stack a new one above it.
func TestUpdateAssistantTurnLabelNoDuplicateTimestamp(t *testing.T) {
	app := &App{
		transcript: tview.NewTextView(),
		palette:    darkPalette(),
	}
	app.appendUserMessage("hello world")
	app.appendAssistantTurnLabel()

	// First transition (commentary begins).
	app.assistantState = "replied"
	app.assistantStamp = "5:04 PM"
	app.updateAssistantTurnLabel()

	// Second transition (turn completes) with a later stamp.
	app.assistantStamp = "5:05 PM"
	app.updateAssistantTurnLabel()

	after := app.transcript.GetText(false)
	stampRe := regexp.MustCompile(`\d{1,2}:\d{2}\s?(AM|PM)`)
	if n := len(stampRe.FindAllString(after, -1)); n != 1 {
		t.Fatalf("expected exactly one assistant timestamp, found %d in %q", n, after)
	}
	if strings.Contains(after, "5:04 PM") {
		t.Fatalf("stale timestamp '5:04 PM' should have been replaced; got %q", after)
	}
	if !strings.Contains(after, "5:05 PM") {
		t.Fatalf("expected latest timestamp '5:05 PM' in transcript, got %q", after)
	}
}
