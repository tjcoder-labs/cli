package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/tools"
)

func freeTextPrompt() *promptState {
	return newChoicePrompt(tools.PromptRequest{Question: "Name?", AllowOther: true})
}

// fakePromptClock pins the focus guard's clock and returns a setter.
func fakePromptClock(t *testing.T) func(time.Duration) {
	t.Helper()
	base := time.Unix(1_700_000_000, 0)
	now := base
	oldNow, oldDelay := promptNow, promptArmDelay
	promptNow = func() time.Time { return now }
	promptArmDelay = 300 * time.Millisecond
	t.Cleanup(func() { promptNow, promptArmDelay = oldNow, oldDelay })
	return func(d time.Duration) { now = base.Add(d) }
}

// Pasted secret text lands in the hidden buffer and is never rendered.
func TestPromptPaste_SecretGoesToHiddenBuffer(t *testing.T) {
	s := newSecretPrompt("Login", "API key")
	const key = "sk-live-Abc123XYZ"
	if !s.paste(key) {
		t.Fatal("paste reported no change")
	}
	if string(s.buf) != key {
		t.Fatalf("buf = %q", s.buf)
	}
	out := s.render(Palette{}, 100)
	if strings.Contains(out, key) || strings.Contains(out, "Abc123") {
		t.Fatalf("secret leaked into the card text:\n%s", out)
	}
	if !strings.Contains(out, strings.Repeat("•", len(key))) {
		t.Fatalf("expected %d bullets in:\n%s", len(key), out)
	}
	if done, _ := s.handleKey(key0(tcell.KeyEnter)); !done || string(s.buf) != key {
		t.Fatalf("Enter after paste: done=%v buf=%q", done, s.buf)
	}
}

func key0(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, 0, tcell.ModNone) }

// When appendSecret has to grow the buffer, the old backing array is
// zeroed rather than left for the GC with secret bytes in it.
func TestPromptPaste_AppendSecretWipesOldArrayOnGrowth(t *testing.T) {
	s := newSecretPrompt("t", "l")
	s.buf = make([]byte, 0, 4)
	s.appendSecret([]byte("abcd"))
	old := s.buf[:cap(s.buf)] // same backing array, full length
	if string(old) != "abcd" {
		t.Fatalf("setup: old = %q", old)
	}
	s.paste("efgh-more-than-fits")
	if &old[0] == &s.buf[0] {
		t.Fatal("buffer did not grow; test is not exercising growth")
	}
	for i, b := range old {
		if b != 0 {
			t.Fatalf("old backing array byte %d = %q, want 0 (all: %q)", i, b, old)
		}
	}
	if string(s.buf) != "abcdefgh-more-than-fits" {
		t.Fatalf("buf = %q", s.buf)
	}
}

// Free-text paste goes into the typed answer; embedded newlines become
// spaces and trailing ones are dropped (the answer is one line).
func TestPromptPaste_FreeTextGoesToTypedAnswer(t *testing.T) {
	s := freeTextPrompt()
	s.paste("hello world\n")
	if string(s.other) != "hello world" {
		t.Fatalf("other = %q", string(s.other))
	}
	s.paste(" a\nb\r\nc\r\n")
	if got := string(s.other); got != "hello world a b c" {
		t.Fatalf("other = %q", got)
	}
	done, ans := s.handleKey(key0(tcell.KeyEnter))
	if !done || ans.Other != "hello world a b c" {
		t.Fatalf("done=%v ans=%+v", done, ans)
	}
}

// A choice card that is not in its text line ignores paste.
func TestPromptPaste_ChoiceRowsIgnorePaste(t *testing.T) {
	s := newChoicePrompt(tools.PromptRequest{Options: []string{"Yes", "No"}, AllowOther: true})
	if s.paste("2") || len(s.other) != 0 {
		t.Fatalf("paste on choice rows changed state: %q", string(s.other))
	}
}

// A pasted key keeps no trailing newline (LF, CRLF or CR), and no
// embedded line breaks either; everything else is kept as-is.
func TestPromptPaste_SecretHasNoTrailingNewline(t *testing.T) {
	for _, in := range []string{"tok\n", "tok\r\n", "tok\r", "tok\n\n", "to\nk"} {
		s := newSecretPrompt("t", "l")
		s.paste(in)
		if string(s.buf) != "tok" {
			t.Errorf("paste(%q) buf = %q, want %q", in, s.buf, "tok")
		}
	}
	s := newSecretPrompt("t", "l")
	if s.paste("\r\n") || len(s.buf) != 0 {
		t.Fatalf("newline-only paste stored %q", s.buf)
	}
}

func newTestPromptView(st *promptState) (*promptView, *bool, *int) {
	v := newPromptView(tview.NewTextView(), st)
	finished := false
	redraws := 0
	v.finished = func() bool { return finished }
	v.redraw = func() { redraws++ }
	return v, &finished, &redraws
}

// A paste within the 0.3 s focus guard is ignored, the same as keys;
// after the guard it is accepted.
func TestPromptPaste_FocusGuardIgnoresEarlyPaste(t *testing.T) {
	setClock := fakePromptClock(t)
	st := newSecretPrompt("t", "l")
	v, _, redraws := newTestPromptView(st)
	v.Focus(nil) // arms the guard at t=0
	h := v.PasteHandler()
	setClock(100 * time.Millisecond)
	h("early", nil)
	setClock(299 * time.Millisecond)
	h("early", nil)
	if len(st.buf) != 0 || *redraws != 0 {
		t.Fatalf("paste inside the guard was accepted: %q", st.buf)
	}
	setClock(300 * time.Millisecond)
	h("late", nil)
	if string(st.buf) != "late" || *redraws != 1 {
		t.Fatalf("paste after the guard: buf=%q redraws=%d", st.buf, *redraws)
	}
	// Re-focusing (e.g. after a modal closes) re-arms the guard.
	v.Blur()
	v.Focus(nil)
	h("again", nil)
	if string(st.buf) != "late" {
		t.Fatalf("paste right after re-focus accepted: %q", st.buf)
	}
}

// Paste is dropped once the card is finished, or while it lacks focus.
func TestPromptPaste_IgnoredWhenFinishedOrUnfocused(t *testing.T) {
	setClock := fakePromptClock(t)
	st := freeTextPrompt()
	v, finished, _ := newTestPromptView(st)
	h := v.PasteHandler()
	h("unfocused", nil)
	if len(st.other) != 0 {
		t.Fatalf("unfocused paste accepted: %q", string(st.other))
	}
	v.Focus(nil)
	setClock(time.Second)
	h("ok", nil)
	if string(st.other) != "ok" {
		t.Fatalf("focused paste: %q", string(st.other))
	}
	*finished = true
	h(" after", nil)
	if string(st.other) != "ok" {
		t.Fatalf("paste after finish accepted: %q", string(st.other))
	}
}

// End to end through tview's bracketed-paste path: a secret card in a
// running (simulated) app takes the paste, never shows it, and returns
// it on Enter.
func TestPromptPaste_SimBracketedPasteIntoSecretCard(t *testing.T) {
	noArmDelay(t)
	a, sim := startPasteSimApp(t)
	const key = "ghp_Sim123 $x\\'\"tok"
	out := askSecretAsync(a)
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	simPaste(sim, key+"\n")
	shown := waitSecretLen(t, a, len(key))
	if strings.Contains(shown, "Sim123") {
		t.Fatalf("secret visible in card:\n%s", shown)
	}
	injectKey(sim, tcell.KeyEnter, 0)
	if b := <-out; string(b) != key {
		t.Fatalf("secret = %q, want %q", b, key)
	}
}

// startPasteSimApp is startSimApp with bracketed paste on, as App.Run
// does before it enters the UI loop (startSimApp calls runUI directly).
func startPasteSimApp(t *testing.T) (*App, tcell.SimulationScreen) {
	t.Helper()
	a, sim := startSimApp(t)
	a.tv.EnablePaste(true)
	return a, sim
}

func askSecretAsync(a *App) <-chan []byte {
	out := make(chan []byte, 1)
	go func() {
		b, _ := a.AskSecret(context.Background(), "Login", "Token")
		out <- b
	}()
	return out
}

// waitSecretLen waits until the live secret card holds n bytes and
// returns the card's visible (tag-stripped) text.
func waitSecretLen(t *testing.T, a *App, n int) string {
	t.Helper()
	var shown string
	var got int
	deadline := time.Now().Add(slashDeadlockTimeout)
	for time.Now().Before(deadline) {
		onLoop(t, a, func() {
			if a.livePrompt != nil {
				shown = a.livePrompt.view.GetText(true)
				got = len(a.livePrompt.view.st.buf)
			}
		})
		if got == n {
			return shown
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("secret len = %d, want %d; card:\n%s", got, n, shown)
	return ""
}

// simPaste delivers text the way tcell's terminal parser does for a
// bracketed paste (ESC[200~ … ESC[201~): paste-start, one key event per
// character (CR/LF as Enter, Tab as Tab), paste-end. The simulation
// queue is small and PostEvent never blocks, so each event is retried
// until it is queued.
func simPaste(sim tcell.SimulationScreen, text string) {
	simPost(sim, tcell.NewEventPaste(true))
	for _, r := range text {
		simPost(sim, simKeyFor(r))
	}
	simPost(sim, tcell.NewEventPaste(false))
	time.Sleep(10 * time.Millisecond)
}

func simKeyFor(r rune) *tcell.EventKey {
	switch r {
	case '\n', '\r':
		return tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
	case '\t':
		return tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
	}
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
}

func simPost(sim tcell.SimulationScreen, ev tcell.Event) {
	for sim.PostEvent(ev) != nil {
		time.Sleep(time.Millisecond)
	}
}
