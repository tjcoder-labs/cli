package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
			if cap(s.buf)-len(s.buf) < n {
				// Grow manually so the old backing array can be wiped
				// rather than left for the GC with secret bytes in it.
				nb := make([]byte, len(s.buf), 2*cap(s.buf)+n+16)
				copy(nb, s.buf)
				s.zeroSecret()
				s.buf = nb
			}
			s.buf = append(s.buf, tmp[:n]...)
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
	default:
		return "QUESTION"
	}
}

// cardHeader is the shared top of every prompt card: heading,
// question and detail.
func cardHeader(th theme.Theme, req tools.PromptRequest, secret bool) []render.Line {
	head := render.Line{{Text: promptHeading(req), Fg: th.Primary, Attr: "b"}}
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

// content returns the live card's rows (before boxing).
func (s *promptState) content(th theme.Theme) []render.Line {
	out := cardHeader(th, s.req, s.secret)
	out = append(out, nil)
	cursor := render.Span{Text: "▏", Fg: th.Primary}
	if s.secret {
		out = append(out,
			render.Line{{Text: s.label, Fg: th.Text}},
			render.Line{{Text: "› ", Fg: th.Bright, Attr: "b"}, {Text: strings.Repeat("•", utf8.RuneCount(s.buf)), Fg: th.Text}, cursor},
			nil,
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
	out = append(out, nil, render.Line{{Text: s.hint(), Fg: th.Faint}})
	return out
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
	return renderCard(p.Theme, s.content(p.Theme), width)
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
	view := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	view.SetBackgroundColor(a.palette.BgRoot)
	// Same horizontal padding as the transcript body, so the live card
	// sits exactly where its answered copy will be printed.
	view.SetBorderPadding(0, 1, 3, 2)
	var prevFocus tview.Primitive
	var finished bool
	unmount := func() {
		a.transcriptPanel.RemoveItem(view)
		if a.livePrompt != nil && a.livePrompt.view == view {
			a.livePrompt = nil
		}
		if prevFocus != nil {
			a.tv.SetFocus(prevFocus)
		}
	}
	lp := &livePrompt{view: view}
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
		done, ans := st.handleKey(ev)
		if done {
			finished = true
			unmount()
			result <- ans
			return nil
		}
		lp.redraw()
		return nil
	})
	a.postUI(func() {
		prevFocus = a.tv.GetFocus()
		a.transcriptPanel.AddItem(view, 3, 0, true)
		a.livePrompt = lp
		lp.redraw()
		a.transcript.ScrollToEnd()
		a.tv.SetFocus(view)
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

// livePrompt is the card currently mounted under the transcript.
type livePrompt struct {
	view   *tview.TextView
	width  int
	redraw func()
	queued bool
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
		card := renderCard(a.palette.Theme, answeredContent(a.palette.Theme, req, ans, how), innerWidth(a.transcript))
		fmt.Fprintf(a.transcript, "\n%s\n", card)
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
