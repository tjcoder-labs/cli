package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tjcoder-labs/cli/internal/theme"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
)

// ergoAuthTimeout bounds every ergo network probe the TUI fires (key
// validation, balance refresh).
const ergoAuthTimeout = 12 * time.Second

// ergoLoginPrompt returns a prominent sign-in notice when the active provider
// is tjcoder/ergo but no credentials are configured, and "" otherwise.
// tjcoder is the default provider, so we point the user at /login (which
// also offers other providers) instead of silently falling back to ollama.
func ergoLoginPrompt(p client.Provider) string {
	if e, ok := p.(*client.Ergo); ok && !e.Authenticated() {
		return theme.Current().Warn("You're not signed in to TJ Coder.") + " Run [::b]/login[-:-:-] to sign in or pick another provider (or start with --provider ollama to use a local model)."
	}
	return ""
}

// openBrowser is swapped in tests; it must never block the UI.
var openBrowser = defaultOpenBrowser

// defaultOpenBrowser best-effort opens an http(s) URL in the user's browser.
// Failures are ignored by callers — the URL is always printed as well.
func defaultOpenBrowser(url string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return fmt.Errorf("refusing to open non-http URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// plausibleEmail is a cheap client-side sanity check; the server validates properly.
func plausibleEmail(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && at < len(s)-3 && strings.Contains(s[at:], ".") && !strings.ContainsAny(s, " \t<>\"'")
}

// deviceClient builds the device-code client for the configured gateway.
func deviceClient() *auth.DeviceClient {
	return &auth.DeviceClient{BaseURL: ergoBaseURL(), ClientName: "ergo-cli"}
}

// signInIntro is the copy above the email field.
const signInIntro = "  Enter your email — we'll send a link, no password needed.\n" +
	"  New here? Your account is created automatically, with $5 credit."

// handleLoginCommand opens the email form that starts the device-code
// sign-in: ask for an email, show the code + URL, and poll until the user
// approves in a browser. The scoped ergo-cli key is stored in the OS
// keychain (0600 file fallback) and never echoed to the transcript.
// /login itself opens the hub (openLoginHub); this is the form it, the
// provider list, and a switch to an unsigned tjcoder lead to.
func (a *App) handleLoginCommand() { a.openSignInForm("") }

// openSignInForm shows the email form, optionally prefilled (Try again).
func (a *App) openSignInForm(prefill string) {
	if a.loginCancel != nil {
		a.appendActivity("A sign-in is already in progress — press Esc in the sign-in panel to cancel it")
		return
	}
	intro := tview.NewTextView().SetDynamicColors(true)
	intro.SetBackgroundColor(a.palette.BgInput)
	intro.SetTextColor(a.palette.TextMain)
	intro.SetText(signInIntro)
	// A blank row under the heading when the pane has room for it; on a
	// short pane every row goes to the text and the form.
	if a.transcriptPanel != nil {
		if _, _, _, ph := a.transcriptPanel.GetRect(); ph >= 18 {
			intro.SetBorderPadding(1, 0, 0, 0)
		}
	}
	status := tview.NewTextView().SetDynamicColors(true)
	status.SetBackgroundColor(a.palette.BgInput)

	form := a.newModalForm(nil)
	email := strings.TrimSpace(prefill)
	form.AddInputField("Email", email, 0, nil, func(s string) { email = strings.TrimSpace(s) })
	if f, ok := form.GetFormItem(0).(*tview.InputField); ok {
		f.SetPlaceholder("you@example.com")
		f.SetPlaceholderStyle(tcell.StyleDefault.Foreground(a.palette.TextDim).Background(a.palette.BgSelect))
	}
	form.AddButton("Send sign-in link", func() {
		if !plausibleEmail(email) {
			status.SetText(fmt.Sprintf("  [%s::b]✗[-:-:-] Enter a valid email address, like you@example.com", a.palette.HexAlert))
			return
		}
		a.beginDeviceLogin(email)
	})
	form.AddButton("Cancel", func() { a.closeModal() })
	// The form keeps its own fixed height (field + buttons) and the intro
	// takes whatever is left, so on a short pane it is the intro that is
	// clipped, never the field or the buttons.
	panel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(intro, 0, 1, false).
		AddItem(status, 1, 0, false).
		AddItem(form, signInFormRows, 0, true)
	panel.SetBackgroundColor(a.palette.BgInput)
	a.showModal("Sign in", panel)
	a.tv.SetFocus(form)
}

// signInFormRows is the height of the email form: padding, the field, a
// gap, the buttons, padding.
const signInFormRows = 5

func (a *App) beginDeviceLogin(email string) {
	ctx, cancel := context.WithCancel(context.Background())
	a.loginCancel = cancel
	finish := func() {
		cancel()
		a.loginCancel = nil
	}

	view := tview.NewTextView().SetDynamicColors(true)
	view.SetBackgroundColor(a.palette.BgInput)
	view.SetTextColor(a.palette.TextMain)
	view.SetText(fmt.Sprintf("\n  [%s]Contacting TJ Coder AI Labs…[-]", a.palette.HexDim))
	cancelLogin := func() {
		finish()
		a.appendActivity("Sign-in cancelled")
		a.closeModal()
	}
	btns := a.newModalForm(cancelLogin)
	btns.AddButton("Cancel", cancelLogin)
	panel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(view, 0, 1, false).
		AddItem(btns, 3, 0, true)
	panel.SetBackgroundColor(a.palette.BgInput)
	a.showModal("Sign in", panel)

	// Every failure screen offers the same two exits: start over with the
	// same email, or go back to the sign-in options.
	failureButtons := []resultButton{
		{Label: "Try again", Fn: func() { a.openSignInForm(email) }},
		{Label: "Back", Fn: func() { a.openLoginHub(nil, false) }},
	}

	dc := deviceClient()
	go func() {
		fail := func(err error) {
			emit("login_result", map[string]any{"ok": false, "method": "device"})
			a.postUI(func() {
				if ctx.Err() != nil {
					return
				}
				finish()
				f := friendlyLoginError(err, email)
				a.appendActivity(fmt.Sprintf("[%s::b]✗ sign-in failed[-::B]: %s", a.palette.HexAlert, f.Headline))
				a.showLoginResult("Sign in", loginFailureView(a.palette, f), failureButtons...)
			})
		}
		st, err := dc.Start(ctx, email)
		if err != nil {
			if ctx.Err() == nil {
				fail(err)
			}
			return
		}
		uri := st.VerificationURIComplete
		if uri == "" {
			uri = st.VerificationURI
		}
		opened := openBrowser(uri) == nil
		expires := time.Now().Add(time.Duration(st.ExpiresIn) * time.Second)
		a.postUI(func() {
			{
				w, h := paneSize(view)
				view.SetText(loginCodeViewW(a.palette, w, h, email, st, time.Until(expires), opened))
			}
			a.appendActivity("Sign-in code " + st.UserCode + " — check " + email + " for the approval link")
		})
		// live countdown
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					rem := time.Until(expires)
					if rem < 0 {
						return
					}
					a.postUI(func() {
						if ctx.Err() == nil {
							w, h := paneSize(view)
							view.SetText(loginCodeViewW(a.palette, w, h, email, st, rem, opened))
						}
					})
				}
			}
		}()

		res, err := dc.Wait(ctx, st, nil)
		if err != nil {
			if ctx.Err() == nil {
				fail(err)
			}
			return
		}
		saveErr := auth.SaveErgoToken(res.APIKey)
		emit("login_result", map[string]any{"ok": saveErr == nil, "method": "device"})
		a.postUI(func() {
			finish()
			if saveErr != nil {
				a.appendActivity(fmt.Sprintf("[%s::b]✗ sign-in failed[-::B]: could not store the key: %v", a.palette.HexAlert, saveErr))
				f := loginFailure{
					Headline: "Signed in, but the key couldn't be saved.",
					Detail:   saveErr.Error(),
					Next:     "Set ERGO_API_KEY in your environment, or fix the permissions on your config directory, then choose Try again.",
				}
				a.showLoginResult("Sign in", loginFailureView(a.palette, f), failureButtons...)
				return
			}
			// Non-secret profile for the per-turn metadata block.
			_ = auth.SaveErgoIdentity(auth.Identity{Email: res.Email, Name: res.Name})
			a.ergoKeyRejected = false
			a.swapErgoProvider(res.APIKey)
			a.setErgoBalanceCents(res.BalanceCents)
			a.afterErgoLogin()
			a.markAccountProviderActive()
			email := res.Email
			if email == "" {
				email = "your account"
			}
			w, h := paneSize(view)
			card := loginSuccessViewW(a.palette, w, h, email, res.WelcomeGranted, res.BalanceCents, a.ergoAvailable(), a.currentModel, tokenStoreLabel())
			a.appendActivity(fmt.Sprintf("[%s]Signed in to TJ Coder as %s[-]", a.palette.HexPurple, tview.Escape(email)))
			// Leave the card in the conversation so it stays visible.
			keepCard := func() { fmt.Fprintf(a.transcript, "\n%s\n", card) }
			buttons := []resultButton{{Label: "Start chatting", Fn: keepCard}}
			text := card
			if offer := a.takeResend(); offer != nil {
				// A message was waiting on this sign-in. Offer it; never
				// send it without the user choosing to.
				text += fmt.Sprintf("\n  [%s]Your message is waiting:[-] %s\n", a.palette.HexLavender, tview.Escape(offer.Preview))
				buttons = []resultButton{
					{Label: "Send my message", Fn: func() { keepCard(); offer.Run() }},
					{Label: "Start chatting", Fn: keepCard},
				}
			}
			a.showLoginResult("You're signed in", text, buttons...)
		})
	}()
}

func tokenStoreLabel() string {
	switch auth.TokenStore() {
	case "keychain":
		return "the OS keychain"
	case "file":
		return auth.ErgoTokenPath()
	case "env":
		return "ERGO_API_KEY"
	}
	return "local storage"
}

// handleLogoutCommand revokes the key server-side, then removes the local
// credential and detaches the client so the next chat surfaces a clean
// auth error. The local credential is cleared even if revocation fails.
func (a *App) handleLogoutCommand() { a.signOutThen(nil) }

// signOutThen is /logout with a continuation: after the credential has
// been cleared (and only then) after runs on the UI goroutine. fullySignedOut
// is false when ERGO_API_KEY still supplies a key, so the caller does not
// pretend the account is gone. The key is resolved off the UI goroutine
// because the keychain lookup can block.
func (a *App) signOutThen(after func(fullySignedOut bool)) {
	if a.loginCancel != nil {
		a.loginCancel()
		a.loginCancel = nil
	}
	a.pendingResend = nil
	go func() {
		key := auth.ResolveErgoToken()
		store := auth.TokenStore()
		var revokeErr error
		if key != "" && store != "env" {
			ctx, cancel := context.WithTimeout(context.Background(), ergoAuthTimeout)
			revokeErr = deviceClient().Logout(ctx, key)
			cancel()
		}
		clearErr := auth.ClearErgoToken()
		a.postUI(func() {
			if clearErr != nil {
				a.appendActivity(fmt.Sprintf(a.palette.Theme.Err("sign-out failed")+": %v", clearErr))
				return
			}
			a.ergoKeyRejected = false
			a.swapErgoProvider("")
			a.clearErgoBalance()
			switch {
			case store == "env":
				a.appendActivity("Signed out locally. ERGO_API_KEY is set in your environment — unset it (the key was NOT revoked server-side)")
			case revokeErr != nil:
				a.appendActivity(fmt.Sprintf("Signed out locally, but the key could not be revoked on the server (%v) — it stays valid until it expires", revokeErr))
			default:
				a.appendActivity("Signed out of TJ Coder (key revoked)")
			}
			if after != nil {
				after(store != "env")
			}
		})
	}()
}

// handleBalanceCommand opens the wallet pane: current balance and a
// numbered top-up list. A digit picks an amount immediately; arrows move
// the highlight and Enter confirms. Esc closes.
func (a *App) handleBalanceCommand() {
	key := auth.ResolveErgoToken()
	if key == "" {
		a.appendActivity("Not signed in to ergo — run /login first")
		return
	}
	header := tview.NewTextView().SetDynamicColors(true)
	header.SetBackgroundColor(a.palette.BgInput)
	header.SetTextColor(a.palette.TextMain)
	header.SetText(fmt.Sprintf("\n  [%s]Loading wallet…[-]", a.palette.HexDim))
	list := tview.NewList()
	a.styleModalList(list)
	list.ShowSecondaryText(true)
	// Esc always closes. tview calls DoneFunc for Esc on a focused list.
	stopPoll := func() {
		if a.balanceCancel != nil {
			a.balanceCancel()
			a.balanceCancel = nil
		}
		a.closeModal()
	}
	list.SetDoneFunc(stopPoll)
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			stopPoll()
			return nil
		}
		return ev
	})
	panel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 6, 0, false).
		AddItem(list, 0, 1, true)
	panel.SetBackgroundColor(a.palette.BgInput)
	a.showModal("TJ Coder Wallet", panel)
	a.tv.SetFocus(list)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ergoAuthTimeout)
		defer cancel()
		cents, err := fetchErgoBalanceCents(ctx, key)
		if err != nil {
			a.postUI(func() {
				header.SetText(fmt.Sprintf("\n  [%s::b]✗ wallet unavailable[-::B]\n\n  %s\n", a.palette.HexAlert, tview.Escape(err.Error())))
			})
			return
		}
		packs, _ := fetchWalletPacks(ctx, key)
		a.postUI(func() {
			a.setErgoBalanceCents(cents)
			header.SetText(walletHeader(a.palette, cents, a.sessionSpendLine()))
			list.Clear()
			for _, p := range packs {
				p := p
				shortcut := rune(0)
				if p.N >= 1 && p.N <= 9 {
					shortcut = rune('0' + p.N)
				}
				list.AddItem(p.Button(), p.Label, shortcut, func() { a.startTopup(key, p) })
			}
			a.tv.SetFocus(list)
		})

		// Poll the balance every few seconds while the wallet pane is open,
		// so a completed Stripe payment is reflected without reopening.
		pollCtx, pollCancel := context.WithCancel(context.Background())
		a.postUI(func() { a.balanceCancel = pollCancel })
		go func() {
			t := time.NewTicker(3 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-pollCtx.Done():
					return
				case <-t.C:
					ctx, cancel := context.WithTimeout(context.Background(), ergoAuthTimeout)
					newCents, err := fetchErgoBalanceCents(ctx, key)
					cancel()
					if err != nil {
						continue
					}
					a.postUI(func() {
						a.setErgoBalanceCents(newCents)
						header.SetText(walletHeader(a.palette, newCents, a.sessionSpendLine()))
					})
				}
			}
		}()
	}()
}

// refreshErgoBalanceAsync quietly updates the status-bar balance; a no-op
// unless the active provider is a signed-in ergo client.
func (a *App) refreshErgoBalanceAsync() {
	e, ok := a.provider.(*client.Ergo)
	if !ok || !e.Authenticated() {
		return
	}
	// Load the gateway's published rates (once per client) so /usage
	// can estimate cost when a response carries no gateway cost.
	e.PrefetchPricing()
	key := auth.ResolveErgoToken()
	if key == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ergoAuthTimeout)
		defer cancel()
		cents, err := fetchErgoBalanceCents(ctx, key)
		if err != nil {
			return
		}
		a.postUI(func() { a.setErgoBalanceCents(cents) })
	}()
}

func (a *App) setErgoBalanceCents(cents int64) {
	a.ergoBalanceCents = cents
	a.ergoBalanceKnown = true
	a.observeWalletForUsage(cents)
	a.refreshContextBar()
}

func (a *App) clearErgoBalance() {
	a.ergoBalanceKnown = false
	a.refreshContextBar()
}

func formatCents(c int64) string { return fmt.Sprintf("$%.2f", float64(c)/100.0) }

// ergoBalanceSegment is the status-bar fragment ("" when unknown / not ergo).
func (a *App) ergoBalanceSegment() string {
	if !a.ergoBalanceKnown {
		return ""
	}
	if a.ergoBalanceCents <= 0 {
		// Out of credit: flagged by glyph + bold, not by hue.
		return fmt.Sprintf("   [%s::b]! ergo %s[-::B]", a.palette.HexAlert, formatCents(a.ergoBalanceCents))
	}
	return fmt.Sprintf("   [%s]●[-] [%s]ergo %s[-]", a.palette.HexDim, a.palette.HexDim, formatCents(a.ergoBalanceCents))
}

// ergoRunErrorNote returns extra guidance for billing/auth errors from the
// ergo provider ("" for anything else). It also kicks a balance refresh.
func (a *App) ergoRunErrorNote(err error) string {
	var ice *client.InsufficientCreditsError
	switch {
	case errors.As(err, &ice):
		a.setErgoBalanceCents(maxInt64(ice.BalanceCents, 0))
		return fmt.Sprintf("Out of ergo credits. Top up: %s — then retry your message. (/balance to check)", ice.TopupLink())
	case client.IsAccountInactive(err):
		// The key is fine; the account is not. Signing in again cannot help.
		return ""
	case client.IsErgoAuthError(err):
		return "Your TJ Coder session is no longer valid — use /login to sign in again."
	}
	return ""
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ergoBaseURL honors ERGO_BASE_URL for staging/dev gateways.
func ergoBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("ERGO_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return client.ErgoDefaultBaseURL
}

// swapErgoProvider hot-swaps the TUI's chat provider to an ergo client
// carrying `key` (empty string leaves it unauthenticated → friendly 401
// on the next chat).
func (a *App) swapErgoProvider(key string) {
	a.mu.Lock()
	a.ctxWinMu.Lock()
	a.provider = client.NewErgo(ergoBaseURL(), key, 0)
	a.contextWindows = nil
	a.ctxWinGen++
	a.ctxWinMu.Unlock()
	a.mu.Unlock()
	if a.runner != nil {
		// The runner holds its own provider reference; keep it in sync so
		// the very next turn uses the new credential.
		a.runner.Provider = a.provider
	}
	a.refreshHeader()
}

// fetchErgoBalance returns the wallet balance as "$12.34".
func fetchErgoBalance(ctx context.Context, key string) (string, error) {
	cents, err := fetchErgoBalanceCents(ctx, key)
	if err != nil {
		return "", err
	}
	return formatCents(cents), nil
}

// fetchErgoBalanceCents asks the ergo gateway for the caller's wallet
// balance in cents via GET /v1/wallet/balance.
func fetchErgoBalanceCents(ctx context.Context, key string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ergoBaseURL()+"/v1/wallet/balance", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client.ApplyErgoIdentityHeaders(req)
	resp, err := (&http.Client{Timeout: ergoAuthTimeout}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("wallet balance request failed: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return 0, fmt.Errorf("endpoint not available — check %s", client.ErgoDefaultTopupURL)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return 0, fmt.Errorf("session expired — run /login again")
	case resp.StatusCode != http.StatusOK:
		return 0, fmt.Errorf("wallet balance: %s", resp.Status)
	}
	var body struct {
		BalanceCents int64 `json:"balance_cents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, err
	}
	return body.BalanceCents, nil
}

// walletPack is one numbered top-up option in the wallet pane.
type walletPack struct {
	N        int
	Label    string
	USDCents int64
	PackID   string // set for catalog packs; empty for custom amounts
	Bonus    string
}

func (p walletPack) Button() string {
	if p.Bonus != "" {
		return fmt.Sprintf("%d  %s  %s", p.N, formatCents(p.USDCents), p.Bonus)
	}
	return fmt.Sprintf("%d  %s", p.N, formatCents(p.USDCents))
}

func (p walletPack) Line() string {
	if p.Bonus != "" {
		return fmt.Sprintf("  %d   %-8s  %s  %s", p.N, formatCents(p.USDCents), p.Label, p.Bonus)
	}
	return fmt.Sprintf("  %d   %-8s  %s", p.N, formatCents(p.USDCents), p.Label)
}

// defaultWalletPacks is used when the packs endpoint is unavailable.
func defaultWalletPacks() []walletPack {
	return []walletPack{
		{N: 1, Label: "Starter", USDCents: 500, PackID: "starter"},
		{N: 2, Label: "Builder", USDCents: 2000, PackID: "builder", Bonus: "+10%"},
		{N: 3, Label: "Scale", USDCents: 10000, PackID: "scale", Bonus: "+20%"},
		{N: 4, Label: "Custom", USDCents: 1000},
		{N: 5, Label: "Custom", USDCents: 2500},
		{N: 6, Label: "Custom", USDCents: 5000},
	}
}

// walletHeader renders the /balance header. session is the session
// spend line shared with /usage ("" before the first model call); it
// rides on the label line so the header keeps its fixed height.
func walletHeader(pal Palette, cents int64, session string) string {
	label := fmt.Sprintf("[%s]Balance[-]", pal.HexDim)
	if session != "" {
		label += fmt.Sprintf("   [%s]%s[-]", pal.HexFaint, tview.Escape(session))
	}
	return fmt.Sprintf(
		"\n  %s\n\n      [%s::b]%s[-:-:-]\n\n  [%s]1–9 picks · ↑↓ moves · Enter confirms · Esc closes[-]\n",
		label, pal.HexPurple, formatCents(cents), pal.HexDim)
}

func fetchWalletPacks(ctx context.Context, key string) ([]walletPack, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ergoBaseURL()+"/v1/wallet/packs", nil)
	if err != nil {
		return defaultWalletPacks(), err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client.ApplyErgoIdentityHeaders(req)
	resp, err := (&http.Client{Timeout: ergoAuthTimeout}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return defaultWalletPacks(), err
	}
	defer resp.Body.Close()
	var body struct {
		Packs []struct {
			ID       string `json:"id"`
			Label    string `json:"label"`
			USDCents int64  `json:"usd_cents"`
			BonusPct int    `json:"bonus_pct"`
		} `json:"packs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Packs) == 0 {
		return defaultWalletPacks(), err
	}
	out := make([]walletPack, 0, len(body.Packs)+3)
	n := 1
	for _, p := range body.Packs {
		if strings.HasPrefix(p.ID, "wifi-") {
			continue
		}
		bonus := ""
		if p.BonusPct > 0 {
			bonus = fmt.Sprintf("+%d%%", p.BonusPct)
		}
		out = append(out, walletPack{N: n, Label: p.Label, USDCents: p.USDCents, PackID: p.ID, Bonus: bonus})
		n++
	}
	for _, c := range []int64{1000, 2500, 5000} {
		out = append(out, walletPack{N: n, Label: "Custom", USDCents: c})
		n++
	}
	if len(out) == 0 {
		return defaultWalletPacks(), nil
	}
	return out, nil
}

func (a *App) startTopup(key string, p walletPack) {
	a.appendActivity(fmt.Sprintf("Opening Stripe Checkout for %s…", formatCents(p.USDCents)))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ergoAuthTimeout)
		defer cancel()
		url, err := createCheckout(ctx, key, p)
		a.postUI(func() {
			if err != nil {
				a.appendActivity(fmt.Sprintf(a.palette.Theme.Err("checkout failed")+": %v", err))
				return
			}
			if err := openBrowser(url); err != nil {
				a.appendActivity("Checkout URL: " + url)
				return
			}
			a.appendActivity(fmt.Sprintf("Stripe Checkout opened for %s. Balance updates after payment.", formatCents(p.USDCents)))
			if a.balanceCancel != nil {
				a.balanceCancel()
				a.balanceCancel = nil
			}
			a.closeModal()
		})
	}()
}

func createCheckout(ctx context.Context, key string, p walletPack) (string, error) {
	body := map[string]any{}
	if p.PackID != "" {
		body["packId"] = p.PackID
	} else {
		body["usdCents"] = p.USDCents
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ergoBaseURL()+"/v1/wallet/checkout", strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client.ApplyErgoIdentityHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: ergoAuthTimeout}).Do(req)
	if err != nil {
		return "", fmt.Errorf("checkout request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checkout: %s", resp.Status)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", fmt.Errorf("checkout returned no url")
	}
	return out.URL, nil
}
