package tui

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// Regression tests for "picking an option on an Ask User card sometimes
// does nothing and the app looks frozen". They run with the production
// burst gap and focus guard, and every wait has a timeout so a
// regression fails instead of hanging the suite.

func withProductionKeyTiming(t *testing.T) {
	t.Helper()
	gap, arm := burstGap, promptArmDelay
	burstGap, promptArmDelay = 8*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { burstGap, promptArmDelay = gap, arm })
}

func threeOptions() tools.PromptRequest {
	return tools.PromptRequest{Question: "Pick one", Options: []string{"A", "B", "C"}}
}

// waitCardReady waits for the card to hold focus and for its focus
// guard to expire, as a user reading the card would.
func waitCardReady(t *testing.T, a *App) {
	t.Helper()
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	time.Sleep(promptArmDelay + 50*time.Millisecond)
}

func wantChoice(t *testing.T, res <-chan askResult, want string) {
	t.Helper()
	select {
	case r := <-res:
		if r.err != nil || r.ans.Cancelled || r.ans.Choice != want {
			t.Fatalf("answer = %+v err=%v, want %q", r.ans, r.err, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("card never answered (want %q): the pick was lost", want)
	}
}

func wantNoAnswer(t *testing.T, res <-chan askResult, within time.Duration) {
	t.Helper()
	select {
	case r := <-res:
		t.Fatalf("card answered without a real pick: %+v err=%v", r.ans, r.err)
	case <-time.After(within):
	}
}

// Down+Enter that reach the app in one terminal read (ssh, tmux, a busy
// machine) arrive within the burst gap. That is a real pick and must
// answer the card, not be dropped as a pasted newline.
func TestAskUserCard_CoalescedArrowEnterPicks(t *testing.T) {
	withProductionKeyTiming(t)
	a, sim := startPasteSimApp(t)

	res := mountCard(t, a, context.Background(), threeOptions())
	waitCardReady(t, a)
	feedTerminal(t, sim, []byte("\x1b[B\r"))
	wantChoice(t, res, "B")

	res = mountCard(t, a, context.Background(), threeOptions())
	waitCardReady(t, a)
	feedTerminal(t, sim, []byte("\x1b[B\x1b[B\r"))
	wantChoice(t, res, "C")

	res = mountCard(t, a, context.Background(), threeOptions())
	waitCardReady(t, a)
	feedTerminal(t, sim, []byte("3"))
	wantChoice(t, res, "C")
}

// The paste guard still holds: Enter inside a burst that carried text
// (a keystroke paste) never answers a choice card; a later real Enter
// does.
func TestAskUserCard_PastedTextEnterNeverAnswers(t *testing.T) {
	withProductionKeyTiming(t)
	a, sim := startPasteSimApp(t)

	res := mountCard(t, a, context.Background(), threeOptions())
	waitCardReady(t, a)
	feedTerminal(t, sim, []byte("j\r"))
	wantNoAnswer(t, res, 300*time.Millisecond)
	feedTerminal(t, sim, []byte("\x1b[Bx\r"))
	wantNoAnswer(t, res, 300*time.Millisecond)

	injectKey(sim, tcell.KeyEnter, 0)
	wantChoice(t, res, "C") // j and Down moved A→B→C; only the real Enter picked
}

// A second card mounting while one is open (an SSH askpass card from a
// mid-turn /add while ask_user waits) used to orphan the first: once
// the second was answered, focus went to the chat input, the first card
// stayed on screen unreachable and its tool call blocked forever.
func TestAskUserCard_OverlappingCardRegainsFocus(t *testing.T) {
	withProductionKeyTiming(t)
	a, sim := startPasteSimApp(t)

	first := mountCard(t, a, context.Background(), threeOptions())
	waitCardReady(t, a)
	var firstView *promptView
	onLoop(t, a, func() { firstView = a.livePrompt.view })

	second := mountCard(t, a, context.Background(), authReq())
	waitFor(t, a, "second card", func() bool { return a.livePrompt != nil && a.livePrompt.view != firstView })
	waitCardReady(t, a)
	injectKey(sim, tcell.KeyRune, '2')
	wantChoice(t, second, "No")

	waitFor(t, a, "first card refocused", func() bool {
		return a.livePrompt != nil && a.livePrompt.view == firstView && cardFocused(a)
	})
	wantNoAnswer(t, first, 50*time.Millisecond) // nothing auto-answered it
	waitCardReady(t, a)
	injectKey(sim, tcell.KeyRune, '2')
	wantChoice(t, first, "B")
}

// Cancelling the older card (its turn was steered) while a newer one is
// open leaves the newer card live and answerable.
func TestAskUserCard_CancelOlderCardKeepsNewerLive(t *testing.T) {
	withProductionKeyTiming(t)
	a, sim := startPasteSimApp(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := mountCard(t, a, ctx, threeOptions())
	waitCardReady(t, a)
	var firstView *promptView
	onLoop(t, a, func() { firstView = a.livePrompt.view })

	second := mountCard(t, a, context.Background(), authReq())
	waitFor(t, a, "second card", func() bool { return a.livePrompt != nil && a.livePrompt.view != firstView })
	cancel()
	select {
	case r := <-first:
		if r.err == nil && !r.ans.Cancelled {
			t.Fatalf("cancelled card answered: %+v", r.ans)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled card never returned")
	}
	waitCardReady(t, a)
	onLoop(t, a, func() {
		if len(a.liveCards) != 1 {
			t.Errorf("liveCards = %d, want 1", len(a.liveCards))
		}
	})
	injectKey(sim, tcell.KeyRune, '1')
	wantChoice(t, second, "Yes, proceed")
	onLoop(t, a, func() {
		if a.livePrompt != nil || len(a.liveCards) != 0 {
			t.Errorf("cards left mounted: live=%v n=%d", a.livePrompt != nil, len(a.liveCards))
		}
	})
}
