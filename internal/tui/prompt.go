package tui

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// promptState is the keyboard state machine behind the in-transcript
// prompt component. It is UI-free so it can be unit-tested directly:
//
//   - 1-9 picks an option immediately
//   - Up/Down (or k/j) move the cursor, Enter confirms it
//   - Esc cancels (an authorization prompt treats that as deny)
//   - with AllowOther, the trailing "Other…" row opens a free-text line
//   - in secret mode the typed bytes are kept in a []byte, rendered as
//     bullets, and never stored anywhere else
type promptState struct {
	req      tools.PromptRequest
	selected int
	editing  bool   // typing into the "Other…" line
	other    []rune // free-text answer
	secret   bool   // masked single-line input (passwords)
	label    string
	buf      []byte // secret bytes; zeroed by the consumer / on cancel
}

func newChoicePrompt(req tools.PromptRequest) *promptState {
	return &promptState{req: req}
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
			s.editing = false
		case tcell.KeyEnter:
			text := strings.TrimSpace(string(s.other))
			if text != "" {
				return true, tools.PromptAnswer{Index: -1, Other: text}
			}
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(s.other) > 0 {
				s.other = s.other[:len(s.other)-1]
			}
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
		if n > 0 {
			s.selected = (s.selected - 1 + n) % n
		}
	case tcell.KeyDown, tcell.KeyTab:
		if n > 0 {
			s.selected = (s.selected + 1) % n
		}
	case tcell.KeyEnter:
		return s.choose(s.selected)
	case tcell.KeyRune:
		r := ev.Rune()
		switch {
		case r >= '1' && r <= '9':
			if i := int(r - '1'); i < n {
				s.selected = i
				return s.choose(i)
			}
		case r == 'k':
			if n > 0 {
				s.selected = (s.selected - 1 + n) % n
			}
		case r == 'j':
			if n > 0 {
				s.selected = (s.selected + 1) % n
			}
		}
	}
	return false, tools.PromptAnswer{}
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

// render returns tview-tagged text for the component.
func (s *promptState) render(p Palette) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s::b]%s[-:-:-]\n", p.HexPurple, promptHeading(s.req))
	if s.secret {
		fmt.Fprintf(&b, "[%s]%s[-]\n", p.HexMain, tview.Escape(s.label))
		fmt.Fprintf(&b, "[%s]›[-] [%s]%s[-][%s]▏[-]\n", p.HexLavender, p.HexMain,
			strings.Repeat("•", utf8.RuneCount(s.buf)), p.HexPurple)
		fmt.Fprintf(&b, "[%s]Enter to submit · Esc to cancel · input is hidden and never sent to the model[-]", p.HexFaint)
		return b.String()
	}
	if s.req.Question != "" {
		fmt.Fprintf(&b, "[%s]%s[-]\n", p.HexMain, tview.Escape(s.req.Question))
	}
	if s.req.Detail != "" {
		fmt.Fprintf(&b, "[%s]  $ %s[-]\n", p.HexLavender, tview.Escape(s.req.Detail))
	}
	for i := 0; i < s.rows(); i++ {
		label := "Other…"
		if !s.isOtherRow(i) {
			label = s.req.Options[i]
		}
		num := ""
		if i < 9 {
			num = fmt.Sprintf("%d.", i+1)
		}
		if i == s.selected {
			fmt.Fprintf(&b, "[%s::b]› %s %s[-:-:-]\n", p.HexLavender, num, tview.Escape(label))
		} else {
			fmt.Fprintf(&b, "[%s]  %s %s[-]\n", p.HexDim, num, tview.Escape(label))
		}
		if s.isOtherRow(i) && s.editing {
			fmt.Fprintf(&b, "    [%s]%s[-][%s]▏[-]\n", p.HexMain, tview.Escape(string(s.other)), p.HexPurple)
		}
	}
	hint := "1-9 or ↑↓ Enter to choose · Esc to cancel"
	if s.req.Kind == tools.PromptAuthorization {
		hint = "1-9 or ↑↓ Enter to choose · Esc to deny"
	}
	if s.editing {
		hint = "type your answer · Enter to send · Esc to go back"
	}
	fmt.Fprintf(&b, "[%s]%s[-]", p.HexFaint, hint)
	return b.String()
}

// --- App as tools.Prompter -------------------------------------------------

var _ tools.Prompter = (*App)(nil)

// runPrompt mounts st at the bottom of the Conversation panel, takes
// focus, and blocks (on the caller's goroutine, never the UI one) until
// the user finishes or ctx is cancelled.
func (a *App) runPrompt(ctx context.Context, st *promptState) (tools.PromptAnswer, error) {
	if a.tv == nil || a.transcriptPanel == nil {
		return tools.PromptAnswer{Index: -1, Cancelled: true}, fmt.Errorf("interactive prompt unavailable")
	}
	if a.onUIGoroutine() {
		// Waiting for an answer here would block the very loop that
		// delivers the keystrokes. Fail fast instead of freezing.
		return tools.PromptAnswer{Index: -1, Cancelled: true}, fmt.Errorf("interactive prompt requested on the UI goroutine")
	}
	result := make(chan tools.PromptAnswer, 1)
	view := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	view.SetBackgroundColor(a.palette.BgInput)
	view.SetBorderPadding(1, 1, 3, 2)
	var prevFocus tview.Primitive
	var finished bool
	unmount := func() {
		a.transcriptPanel.RemoveItem(view)
		if prevFocus != nil {
			a.tv.SetFocus(prevFocus)
		}
	}
	redraw := func() {
		text := st.render(a.palette)
		view.SetText(text)
		a.transcriptPanel.ResizeItem(view, strings.Count(text, "\n")+3, 0)
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
		redraw()
		return nil
	})
	a.postUI(func() {
		prevFocus = a.tv.GetFocus()
		a.transcriptPanel.AddItem(view, 3, 0, true)
		redraw()
		a.transcript.ScrollToEnd()
		a.tv.SetFocus(view)
	})
	select {
	case ans := <-result:
		return ans, nil
	case <-ctx.Done():
		a.postUI(func() {
			if !finished {
				finished = true
				st.zeroSecret()
				unmount()
			}
		})
		return tools.PromptAnswer{Index: -1, Cancelled: true}, ctx.Err()
	}
}

// Ask implements tools.Prompter.
func (a *App) Ask(ctx context.Context, req tools.PromptRequest) (tools.PromptAnswer, error) {
	ans, err := a.runPrompt(ctx, newChoicePrompt(req))
	if err == nil {
		q := req.Question
		if req.Detail != "" {
			q = req.Detail
		}
		if req.Title != "sudo" { // the sudo gate records its own decision
			a.Record(fmt.Sprintf("%s → %s", q, ans.Text()))
		}
	}
	return ans, err
}

// AskSecret implements tools.Prompter: a masked single-line input whose
// bytes go straight back to the caller. Nothing is echoed to the
// transcript, history, or session.
func (a *App) AskSecret(ctx context.Context, title, label string) ([]byte, error) {
	st := newSecretPrompt(title, label)
	ans, err := a.runPrompt(ctx, st)
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
