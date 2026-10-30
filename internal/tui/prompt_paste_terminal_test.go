package tui

import (
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

// Free-text answers: CR (what macOS terminals send) and CRLF become a
// space; a bare LF is dropped by tview's paste assembly (tcell reports
// it as Ctrl-J, which tview does not collect), joining the lines.
func TestPromptPasteTerminal_FreeTextLineEndings(t *testing.T) {
	noArmDelay(t)
	for raw, want := range map[string]string{
		"one\rtwo\r": "one two", "one\r\ntwo\r\n": "one two", "one\ntwo\n": "onetwo",
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

// Without bracketed paste (terminal or tmux not forwarding it) the paste
// arrives as ordinary keystrokes. A single-line token still lands in the
// hidden buffer, and a trailing CR acts as Enter and submits it. A CR in
// the middle would submit early; that is inherent to keystroke input.
func TestPromptPasteTerminal_RawKeystrokesWithoutBracketedPaste(t *testing.T) {
	noArmDelay(t)
	a, sim := startPasteSimApp(t)
	out := askSecretAsync(a)
	waitFor(t, a, "card focused", func() bool { return cardFocused(a) })
	tok := tokenOf(64)
	feedTerminal(t, sim, []byte(tok+"\r"))
	select {
	case b := <-out:
		if string(b) != tok {
			t.Fatalf("got %q, want %q", b, tok)
		}
	case <-time.After(slashDeadlockTimeout):
		t.Fatal("trailing CR did not submit the secret")
	}
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
