package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// These tests feed raw terminal bytes through tcell's own input parser
// (the code that reads the tty on macOS and Linux alike) and forward the
// events into a simulated app, so the whole path is exercised:
// ESC[200~ … ESC[201~ → tcell EventPaste/EventKey → tview paste
// assembly → promptView.PasteHandler → hidden buffer.

// feedTerminal parses b exactly as tcell parses tty input and posts the
// resulting events to sim, in order.
func feedTerminal(t *testing.T, sim tcell.SimulationScreen, b []byte) {
	t.Helper()
	ch := make(chan tcell.Event, 1024)
	ip := tcell.NewInputProcessor(ch)
	go ip.ScanUTF8(b) // ip blocks when ch is full, like a real tty reader
	for {
		select {
		case ev := <-ch:
			simPost(sim, ev)
		case <-time.After(150 * time.Millisecond): // past tcell's 50ms lone-ESC timeout
			time.Sleep(20 * time.Millisecond)
			return
		}
	}
}

func bracketed(s string) []byte { return []byte("\x1b[200~" + s + "\x1b[201~") }

// tokenOf returns an n-byte credential-like string mixing the awkward
// characters a pasted secret can contain.
func tokenOf(n int) string {
	const alphabet = `AZaz09-_.~+/=!@#$%^&*()[]{}<>?,;:'"\|` + "` "
	var sb strings.Builder
	for i := 0; sb.Len() < n; i++ {
		sb.WriteByte(alphabet[i%len(alphabet)])
	}
	return sb.String()[:n]
}

func pasteSecretViaTerminal(t *testing.T, raw []byte, wantLen int) (*App, tcell.SimulationScreen, <-chan []byte) {
	t.Helper()
	a, sim := startPasteSimApp(t)
	out := askSecretAsync(a)
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	feedTerminal(t, sim, raw)
	waitSecretLen(t, a, wantLen)
	return a, sim, out
}

func submitSecret(t *testing.T, sim tcell.SimulationScreen, out <-chan []byte) []byte {
	t.Helper()
	injectKey(sim, tcell.KeyEnter, 0)
	select {
	case b := <-out:
		return b
	case <-time.After(slashDeadlockTimeout):
		t.Fatal("secret card did not return")
	}
	return nil
}

// Long credentials with quotes, backslashes, $, spaces, tabs and
// Unicode survive a bracketed paste byte-for-byte, with no truncation.
func TestPromptPasteTerminal_LongSpecialTokens(t *testing.T) {
	noArmDelay(t)
	cases := map[string]string{
		"40 bytes":   tokenOf(40),
		"256 bytes":  tokenOf(256),
		"4 KiB":      tokenOf(4096),
		"tab+unicde": "pa\tss wörd ✓ $HOME \\n 'q' \"dq\" `bt` 🔑",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			a, sim, out := pasteSecretViaTerminal(t, bracketed(tok+"\r"), len(tok))
			var shown string
			onLoop(t, a, func() { shown = a.livePrompt.view.GetText(true) })
			if len(tok) >= 8 && strings.Contains(shown, tok[:8]) {
				t.Fatalf("secret visible in card")
			}
			if got := submitSecret(t, sim, out); string(got) != tok {
				t.Fatalf("secret mismatch: got %d bytes %q, want %d bytes", len(got), got, len(tok))
			}
		})
	}
}

// Line endings: macOS terminals send CR for a pasted newline; LF and
// CRLF are also handled. None of them reach the secret, and a newline
// inside a bracketed paste never submits the card early.
func TestPromptPasteTerminal_LineEndings(t *testing.T) {
	noArmDelay(t)
	for name, raw := range map[string]string{
		"LF": "tok-123\n", "CRLF": "tok-123\r\n", "CR": "tok-123\r", "CRCR": "tok-123\r\r",
		"embedded CR": "tok-\r123", "embedded LF": "tok-\n123",
	} {
		t.Run(name, func(t *testing.T) {
			_, sim, out := pasteSecretViaTerminal(t, bracketed(raw), len("tok-123"))
			if got := submitSecret(t, sim, out); string(got) != "tok-123" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

// Free-text answers: CR (what macOS terminals send), CRLF and a bare LF
// all become a space; trailing newlines are dropped.
func TestPromptPasteTerminal_FreeTextLineEndings(t *testing.T) {
	noArmDelay(t)
	for raw, want := range map[string]string{
		"one\rtwo\r": "one two", "one\r\ntwo\r\n": "one two", "one\ntwo\n": "one two",
	} {
		a, sim := startPasteSimApp(t)
		res := mountCard(t, a, t.Context(), freeTextPrompt().req)
		waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
		feedTerminal(t, sim, bracketed(raw))
		injectKey(sim, tcell.KeyEnter, 0)
		r := <-res
		if r.ans.Other != want {
			t.Errorf("paste %q: answer %q, want %q", raw, r.ans.Other, want)
		}
	}
}

// Escape sequences and control bytes inside a paste are not kept:
// tcell parses them as keys and tview's paste assembly keeps only runes,
// Enter and Tab. A lone ESC+letter keeps the letter (as Alt+letter).
// Real credentials never contain these bytes; documented here.
func TestPromptPasteTerminal_ControlBytesDropped(t *testing.T) {
	noArmDelay(t)
	_, sim, out := pasteSecretViaTerminal(t, bracketed("ab\x1b[Acd\x7fef\x01gh"), len("abcdefgh"))
	if got := submitSecret(t, sim, out); string(got) != "abcdefgh" {
		t.Fatalf("got %q", got)
	}
}

// withBurstDetection turns keystroke-burst detection back on (the
// package tests disable it because simulated typing is instantaneous).
func withBurstDetection(t *testing.T) {
	old := burstGap
	burstGap = 8 * time.Millisecond
	t.Cleanup(func() { burstGap = old })
}

func init() { burstGap = 0 }

// Without bracketed paste (terminal or tmux not forwarding it) a paste
// arrives as keystrokes from one tty read, microseconds apart. A CR in
// the middle or at the end of that burst is a pasted newline, not a
// submit: the whole token lands in the hidden buffer, nothing leaks
// into the chat input, and the user's own Enter submits.
func TestPromptPasteTerminal_RawKeystrokesWithoutBracketedPaste(t *testing.T) {
	noArmDelay(t)
	withBurstDetection(t)
	for name, raw := range map[string]string{"trailing CR": "%s\r", "mid CR": "%s\r%s\r", "CRLF": "%s\r\n%s"} {
		t.Run(name, func(t *testing.T) {
			a, sim := startPasteSimApp(t)
			out := askSecretAsync(a)
			waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
			half := tokenOf(64)
			var want string
			var bytes string
			if strings.Count(raw, "%s") == 2 {
				bytes, want = fmt.Sprintf(raw, half[:32], half[32:]), half
			} else {
				bytes, want = fmt.Sprintf(raw, half), half
			}
			feedTerminal(t, sim, []byte(bytes))
			waitSecretLen(t, a, len(want))
			select {
			case <-out:
				t.Fatal("a CR inside the keystroke paste submitted the secret")
			default:
			}
			var chat string
			onLoop(t, a, func() { chat = a.input.GetText() })
			if chat != "" {
				t.Fatalf("paste leaked into the chat input: %q", chat)
			}
			if got := submitSecret(t, sim, out); string(got) != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

// Keystroke paste into a free-text answer: pasted CRs become spaces
// instead of sending a partial answer.
func TestPromptPasteTerminal_RawKeystrokesFreeText(t *testing.T) {
	noArmDelay(t)
	withBurstDetection(t)
	a, sim := startPasteSimApp(t)
	res := mountCard(t, a, t.Context(), freeTextPrompt().req)
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	feedTerminal(t, sim, []byte("one\rtwo\r"))
	injectKey(sim, tcell.KeyEnter, 0)
	if r := <-res; r.ans.Other != "one two" {
		t.Fatalf("answer %q", r.ans.Other)
	}
}

// A keystroke paste that starts inside the focus guard is dropped whole
// (all or nothing), even the part arriving after the guard expires, and
// the card says so; a later paste is accepted.
func TestPromptPasteTerminal_RawBurstInsideGuardDroppedWhole(t *testing.T) {
	withBurstDetection(t)
	old := promptArmDelay
	promptArmDelay = 40 * time.Millisecond
	t.Cleanup(func() { promptArmDelay = old })
	a, sim := startPasteSimApp(t)
	out := askSecretAsync(a)
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	// ~120 keys 1 ms apart: the burst outlasts the 40 ms guard.
	for _, r := range tokenOf(120) {
		simPost(sim, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	var n int
	var shown string
	onLoop(t, a, func() { n, shown = len(a.livePrompt.view.st.buf), a.livePrompt.view.GetText(true) })
	if n != 0 {
		t.Fatalf("guard let %d bytes of the burst through (partial capture)", n)
	}
	if !strings.Contains(shown, pasteIgnoredNotice) {
		t.Fatalf("no notice shown:\n%s", shown)
	}
	feedTerminal(t, sim, []byte("good-token"))
	waitSecretLen(t, a, len("good-token"))
	onLoop(t, a, func() { shown = a.livePrompt.view.GetText(true) })
	if strings.Contains(shown, pasteIgnoredNotice) {
		t.Fatal("notice not cleared by the accepted paste")
	}
	if got := submitSecret(t, sim, out); string(got) != "good-token" {
		t.Fatalf("got %q", got)
	}
}

// When a keystroke burst finishes a card (a digit picks an option), the
// rest of the burst is swallowed instead of landing in the chat input;
// typing afterwards reaches the input normally.
func TestPromptPasteTerminal_BurstTailAfterFinishSwallowed(t *testing.T) {
	noArmDelay(t)
	withBurstDetection(t)
	a, sim := startPasteSimApp(t)
	res := mountCard(t, a, t.Context(), authReq())
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	feedTerminal(t, sim, []byte("1secret-tail\r"))
	if r := <-res; r.ans.Index != 0 {
		t.Fatalf("answer %+v", r.ans)
	}
	var chat string
	onLoop(t, a, func() { chat = a.input.GetText() })
	if chat != "" {
		t.Fatalf("burst tail leaked into the chat input: %q", chat)
	}
	waitFor(t, a, "input focused", func() bool { return a.tv.GetFocus() == a.input })
	injectKey(sim, tcell.KeyRune, 'h')
	injectKey(sim, tcell.KeyRune, 'i')
	waitFor(t, a, "typing reaches chat input", func() bool { return a.input.GetText() == "hi" })
}

// A bracketed paste arriving inside the 0.3 s focus guard is dropped
// whole (never half-applied); one after the guard is accepted.
func TestPromptPasteTerminal_FocusGuard(t *testing.T) {
	old := promptArmDelay
	promptArmDelay = 300 * time.Millisecond
	t.Cleanup(func() { promptArmDelay = old })
	a, sim := startPasteSimApp(t)
	out := askSecretAsync(a)
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	feedTerminal(t, sim, bracketed("early-token\r"))
	var n int
	onLoop(t, a, func() { n = len(a.livePrompt.view.st.buf) })
	if n != 0 {
		t.Fatalf("paste inside the guard stored %d bytes", n)
	}
	time.Sleep(promptArmDelay)
	feedTerminal(t, sim, bracketed("late-token\r"))
	waitSecretLen(t, a, len("late-token"))
	if got := submitSecret(t, sim, out); string(got) != "late-token" {
		t.Fatalf("got %q", got)
	}
}

// A very long pasted secret must not grow the card past the screen: the
// mask is capped and shows a count instead (4 KiB was ~117 rows before).
func TestPromptPasteTerminal_LongSecretCardStaysSmall(t *testing.T) {
	noArmDelay(t)
	a, sim, out := pasteSecretViaTerminal(t, bracketed(tokenOf(4096)), 4096)
	var shown string
	onLoop(t, a, func() { shown = a.livePrompt.view.GetText(true) })
	if rows := strings.Count(shown, "\n") + 2; rows > 16 {
		t.Fatalf("card is %d rows for a 4 KiB secret", rows)
	}
	if !strings.Contains(shown, strings.Repeat("•", secretMaskMax)+"…") || strings.Contains(shown, strings.Repeat("•", secretMaskMax+1)) || !strings.Contains(shown, "4096 chars") {
		t.Fatalf("capped mask/count missing:\n%s", shown)
	}
	if got := submitSecret(t, sim, out); string(got) != tokenOf(4096) {
		t.Fatalf("secret mismatch (%d bytes)", len(got))
	}
}

func TestSecretMask(t *testing.T) {
	if got := secretMask(5); got != "•••••" {
		t.Fatalf("%q", got)
	}
	if got := secretMask(secretMaskMax); got != strings.Repeat("•", secretMaskMax) {
		t.Fatalf("%q", got)
	}
	if got := secretMask(200); got != strings.Repeat("•", secretMaskMax)+"… 200 chars" {
		t.Fatalf("%q", got)
	}
}

// The screen wrapper turns a bare LF inside a bracketed paste into a
// newline (tview would drop it), keeps CRLF a single newline, and leaves
// Ctrl-J outside a paste alone.
func TestLFPasteScreen(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	defer sim.Fini()
	w := &lfPasteScreen{Screen: sim}
	ch := make(chan tcell.Event, 64)
	ip := tcell.NewInputProcessor(ch)
	ip.ScanUTF8([]byte("\n" + "\x1b[200~a\nb\r\nc\x1b[201~" + "\n"))
	var got []string
	for len(got) < 9 {
		ev := <-ch
		simPost(sim, ev)
		switch e := w.PollEvent().(type) {
		case *tcell.EventKey:
			got = append(got, e.Name())
		case *tcell.EventPaste:
			got = append(got, map[bool]string{true: "start", false: "end"}[e.Start()])
		}
	}
	want := "Ctrl+J start Rune[a] Enter Rune[b] Enter Ctrl+J Rune[c] end"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}
