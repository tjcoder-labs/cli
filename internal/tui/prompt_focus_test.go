package tui

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

type askResult struct {
	ans tools.PromptAnswer
	err error
}

// mountCard starts an Ask on a background goroutine (as the tool
// runner does) and waits until the card is mounted.
func mountCard(t *testing.T, a *App, ctx context.Context, req tools.PromptRequest) <-chan askResult {
	t.Helper()
	out := make(chan askResult, 1)
	go func() {
		ans, err := a.Ask(ctx, req)
		out <- askResult{ans, err}
	}()
	waitFor(t, a, "card mounted", func() bool { return a.livePrompt != nil })
	return out
}

func authReq() tools.PromptRequest {
	return tools.PromptRequest{
		Kind:     tools.PromptAuthorization,
		Question: "Run the migration?",
		Options:  []string{"Yes, proceed", "No"},
		Default:  0,
	}
}

func noArmDelay(t *testing.T) {
	old := promptArmDelay
	promptArmDelay = 0
	t.Cleanup(func() { promptArmDelay = old })
}

func cardFocused(a *App) bool {
	return a.livePrompt != nil && a.tv.GetFocus() == a.livePrompt.view
}

// With a question card up, opening and closing each menu or modal must
// hand focus back to the card (not the chat input), and the card must
// still answer from the keyboard afterwards.
func TestPromptFocus_ModalRoundTripReturnsToCard(t *testing.T) {
	noArmDelay(t)
	type opener struct {
		name string
		open func(sim tcell.SimulationScreen)
	}
	openers := []opener{
		{"agent menu (Ctrl+A)", func(sim tcell.SimulationScreen) { injectKey(sim, tcell.KeyCtrlA, 0) }},
		{"tool menu (Ctrl+T)", func(sim tcell.SimulationScreen) { injectKey(sim, tcell.KeyCtrlT, 0) }},
		{"model menu (Ctrl+P)", func(sim tcell.SimulationScreen) { injectKey(sim, tcell.KeyCtrlP, 0) }},
	}
	for _, o := range openers {
		t.Run(o.name, func(t *testing.T) {
			a, sim := startSimApp(t)
			// Known models, so the model menu opens without a network
			// fetch (which fails fast here and closes the menu itself).
			onLoop(t, a, func() { a.models = []client.ModelInfo{{Name: "m"}} })
			res := mountCard(t, a, context.Background(), authReq())
			waitFor(t, a, "card focused on mount", func() bool { return cardFocused(a) })
			o.open(sim)
			waitFor(t, a, "modal open", func() bool { return a.leftPane != nil && !cardFocused(a) })
			injectKey(sim, tcell.KeyEscape, 0)
			waitFor(t, a, "focus back on card", func() bool { return a.leftPane == nil && cardFocused(a) })
			injectKey(sim, tcell.KeyRune, '2')
			select {
			case r := <-res:
				if r.err != nil || r.ans.Cancelled || r.ans.Choice != "No" {
					t.Fatalf("answer = %+v, %v", r.ans, r.err)
				}
			case <-time.After(slashDeadlockTimeout):
				t.Fatal("card did not answer after the modal closed")
			}
			waitFor(t, a, "focus on input after answer", func() bool { return a.livePrompt == nil && a.tv.GetFocus() == a.input })
		})
	}
}

// Slash-command modal forms closed with Esc (their onCancel path) also
// return to a mounted card.
func TestPromptFocus_SlashModalFormsReturnToCard(t *testing.T) {
	noArmDelay(t)
	for _, cmd := range []string{"/task", "/memory", "/reminder"} {
		t.Run(cmd, func(t *testing.T) {
			a, sim := startSimApp(t)
			res := mountCard(t, a, context.Background(), authReq())
			// Get back to the input to type the command.
			onLoop(t, a, func() { a.tv.SetFocus(a.input) })
			typeLine(sim, cmd)
			waitFor(t, a, cmd+" open", func() bool { return a.leftPane != nil })
			injectKey(sim, tcell.KeyEscape, 0)
			waitFor(t, a, "focus back on card", func() bool { return a.leftPane == nil && cardFocused(a) })
			injectKey(sim, tcell.KeyEscape, 0)
			r := <-res
			if !r.ans.Cancelled {
				t.Fatalf("Esc on card should dismiss, got %+v", r.ans)
			}
		})
	}
}

// Without a card, closing a modal still focuses the chat input.
func TestPromptFocus_NoCardModalReturnsToInput(t *testing.T) {
	a, sim := startSimApp(t)
	injectKey(sim, tcell.KeyCtrlA, 0)
	waitFor(t, a, "modal open", func() bool { return a.leftPane != nil })
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "focus on input", func() bool { return a.leftPane == nil && a.tv.GetFocus() == a.input })
}

// Switching side panels (Alt+T / Alt+A) and toggling fullscreen keep
// the card reachable; Tab from the chat input reaches it too.
func TestPromptFocus_PanelsFullscreenAndTabReachCard(t *testing.T) {
	noArmDelay(t)
	a, sim := startSimApp(t)
	res := mountCard(t, a, context.Background(), authReq())
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })

	sim.InjectKey(tcell.KeyRune, 't', tcell.ModAlt)
	waitFor(t, a, "card focused after Alt+T", func() bool { return a.activePanel == "tasks" && cardFocused(a) })
	sim.InjectKey(tcell.KeyRune, 'a', tcell.ModAlt)
	waitFor(t, a, "card focused after Alt+A", func() bool { return a.activePanel == "activity" && cardFocused(a) })

	sim.InjectKey(tcell.KeyRune, 'f', tcell.ModAlt)
	waitFor(t, a, "fullscreen hides card", func() bool { return a.fullscreen && !cardFocused(a) })
	sim.InjectKey(tcell.KeyRune, 'f', tcell.ModAlt)
	waitFor(t, a, "card focused after fullscreen off", func() bool { return !a.fullscreen && cardFocused(a) })

	onLoop(t, a, func() { a.tv.SetFocus(a.input) })
	injectKey(sim, tcell.KeyTab, 0)
	waitFor(t, a, "Tab reaches card", func() bool { return cardFocused(a) })

	injectKey(sim, tcell.KeyRune, '1')
	r := <-res
	if r.ans.Choice != "Yes, proceed" || r.ans.Cancelled {
		t.Fatalf("answer = %+v", r.ans)
	}
	// Tab with no card does nothing special: focus stays on the input.
	waitFor(t, a, "input focused", func() bool { return a.tv.GetFocus() == a.input })
	injectKey(sim, tcell.KeyTab, 0)
	waitFor(t, a, "still input", func() bool { return a.tv.GetFocus() == a.input })
}

// A card that arrives while a modal is open must not steal focus from
// it; it takes focus when the modal closes.
func TestPromptFocus_MountDuringModalWaits(t *testing.T) {
	noArmDelay(t)
	a, sim := startSimApp(t)
	injectKey(sim, tcell.KeyCtrlT, 0)
	waitFor(t, a, "modal open", func() bool { return a.leftPane != nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := mountCard(t, a, ctx, authReq())
	waitFor(t, a, "modal keeps focus", func() bool { return a.leftPane != nil && !cardFocused(a) && a.tv.GetFocus() != a.input })
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "card focused after close", func() bool { return a.leftPane == nil && cardFocused(a) })
	cancel()
	r := <-res
	if r.err == nil || !r.ans.Cancelled {
		t.Fatalf("cancelled turn should not answer: %+v %v", r.ans, r.err)
	}
	waitFor(t, a, "input focused after cancel", func() bool { return a.livePrompt == nil && a.tv.GetFocus() == a.input })
}

// Enter (or a digit) pressed right as the card appears is swallowed, so
// the pre-selected default can never be chosen by a stray keystroke.
func TestPromptFocus_EnterOnMountDoesNotAnswer(t *testing.T) {
	old := promptArmDelay
	promptArmDelay = 2 * time.Second
	t.Cleanup(func() { promptArmDelay = old })
	a, sim := startSimApp(t)
	res := mountCard(t, a, context.Background(), authReq())
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	injectKey(sim, tcell.KeyEnter, 0)
	injectKey(sim, tcell.KeyRune, '1')
	select {
	case r := <-res:
		t.Fatalf("stray key answered the card: %+v", r.ans)
	case <-time.After(300 * time.Millisecond):
	}
	// Esc is always honoured and is a dismissal, not an approval.
	injectKey(sim, tcell.KeyEscape, 0)
	r := <-res
	if !r.ans.Cancelled || r.ans.Choice != "" {
		t.Fatalf("Esc should dismiss: %+v", r.ans)
	}
}

// Ctrl-U clears the card's Other… text instead of scrolling.
func TestPromptFocus_CtrlUReachesCardTextEntry(t *testing.T) {
	noArmDelay(t)
	a, sim := startSimApp(t)
	req := tools.PromptRequest{Question: "Name?", AllowOther: true}
	res := mountCard(t, a, context.Background(), req)
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	for _, r := range "abc" {
		injectKey(sim, tcell.KeyRune, r)
	}
	injectKey(sim, tcell.KeyCtrlU, 0)
	for _, r := range "xy" {
		injectKey(sim, tcell.KeyRune, r)
	}
	injectKey(sim, tcell.KeyEnter, 0)
	r := <-res
	if r.ans.Other != "xy" {
		t.Fatalf("Other = %q, want %q", r.ans.Other, "xy")
	}
}
