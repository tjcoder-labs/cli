package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/render"
	"github.com/tjcoder-labs/cli/internal/theme"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// promptState is the keyboard state machine behind the in-transcript
// prompt card. It is UI-free so it can be unit-tested directly:
//
//   - 1-9 picks an option immediately (toggles it when multi-select)
//   - Up/Down (or k/j, Tab) move the cursor, Enter confirms
//   - Space toggles the highlighted option when multi-select
//   - Esc cancels (an authorization prompt treats that as deny)
//   - with AllowOther, the trailing free-text row opens a text line; a
//     prompt with no options starts in that line
//   - in secret mode the typed bytes are kept in a []byte, rendered as
//     bullets, and never stored anywhere else
type promptState struct {
	req      tools.PromptRequest
	selected int
	checked  []bool // multi-select ticks, one per option
	editing  bool   // typing into the free-text line
	other    []rune // free-text answer
	secret   bool   // masked single-line input (passwords)
	label    string
	buf      []byte // secret bytes; zeroed by the consumer / on cancel
	notice   string // short muted note under the input (e.g. paste ignored)
}

func newChoicePrompt(req tools.PromptRequest) *promptState {
	s := &promptState{req: req, checked: make([]bool, len(req.Options))}
	if req.Default > 0 && req.Default <= len(req.Options) {
		s.selected = req.Default - 1
	}
	if len(req.Options) == 0 && req.AllowOther {
		s.editing = true
	}
	return s
}

func newSecretPrompt(title, label string) *promptState {
	return &promptState{secret: true, label: label, req: tools.PromptRequest{Kind: tools.PromptAuthorization, Title: title}}
}

// rows is the number of selectable rows (options + optional Other).
func (s *promptState) rows() int {
	n := len(s.req.Options)
	if s.req.AllowOther {
		n++
	}
	return n
}

func (s *promptState) isOtherRow(i int) bool { return s.req.AllowOther && i == len(s.req.Options) }

func (s *promptState) otherLabel() string {
	if s.req.OtherLabel != "" {
		return s.req.OtherLabel
	}
	return "Other…"
}

// freeTextOnly reports a question with no options at all.
func (s *promptState) freeTextOnly() bool { return len(s.req.Options) == 0 && s.req.AllowOther }

// zeroSecret wipes the masked buffer.
func (s *promptState) zeroSecret() {
	for i := range s.buf {
		s.buf[i] = 0
	}
	s.buf = nil
}

// appendSecret appends p to the masked buffer, growing the backing
// array manually so the old one can be wiped rather than left for the
// GC with secret bytes still in it.
func (s *promptState) appendSecret(p []byte) {
	if len(p) == 0 {
		return
	}
	if cap(s.buf)-len(s.buf) < len(p) {
		nb := make([]byte, len(s.buf), 2*cap(s.buf)+len(p)+16)
		copy(nb, s.buf)
		s.zeroSecret()
		s.buf = nb
	}
	s.buf = append(s.buf, p...)
}

// paste appends pasted text to the active input line and reports
// whether anything changed. For secret prompts the bytes go into the
// masked buffer with every CR/LF dropped, so a key copied with its
// trailing newline (or a terminal that turns the newline into Enter)
// never smuggles a line break into the secret. For an open free-text
// line the text goes into the visible answer: trailing newlines are
// trimmed and embedded ones become spaces, because the answer is a
// single line and a raw newline would break the card frame. Choice
// rows (not editing) ignore paste.
func (s *promptState) paste(text string) bool {
	if s.secret {
		clean := make([]byte, 0, len(text))
		for i := 0; i < len(text); i++ {
			if c := text[i]; c != '\r' && c != '\n' {
				clean = append(clean, c)
			}
		}
		s.appendSecret(clean)
		if len(clean) > 0 {
			s.notice = ""
		}
		for i := range clean {
			clean[i] = 0
		}
		return len(clean) > 0
	}
	if !s.editing {
		return false
	}
	text = strings.TrimRight(text, "\r\n")
	text = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(text)
	s.notice = ""
	if text == "" {
		return false
	}
	s.other = append(s.other, []rune(text)...)
	return true
}

// handleKey applies one key. done reports the prompt finished; ans is
// the result for choice prompts; for secret prompts the caller takes
// s.buf (nil when cancelled).
func (s *promptState) handleKey(ev *tcell.EventKey) (done bool, ans tools.PromptAnswer) {
	if s.secret {
		switch ev.Key() {
		case tcell.KeyEscape:
			s.zeroSecret()
			return true, tools.PromptAnswer{Index: -1, Cancelled: true}
		case tcell.KeyEnter:
			return true, tools.PromptAnswer{Index: 0}
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(s.buf) > 0 {
				_, size := utf8.DecodeLastRune(s.buf)
				for i := len(s.buf) - size; i < len(s.buf); i++ {
					s.buf[i] = 0
				}
				s.buf = s.buf[:len(s.buf)-size]
			}
		case tcell.KeyCtrlU:
			s.zeroSecret()
		case tcell.KeyRune:
			var tmp [utf8.UTFMax]byte
			n := utf8.EncodeRune(tmp[:], ev.Rune())
			s.appendSecret(tmp[:n])
		}
		return false, tools.PromptAnswer{}
	}

	if s.editing {
		switch ev.Key() {
		case tcell.KeyEscape:
			if s.freeTextOnly() {
				return true, tools.PromptAnswer{Index: -1, Cancelled: true}
			}
			s.editing = false
		case tcell.KeyEnter:
			text := strings.TrimSpace(string(s.other))
			if text == "" {
				break
			}
			if s.req.MultiSelect {
				return true, s.multiAnswer(text)
			}
			return true, tools.PromptAnswer{Index: -1, Other: text}
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(s.other) > 0 {
				s.other = s.other[:len(s.other)-1]
			}
		case tcell.KeyCtrlU:
			s.other = s.other[:0]
		case tcell.KeyRune:
			s.other = append(s.other, ev.Rune())
		}
		return false, tools.PromptAnswer{}
	}

	n := s.rows()
	switch ev.Key() {
	case tcell.KeyEscape:
		return true, tools.PromptAnswer{Index: -1, Cancelled: true}
	case tcell.KeyUp, tcell.KeyBacktab:
		s.move(-1)
	case tcell.KeyDown, tcell.KeyTab:
		s.move(1)
	case tcell.KeyEnter:
		if s.req.MultiSelect && !s.isOtherRow(s.selected) {
			if !s.anyChecked() {
				s.checked[s.selected] = true
			}
			return true, s.multiAnswer(strings.TrimSpace(string(s.other)))
		}
		return s.choose(s.selected)
	case tcell.KeyRune:
		r := ev.Rune()
		switch {
		case r >= '1' && r <= '9':
			if i := int(r - '1'); i < n {
				s.selected = i
				if s.req.MultiSelect && !s.isOtherRow(i) {
					s.checked[i] = !s.checked[i]
					return false, tools.PromptAnswer{}
				}
				return s.choose(i)
			}
		case r == ' ' && s.req.MultiSelect:
			if s.isOtherRow(s.selected) {
				s.editing = true
			} else if n > 0 {
				s.checked[s.selected] = !s.checked[s.selected]
			}
		case r == 'k':
			s.move(-1)
		case r == 'j':
			s.move(1)
		}
	}
	return false, tools.PromptAnswer{}
}

func (s *promptState) move(d int) {
	if n := s.rows(); n > 0 {
		s.selected = (s.selected + d + n) % n
	}
}

func (s *promptState) anyChecked() bool {
	for _, c := range s.checked {
		if c {
			return true
		}
	}
	return false
}

// multiAnswer collects the ticked options (in option order) plus other.
func (s *promptState) multiAnswer(other string) tools.PromptAnswer {
	ans := tools.PromptAnswer{Index: -1, Other: other, Indices: []int{}}
	for i, c := range s.checked {
		if c {
			if ans.Index < 0 {
				ans.Index = i
			}
			ans.Indices = append(ans.Indices, i)
			ans.Choices = append(ans.Choices, s.req.Options[i])
		}
	}
	if ans.Index >= 0 {
		ans.Choice = ans.Choices[0]
	}
	return ans
}

func (s *promptState) choose(i int) (bool, tools.PromptAnswer) {
	if i < 0 || i >= s.rows() {
		return false, tools.PromptAnswer{}
	}
	if s.isOtherRow(i) {
		s.editing = true
		return false, tools.PromptAnswer{}
	}
	return true, tools.PromptAnswer{Index: i, Choice: s.req.Options[i]}
}

func promptHeading(req tools.PromptRequest) string {
	if req.Title != "" {
		return strings.ToUpper(req.Title)
	}
	switch req.Kind {
	case tools.PromptAuthorization:
		return "AUTHORIZATION"
	case tools.PromptNextSteps:
		return "NEXT STEPS"
	case tools.PromptError:
		return "ERROR"
	default:
		return "QUESTION"
	}
}

// cardHeader is the shared top of every prompt card: heading,
// question and detail.
func cardHeader(th theme.Theme, req tools.PromptRequest, secret bool) []render.Line {
	headFg := th.Primary
	if req.Kind == tools.PromptError {
		headFg = cardErrorAccent
	}
	head := render.Line{{Text: promptHeading(req), Fg: headFg, Attr: "b"}}
	if req.MultiSelect && !secret {
		head = append(head, render.Span{Text: "  · choose any", Fg: th.Dim})
	}
	out := []render.Line{head}
	if req.Question != "" {
		for _, q := range strings.Split(req.Question, "\n") {
			out = append(out, render.Line{{Text: q, Fg: th.Text}})
		}
	}
	if req.Detail != "" {
		prefix := ""
		if req.Kind == tools.PromptAuthorization {
			prefix = "$ "
		}
		out = append(out, render.Line{{Text: "  "}, {Text: prefix + req.Detail, Fg: th.Soft}})
	}
	return out
}

// secretMaskMax caps the bullets shown for a secret so a long pasted
// credential (a 4 KiB token is ~100 wrapped rows) cannot grow the card
// past the screen; longer secrets show the capped mask plus a count.
const secretMaskMax = 32

func secretMask(n int) string {
	if n <= secretMaskMax {
		return strings.Repeat("•", n)
	}
	return strings.Repeat("•", secretMaskMax) + "… " + strconv.Itoa(n) + " chars"
}

// content returns the live card's rows (before boxing).
func (s *promptState) content(th theme.Theme) []render.Line {
	out := cardHeader(th, s.req, s.secret)
	out = append(out, nil)
	cursor := render.Span{Text: "▏", Fg: th.Primary}
	if s.secret {
		out = append(out,
			render.Line{{Text: s.label, Fg: th.Text}},
			render.Line{{Text: "› ", Fg: th.Bright, Attr: "b"}, {Text: secretMask(utf8.RuneCount(s.buf)), Fg: th.Text}, cursor},
			s.noticeLine(th),
			render.Line{{Text: "Enter to submit · Esc to cancel · input is hidden and never sent to the model", Fg: th.Faint}})
		return out
	}
	for i := 0; i < s.rows(); i++ {
		other := s.isOtherRow(i)
		label := s.otherLabel()
		if !other {
			label = s.req.Options[i]
		}
		num := "  "
		if i < 9 {
			num = fmt.Sprintf("%d.", i+1)
		}
		box := ""
		if s.req.MultiSelect && !other {
			box = "○ "
			if s.checked[i] {
				box = "● "
			}
		}
		if i == s.selected && !(other && s.editing && s.freeTextOnly()) {
			out = append(out, render.Line{{Text: "› " + num + " ", Fg: th.Bright, Attr: "b"}, {Text: box, Fg: th.Primary}, {Text: label, Fg: th.Bright, Attr: "b"}})
		} else if !s.freeTextOnly() {
			out = append(out, render.Line{{Text: "  " + num + " ", Fg: th.Dim}, {Text: box, Fg: th.Primary}, {Text: label, Fg: th.Dim}})
		}
		if !other && i < len(s.req.Descriptions) && s.req.Descriptions[i] != "" {
			out = append(out, render.Line{{Text: "      "}, {Text: s.req.Descriptions[i], Fg: th.Faint}})
		}
		if other && s.editing {
			lead := "      "
			if s.freeTextOnly() {
				lead = "› "
			}
			out = append(out, render.Line{{Text: lead, Fg: th.Bright, Attr: "b"}, {Text: string(s.other), Fg: th.Text}, cursor})
		}
	}
	out = append(out, s.noticeLine(th), render.Line{{Text: s.hint(), Fg: th.Faint}})
	return out
}

// noticeLine is the (usually empty) muted row above the key hints.
func (s *promptState) noticeLine(th theme.Theme) render.Line {
	if s.notice == "" {
		return nil
	}
	return render.Line{{Text: s.notice, Fg: th.Dim}}
}

func (s *promptState) hint() string {
	esc := "Esc to dismiss"
	if s.req.Kind == tools.PromptAuthorization {
		esc = "Esc to deny"
	}
	switch {
	case s.editing && s.freeTextOnly():
		return "type your answer · Enter to send · " + esc
	case s.editing:
		return "type your answer · Enter to send · Esc to go back"
	case s.req.MultiSelect:
		return "1-9 or Space to tick · ↑↓ to move · Enter to confirm · " + esc
	}
	return "1-9 or ↑↓ Enter to choose · " + esc
}

// render returns the boxed live card, width cells wide.
func (s *promptState) render(p Palette, width int) string {
	return renderCard(p.Theme, s.content(p.Theme), width, cardAccentFor(s.req))
}

// cardAccentFor picks the left-rule colour. Error cards are red; every
// other card keeps the brand purple.
func cardAccentFor(req tools.PromptRequest) string {
	if req.Kind == tools.PromptError {
		return cardErrorAccent
	}
	return ""
}

// promptOutcome is how a prompt ended, for the answered card.
type promptOutcome int

const (
	outcomeAnswered promptOutcome = iota
	outcomeDismissed
	outcomeInterrupted
	outcomeCancelled
)

// answeredContent returns the collapsed card left in the transcript:
// the same header, then the answer row (status by glyph and shade).
func answeredContent(th theme.Theme, req tools.PromptRequest, ans tools.PromptAnswer, how promptOutcome) []render.Line {
	out := cardHeader(th, req, false)
	out = append(out, nil)
	switch how {
	case outcomeAnswered:
		text := ans.Text()
		if ans.Index < 0 && len(ans.Choices) == 0 && ans.Other != "" {
			text = "“" + ans.Other + "”"
		}
		glyph := "✓ "
		if req.Kind == tools.PromptAuthorization && strings.HasPrefix(strings.ToLower(ans.Choice), "deny") {
			glyph = "✗ "
		}
		out = append(out, render.Line{{Text: glyph, Fg: th.Bright, Attr: "b"}, {Text: text, Fg: th.Bright, Attr: "b"}})
	case outcomeDismissed:
		if req.Kind == tools.PromptAuthorization {
			out = append(out, render.Line{{Text: "✗ denied", Fg: th.Bright, Attr: "b"}, {Text: " · dismissed with Esc", Fg: th.Dim}})
		} else {
			out = append(out, render.Line{{Text: "• dismissed", Fg: th.Dim, Attr: "b"}, {Text: " · the agent will proceed without an answer", Fg: th.Faint}})
		}
	case outcomeInterrupted:
		out = append(out, render.Line{{Text: "• interrupted", Fg: th.Dim, Attr: "b"}, {Text: " · you sent a new message instead", Fg: th.Faint}})
	default:
		out = append(out, render.Line{{Text: "• cancelled", Fg: th.Dim, Attr: "b"}, {Text: " · the turn ended before an answer", Fg: th.Faint}})
	}
	return out
}

// --- App as tools.Prompter -------------------------------------------------

var _ tools.Prompter = (*App)(nil)

// promptGuard is the focus guard shared by keys and paste: for
// promptArmDelay after the card gains focus, everything except Esc is
// ignored, so an Enter, digit or paste aimed at the chat input (or the
// modal that just closed) can never answer the card.
type promptGuard struct{ armedAt atomic.Int64 }

// arm records that the card just gained focus.
func (g *promptGuard) arm() { g.armedAt.Store(promptNow().UnixNano()) }

// settling reports whether the card is still inside the guard window.
func (g *promptGuard) settling() bool {
	return promptNow().Sub(time.Unix(0, g.armedAt.Load())) < promptArmDelay
}

// promptNow is the guard's clock. Tests replace it.
var promptNow = time.Now

// burstGap is the longest pause between two key events that still
// counts as one burst. A terminal without bracketed paste delivers a
// paste as keystrokes parsed from the same read, microseconds apart;
// even very fast typing is tens of milliseconds per key. Zero disables
// burst detection (the package tests do, since simulated typing is
// instantaneous; the burst tests turn it back on).
var burstGap = 8 * time.Millisecond

// burstSwallow is how long, at most, the rest of a burst is swallowed
// after it finished a card, so it never spills into the chat input.
const burstSwallow = 150 * time.Millisecond

// pasteIgnoredNotice is shown when a keystroke paste hit the guard.
const pasteIgnoredNotice = "paste ignored · try again"

// keyBurst groups key events that arrive back to back (a paste typed
// out by a terminal without bracketed paste) by their tcell timestamps.
type keyBurst struct {
	last     time.Time
	n        int  // keys in the current burst
	dropping bool // the burst started inside the guard: drop all of it
}

// step records a key at t and reports whether it continues the burst
// of the previous key.
func (b *keyBurst) step(t time.Time) bool {
	d := t.Sub(b.last)
	cont := burstGap > 0 && !b.last.IsZero() && d >= 0 && d < burstGap
	b.last = t
	if !cont {
		b.n, b.dropping = 0, false
	}
	b.n++
	return cont
}

// burstFilter applies the keystroke-paste rules before a key reaches
// the state machine and returns the key to apply, or nil to drop it:
//   - a burst whose first key lands inside the focus guard is dropped
//     whole, even the part after the guard expires, with a short notice,
//     so a paste is never half-captured;
//   - Enter (or LF) inside a burst is a pasted newline, never a submit:
//     dropped for secrets and choice rows, a space in free text.
//
// Esc always passes.
func (v *promptView) burstFilter(ev *tcell.EventKey) *tcell.EventKey {
	inBurst := v.burst.step(ev.When())
	if ev.Key() == tcell.KeyEscape {
		return ev
	}
	if v.guard.settling() {
		v.burst.dropping = true
	}
	if v.burst.dropping {
		if v.burst.n == 2 && v.st != nil {
			v.st.notice = pasteIgnoredNotice
			if v.redraw != nil {
				v.redraw()
			}
		}
		return nil
	}
	if inBurst && (ev.Key() == tcell.KeyEnter || ev.Key() == tcell.KeyCtrlJ) {
		if v.st != nil && !v.st.secret && v.st.editing {
			return tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone)
		}
		return nil
	}
	return ev
}

// promptView is a TextView that carries a paste handler. tview.TextView
// defines no PasteHandler (it always returns nil), so a bracketed paste
// would otherwise be dropped before it reaches the prompt's input
// capture. Embedding the TextView and overriding PasteHandler lets the
// live prompt card receive pasted text while keeping every other
// TextView behaviour intact.
type promptView struct {
	*tview.TextView
	st       *promptState
	guard    *promptGuard
	burst    keyBurst
	finished func() bool // card already answered / unmounted
	redraw   func()
}

func newPromptView(tv *tview.TextView, st *promptState) *promptView {
	v := &promptView{TextView: tv, st: st, guard: &promptGuard{}}
	tv.SetFocusFunc(v.guard.arm)
	return v
}

// paste routes pasted text into the card's state machine, honouring
// the same rules as keys: nothing once finished, nothing unless the
// card holds focus, and nothing inside the focus guard window.
func (v *promptView) paste(text string) {
	if v.st == nil || (v.finished != nil && v.finished()) || !v.HasFocus() || v.guard.settling() {
		return
	}
	if v.st.paste(text) && v.redraw != nil {
		v.redraw()
	}
}

func (v *promptView) PasteHandler() func(string, func(tview.Primitive)) {
	return v.WrapPasteHandler(func(text string, _ func(tview.Primitive)) { v.paste(text) })
}

// runPrompt mounts st as a card at the bottom of the Conversation
// panel (aligned with the transcript text), takes focus, and blocks (on
// the caller's goroutine, never the UI one) until the user finishes or
// ctx is cancelled. The outcome distinguishes a dismissal (Esc), a
// steering interruption and a cancelled turn.
func (a *App) runPrompt(ctx context.Context, st *promptState) (tools.PromptAnswer, promptOutcome, error) {
	cancelled := tools.PromptAnswer{Index: -1, Cancelled: true}
	if a.tv == nil || a.transcriptPanel == nil {
		return cancelled, outcomeCancelled, fmt.Errorf("interactive prompt unavailable")
	}
	if a.onUIGoroutine() {
		// Waiting for an answer here would block the very loop that
		// delivers the keystrokes. Fail fast instead of freezing.
		return cancelled, outcomeCancelled, fmt.Errorf("interactive prompt requested on the UI goroutine")
	}
	result := make(chan tools.PromptAnswer, 1)
	tv := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	tv.SetBackgroundColor(a.palette.BgRoot)
	// Same horizontal padding as the transcript body, so the live card
	// sits exactly where its answered copy will be printed.
	// One blank row above and below the live card so it does not run
	// into the transcript text above or the input below.
	tv.SetBorderPadding(1, 1, 3, 2)
	view := newPromptView(tv, st)
	var finished bool
	unmount := func() {
		hadFocus := a.tv.GetFocus() == view
		a.transcriptPanel.RemoveItem(view)
		if a.livePrompt != nil && a.livePrompt.view == view {
			a.livePrompt = nil
		}
		// Only move focus if the card still owned it; a modal or
		// panel the user opened meanwhile keeps its focus.
		if hadFocus {
			a.restoreDefaultFocus()
		}
	}
	lp := &livePrompt{view: view, textEntry: func() bool { return st.secret || st.editing }}
	lp.redraw = func() {
		lp.width = innerWidth(a.transcript)
		text := st.render(a.palette, lp.width)
		view.SetText(text)
		a.transcriptPanel.ResizeItem(view, strings.Count(text, "\n")+2, 0)
	}
	view.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if finished {
			return nil
		}
		if ev = view.burstFilter(ev); ev == nil {
			return nil
		}
		if ev.Key() == tcell.KeyRune || ev.Key() == tcell.KeyCtrlU {
			st.notice = ""
		}
		done, ans := st.handleKey(ev)
		if done {
			finished = true
			// Keys still arriving in this burst belong to the card,
			// not the chat input that is about to take focus.
			a.swallowBurstAfter(view.burst.last)
			unmount()
			result <- ans
			return nil
		}
		lp.redraw()
		return nil
	})
	// A bracketed paste is delivered to the focused primitive's
	// PasteHandler, not its InputHandler. Route it into the same state
	// machine (view.paste applies the focus guard) so secret prompts
	// accept a pasted key and free-text prompts accept pasted answers.
	// Both closures run on the UI goroutine only.
	view.finished = func() bool { return finished }
	view.redraw = lp.redraw
	a.postUI(func() {
		if finished {
			return
		}
		a.transcriptPanel.AddItem(view, 3, 0, true)
		a.livePrompt = lp
		lp.redraw()
		a.transcript.ScrollToEnd()
		// Take focus only when the card is on screen. With a modal,
		// fullscreen or /about up, the card waits; closing those
		// routes focus here through restoreDefaultFocus.
		if a.liveCardView() != nil {
			a.tv.SetFocus(view)
		}
	})
	select {
	case ans := <-result:
		if ans.Cancelled {
			return ans, outcomeDismissed, nil
		}
		return ans, outcomeAnswered, nil
	case <-ctx.Done():
		a.postUI(func() {
			if !finished {
				finished = true
				st.zeroSecret()
				unmount()
			}
		})
		how := outcomeCancelled
		err := ctx.Err()
		if errors.Is(context.Cause(ctx), tools.ErrSteered) {
			how, err = outcomeInterrupted, tools.ErrSteered
		}
		return cancelled, how, err
	}
}

// swallowBurstAfter drops keys that continue a burst ending at t (gaps
// under burstGap, at most burstSwallow in total), so the tail of a
// keystroke paste that finished a card never reaches the chat input.
// UI goroutine only.
func (a *App) swallowBurstAfter(t time.Time) {
	a.burstTail = keyBurst{last: t}
	a.burstTailUntil = t.Add(burstSwallow)
}

// swallowBurstTail reports (and records) a key that continues the burst
// that just finished a card. Called from globalKeys. UI goroutine only.
func (a *App) swallowBurstTail(ev *tcell.EventKey) bool {
	if a.burstTailUntil.IsZero() {
		return false
	}
	if !ev.When().Before(a.burstTailUntil) || !a.burstTail.step(ev.When()) {
		a.burstTailUntil = time.Time{}
		return false
	}
	return true
}

// promptArmDelay is how long a freshly focused card ignores keys and
// pastes (everything except Esc). Tests set it to 0.
var promptArmDelay = 300 * time.Millisecond

// livePrompt is the card currently mounted under the transcript.
type livePrompt struct {
	view      *promptView
	width     int
	redraw    func()
	queued    bool
	textEntry func() bool // card is taking typed text (Other… or secret)
}

// liveCardView returns the mounted card when it is actually on screen
// (no modal pane, fullscreen or /about hiding the Conversation panel),
// else nil. UI goroutine only.
func (a *App) liveCardView() *promptView {
	if a.livePrompt == nil || a.leftPane != nil || a.fullscreen || a.aboutMode {
		return nil
	}
	return a.livePrompt.view
}

// restoreDefaultFocus is the one place focus goes back to after a
// modal, menu, panel switch or layout rebuild: the question card when
// one is mounted and visible, otherwise the chat input. UI goroutine
// only; never blocks.
func (a *App) restoreDefaultFocus() {
	if a.tv == nil {
		return
	}
	if v := a.liveCardView(); v != nil {
		a.tv.SetFocus(v)
		return
	}
	if a.input != nil {
		a.tv.SetFocus(a.input)
	}
}

// checkPromptWidth runs after every draw and re-boxes the live card
// when the transcript width changed (terminal resize). Queued, never
// run inside the draw pass.
func (a *App) checkPromptWidth() {
	lp := a.livePrompt
	if lp == nil || lp.queued || a.transcript == nil {
		return
	}
	if w := innerWidth(a.transcript); w > 0 && w != lp.width {
		lp.queued = true
		a.enqueueUI(func() {
			lp.queued = false
			if a.livePrompt == lp {
				lp.redraw()
			}
		})
	}
}

// recordCard prints the answered (collapsed) card into the transcript
// and a matching activity line. UI-goroutine safe via postUI.
func (a *App) recordCard(req tools.PromptRequest, ans tools.PromptAnswer, how promptOutcome) {
	if a.tv == nil || a.transcript == nil {
		return
	}
	a.postUI(func() {
		card := renderCard(a.palette.Theme, answeredContent(a.palette.Theme, req, ans, how), innerWidth(a.transcript), cardAccentFor(req))
		// One blank line above and below so the card never runs into the
		// previous or next transcript line.
		fmt.Fprintf(a.transcript, "\n\n%s\n\n", card)
		a.transcript.ScrollToEnd()
		if a.activity != nil {
			q := req.Question
			if req.Detail != "" {
				q = req.Detail
			}
			th := a.palette.Theme
			var mark string
			switch how {
			case outcomeAnswered:
				mark = th.OK(ans.Text())
			case outcomeDismissed:
				if req.Kind == tools.PromptAuthorization {
					mark = th.Err("denied")
				} else {
					mark = th.Info("dismissed")
				}
			case outcomeInterrupted:
				mark = th.Info("interrupted")
			default:
				mark = th.Info("cancelled")
			}
			a.appendActivity(fmt.Sprintf("[%s::b]%s[-:-:-] %s → %s", th.Primary, tview.Escape(strings.ToLower(promptHeading(req))), tview.Escape(q), mark))
		}
	})
}

// Ask implements tools.Prompter. Every prompt (sudo, update, ask_user)
// uses the same card and leaves the same answered card behind.
func (a *App) Ask(ctx context.Context, req tools.PromptRequest) (tools.PromptAnswer, error) {
	ans, how, err := a.runPrompt(ctx, newChoicePrompt(req))
	if a.transcript != nil && (err == nil || how == outcomeInterrupted || ctx.Err() != nil) {
		a.recordCard(req, ans, how)
	}
	return ans, err
}

// AskSecret implements tools.Prompter: a masked single-line input whose
// bytes go straight back to the caller. Nothing is echoed to the
// transcript, history, or session.
func (a *App) AskSecret(ctx context.Context, title, label string) ([]byte, error) {
	st := newSecretPrompt(title, label)
	ans, _, err := a.runPrompt(ctx, st)
	if err != nil || ans.Cancelled {
		st.zeroSecret()
		return nil, err
	}
	out := st.buf
	st.buf = nil
	if out == nil {
		out = []byte{}
	}
	return out, nil
}

// Record implements tools.Prompter: a dim one-line transcript note.
// Safe from any goroutine: on the UI event loop (slash commands, key
// and form handlers) it writes inline; elsewhere it is queued in order
// without blocking. It must never call tv.QueueUpdate* directly — that
// blocks until the event loop runs it and deadlocks when the caller is
// the event loop (the /telemetry freeze).
func (a *App) Record(text string) {
	if a.tv == nil || a.transcript == nil {
		return
	}
	a.postUI(func() {
		fmt.Fprintf(a.transcript, "\n[%s]◆ %s[-]\n", a.palette.HexDim, tview.Escape(text))
		a.transcript.ScrollToEnd()
		if a.activity != nil {
			a.appendActivity(tview.Escape(text))
		}
	})
}
