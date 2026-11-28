package tui

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
)

// codeBoxes renders a XXXX-XXXX device code as spaced boxed characters, in
// three lines. Box-drawing glyphs and bold keep it legible without colour
// (NO_COLOR terminals drop the hues but not the structure).
func codeBoxes(pal Palette, code string) string {
	groups := strings.Split(strings.ToUpper(strings.TrimSpace(code)), "-")
	dim := func(s string) string { return fmt.Sprintf("[%s]%s[-]", pal.HexDim, s) }
	var top, mid, bot []string
	for _, g := range groups {
		runes := []rune(g)
		if len(runes) == 0 {
			continue
		}
		t, m, b := "┌", dim("│"), "└"
		for i, r := range runes {
			end := i == len(runes)-1
			sep, bsep := "┬", "┴"
			if end {
				sep, bsep = "┐", "┘"
			}
			t += "───" + sep
			b += "───" + bsep
			m += fmt.Sprintf(" [%s::b]%c[-:-:-] %s", pal.HexPurple, r, dim("│"))
		}
		top = append(top, dim(t))
		mid = append(mid, m)
		bot = append(bot, dim(b))
	}
	const indent = "      "
	return indent + strings.Join(top, "   ") + "\n" +
		indent + strings.Join(mid, dim(" – ")) + "\n" +
		indent + strings.Join(bot, "   ")
}

// Below these pane sizes (Termux, split panes, short terminals) the sign-in
// views switch to a compact layout: the boxed code (43 cells wide, 17 rows
// of screen in all) would wrap, tear, or push the buttons off screen.
const (
	compactLoginWidth  = 50
	compactLoginHeight = 20
)

// paneSize is the size of the area a modal text view was last drawn at,
// or a comfortable 80x30 before the first draw.
func paneSize(v *tview.TextView) (w, h int) {
	if v != nil {
		if _, _, w, h = v.GetInnerRect(); w > 0 && h > 0 {
			return w, h
		}
	}
	return 80, 30
}

// loginCodeView renders the waiting screen at a comfortable size.
func loginCodeView(pal Palette, email string, st *auth.DeviceStart, remaining time.Duration, opened bool) string {
	return loginCodeViewW(pal, 80, 30, email, st, remaining, opened)
}

// loginCodeViewW renders the waiting screen: the code is the hero (Ergo
// purple, boxed), with the approval link and the countdown under it. In a
// small pane the boxes and spacer rows give way to a tight layout that
// keeps the email, the code, and the countdown on screen.
func loginCodeViewW(pal Palette, width, height int, email string, st *auth.DeviceStart, remaining time.Duration, opened bool) string {
	uri := st.VerificationURIComplete
	if uri == "" {
		uri = st.VerificationURI
	}
	openedNote := ""
	if opened {
		openedNote = " (opened in your browser)"
	}
	if remaining < 0 {
		remaining = 0
	}
	mins := int(remaining.Minutes())
	secs := int(remaining.Seconds()) % 60
	var b strings.Builder
	if (width > 0 && width < compactLoginWidth) || (height > 0 && height < compactLoginHeight) {
		fmt.Fprintf(&b, "  [::b]Check your email[-:-:-] — we sent a sign-in link to [::b]%s[-:-:-].\n", tview.Escape(email))
		fmt.Fprintf(&b, "  Enter this code to approve: [%s::b]%s[-:-:-]\n", pal.HexPurple, st.UserCode)
		fmt.Fprintf(&b, "  Or open: %s%s\n", tview.Escape(uri), openedNote)
		fmt.Fprintf(&b, "  [%s]Waiting… expires in %d:%02d · Esc to cancel[-]\n", pal.HexDim, mins, secs)
		return b.String()
	}
	fmt.Fprintf(&b, "\n  [::b]Check your email[-:-:-]\n\n")
	fmt.Fprintf(&b, "  We sent a sign-in link to [::b]%s[-:-:-].\n", tview.Escape(email))
	b.WriteString("  Open it and enter this code to approve this device:\n\n")
	fmt.Fprintf(&b, "%s\n\n", codeBoxes(pal, st.UserCode))
	fmt.Fprintf(&b, "      [%s]Code  [-][%s::b]%s[-:-:-]\n\n", pal.HexDim, pal.HexPurple, st.UserCode)
	fmt.Fprintf(&b, "  Or open the approval page yourself:\n  %s%s\n\n", tview.Escape(uri), openedNote)
	fmt.Fprintf(&b, "  [%s]Waiting for approval…  expires in %d:%02d · Esc to cancel[-]\n", pal.HexDim, mins, secs)
	return b.String()
}

// loginFailure is a friendly, actionable sign-in error.
type loginFailure struct {
	Headline string // one sentence: what happened
	Detail   string // optional extra context (server message)
	Next     string // what to do now
}

// friendlyLoginError maps device-flow errors to plain-language copy and a next step.
func friendlyLoginError(err error, email string) loginFailure {
	var rl *auth.RateLimitedError
	var netErr net.Error
	msg := err.Error()
	switch {
	case errors.Is(err, auth.ErrSignupClosed):
		f := loginFailure{
			Headline: fmt.Sprintf("Sign-ups are closed for %s.", email),
			Next:     "Try the email you signed up with, or use a different provider.",
		}
		if i := strings.Index(msg, ": "); i >= 0 && strings.Contains(msg, auth.ErrSignupClosed.Error()) {
			f.Detail = strings.TrimSpace(msg[i+2:])
		}
		return f
	case errors.As(err, &rl):
		wait := "in a few minutes"
		if rl.RetryAfter > 0 {
			wait = "in " + rl.RetryAfter.Round(time.Second).String()
		}
		return loginFailure{
			Headline: "Too many sign-in attempts.",
			Next:     "Take a short break, then choose Try again " + wait + ".",
		}
	case errors.Is(err, auth.ErrExpired):
		return loginFailure{
			Headline: "That sign-in code expired.",
			Next:     "Codes last about 15 minutes. Choose Try again to get a fresh one.",
		}
	case errors.Is(err, auth.ErrDenied):
		return loginFailure{
			Headline: "The sign-in request was declined in your browser.",
			Next:     "If that wasn't you, nothing was signed in. To start over, choose Try again.",
		}
	case errors.As(err, &netErr), strings.Contains(msg, "could not reach"), strings.Contains(msg, "lost contact"):
		return loginFailure{
			Headline: "Can't reach TJ Coder AI Labs right now.",
			Detail:   msg,
			Next:     "Check your internet connection (and ERGO_BASE_URL if you set it), then choose Try again.",
		}
	}
	return loginFailure{
		Headline: "Sign-in didn't go through.",
		Detail:   msg,
		Next:     "Choose Try again. If it keeps happening, check https://dashboard.tjcoder.com.",
	}
}

// loginFailureView renders a friendly failure card.
func loginFailureView(pal Palette, f loginFailure) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  [%s::b]✗ %s[-:-:-]\n", pal.HexAlert, tview.Escape(f.Headline))
	if f.Detail != "" {
		fmt.Fprintf(&b, "\n  [%s]%s[-]\n", pal.HexDim, tview.Escape(f.Detail))
	}
	fmt.Fprintf(&b, "\n  [%s::b]Next:[-:-:-] %s\n", pal.HexPurple, tview.Escape(f.Next))
	return b.String()
}

// loginSuccessView renders the post-login confirmation card at a
// comfortable width.
func loginSuccessView(pal Palette, email string, welcomeGranted bool, balanceCents int64, agents []string, selected, store string) string {
	return loginSuccessViewW(pal, 80, 30, email, welcomeGranted, balanceCents, agents, selected, store)
}

// loginSuccessViewW renders the post-login confirmation card. Agent
// names share one column so the descriptions line up; on narrow panes
// the descriptions are dropped rather than wrapped out of alignment, and
// on short panes the spacer rows and the key-storage line go too.
func loginSuccessViewW(pal Palette, width, height int, email string, welcomeGranted bool, balanceCents int64, agents []string, selected, store string) string {
	short := height > 0 && height < compactLoginHeight
	gap := "\n"
	if short {
		gap = ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  [%s::b]✓ You're signed in[-:-:-]  [%s]%s[-]\n", gap, pal.HexPurple, pal.HexDim, tview.Escape(email))
	credit := fmt.Sprintf("Balance: %s", formatCents(balanceCents))
	if welcomeGranted {
		credit = "$5.00 welcome credit added"
	}
	fmt.Fprintf(&b, "  [%s]◆[-] %s\n", pal.HexPurple, credit)
	if !short {
		fmt.Fprintf(&b, "  [%s]Key stored in %s[-]\n", pal.HexDim, tview.Escape(store))
	}
	b.WriteString(gap)
	if short {
		// A short pane keeps the account, the credit, and the agent in use;
		// the list and the footer are a /model and /login away.
		fmt.Fprintf(&b, "  [%s]Agent:[-] [%s::b]%s[-:-:-] [%s]· /model to switch[-]\n", pal.HexLavender, pal.HexPurple, tview.Escape(selected), pal.HexDim)
		return b.String()
	}
	fmt.Fprintf(&b, "  [%s::b]Your agents[-:-:-]\n", pal.HexLavender)
	col := 0
	for _, name := range agents {
		if n := len([]rune(name)); n > col {
			col = n
		}
	}
	showDesc := width <= 0 || width >= 60
	for _, name := range agents {
		marker, style := "  ", fmt.Sprintf("[%s]", pal.HexDim)
		if name == selected {
			marker, style = "● ", fmt.Sprintf("[%s::b]", pal.HexPurple)
		}
		fmt.Fprintf(&b, "  %s%s%-*s[-:-:-]", style, marker, col, name)
		if d := client.ErgoAgentDesc(name); d != "" && showDesc {
			fmt.Fprintf(&b, "  [%s]%s[-]", pal.HexDim, d)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n  [%s]Type a message to start  ·  /model switch agent  ·  /balance  ·  /login account menu[-]\n", pal.HexDim)
	return b.String()
}

// resultButton is one button on a sign-in result screen. Its Fn runs after
// the modal has closed.
type resultButton struct {
	Label string
	Fn    func()
}

// showLoginResult replaces the sign-in modal with a result card and one
// or more buttons. Esc closes the modal without running any of them.
func (a *App) showLoginResult(title, text string, buttons ...resultButton) {
	view := tview.NewTextView().SetDynamicColors(true)
	view.SetBackgroundColor(a.palette.BgInput)
	view.SetTextColor(a.palette.TextMain)
	view.SetText(text)
	btns := a.newModalForm(nil)
	for _, bt := range buttons {
		bt := bt
		btns.AddButton(bt.Label, func() {
			a.closeModal()
			if bt.Fn != nil {
				bt.Fn()
			}
		})
	}
	panel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(view, 0, 1, false).
		AddItem(btns, 3, 0, true)
	panel.SetBackgroundColor(a.palette.BgInput)
	a.showModal(title, panel)
}

// afterErgoLogin reconciles the model with the gateway after the provider
// has just been swapped to a signed-in ergo client: an Ollama tag (or any
// value the gateway doesn't serve) is replaced by the remembered ergo pick
// or the default, and the live /v1/models list is fetched in the background.
func (a *App) afterErgoLogin() {
	saved := savedErgoModel()
	if !a.modelChosen || !client.ErgoModelAllowed(a.currentModel, a.ergoAvailable()) {
		a.currentModel = client.ResolveErgoModel("", saved, a.ergoAvailable())
	}
	a.models = nil
	a.loadModelsAsync()
	a.refreshFooter()
}
