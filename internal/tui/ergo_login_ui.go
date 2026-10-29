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

// loginCodeView renders the waiting screen: the code is the hero (Ergo
// purple, boxed), with the URL and the email hint under it.
func loginCodeView(pal Palette, email string, st *auth.DeviceStart, remaining time.Duration, opened bool) string {
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
	return fmt.Sprintf(
		"\n  Your sign-in code:\n\n%s\n\n"+
			"      [%s]Code  [-][%s::b]%s[-:-:-]\n\n"+
			"  1. Open the sign-in link we emailed to [::b]%s[-:-:-]\n"+
			"  2. Enter the code above to approve this device\n\n"+
			"  Info page: %s%s\n\n"+
			"  [%s]Waiting for approval…  (expires in %d:%02d — Esc to cancel)[-]\n",
		codeBoxes(pal, st.UserCode),
		pal.HexDim, pal.HexPurple, st.UserCode,
		tview.Escape(email), tview.Escape(uri), openedNote,
		pal.HexDim, mins, secs)
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
			Next:     "Ask your admin to enable sign-ups or add you to the allowlist, or run /login with a different email.",
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
			Next:     "Take a short break, then run /login again " + wait + ".",
		}
	case errors.Is(err, auth.ErrExpired):
		return loginFailure{
			Headline: "That sign-in code expired.",
			Next:     "Run /login to get a fresh code (they last about 15 minutes).",
		}
	case errors.Is(err, auth.ErrDenied):
		return loginFailure{
			Headline: "The sign-in request was declined in the browser.",
			Next:     "If that wasn't you, nothing was signed in. To try again, run /login.",
		}
	case errors.As(err, &netErr), strings.Contains(msg, "could not reach"), strings.Contains(msg, "lost contact"):
		return loginFailure{
			Headline: "Can't reach ergo right now.",
			Detail:   msg,
			Next:     "Check your internet connection (and ERGO_BASE_URL if you set it), then run /login again.",
		}
	}
	return loginFailure{
		Headline: "Sign-in didn't go through.",
		Detail:   msg,
		Next:     "Run /login to try again. If it keeps happening, check https://dashboard.tjcoder.com.",
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

// loginSuccessView renders the post-login confirmation card.
func loginSuccessView(pal Palette, email string, welcomeGranted bool, balanceCents int64, agents []string, selected, store string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  [%s::b]✓ You're signed in[-:-:-]  [%s]%s[-]\n", pal.HexPurple, pal.HexDim, tview.Escape(email))
	credit := fmt.Sprintf("Balance: %s", formatCents(balanceCents))
	if welcomeGranted {
		credit = "$5.00 welcome credit added"
	}
	fmt.Fprintf(&b, "  [%s]◆[-] %s\n", pal.HexPurple, credit)
	fmt.Fprintf(&b, "  [%s]Key stored in %s[-]\n\n", pal.HexDim, tview.Escape(store))
	fmt.Fprintf(&b, "  [%s::b]Your agents[-:-:-]\n", pal.HexLavender)
	for _, name := range agents {
		marker, style := "  ", fmt.Sprintf("[%s]", pal.HexDim)
		if name == selected {
			marker, style = "● ", fmt.Sprintf("[%s::b]", pal.HexPurple)
		}
		fmt.Fprintf(&b, "  %s%s%-12s[-:-:-]", style, marker, name)
		if d := client.ErgoAgentDesc(name); d != "" {
			fmt.Fprintf(&b, " [%s]%s[-]", pal.HexDim, d)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n  [%s]Type a message to start  ·  /model switch agent  ·  /balance  ·  /logout[-]\n", pal.HexDim)
	return b.String()
}

// showLoginResult replaces the sign-in modal with a result card and one
// button. onClose (optional) runs after the modal closes.
func (a *App) showLoginResult(title, text, button string, onClose func()) {
	view := tview.NewTextView().SetDynamicColors(true)
	view.SetBackgroundColor(a.palette.BgInput)
	view.SetTextColor(a.palette.TextMain)
	view.SetText(text)
	closeFn := func() {
		a.closeModal()
		if onClose != nil {
			onClose()
		}
	}
	btns := a.newModalForm(closeFn)
	btns.AddButton(button, closeFn)
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
