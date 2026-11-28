package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/providers"
	"github.com/tjcoder-labs/cli/internal/session"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// accountProviderName is the registry entry that signs in with a TJ Coder
// account (device-code flow).
const accountProviderName = "tjcoder"

// resendOffer is a message that was waiting when the user was sent to sign
// in. Preview is a one-line excerpt for the screens; Run resends it. It is
// only ever run by an explicit choice ("Try again", "Send my message").
type resendOffer struct {
	Preview string
	Run     func()
}

// takeResend returns and clears the pending resend offer (nil when none).
func (a *App) takeResend() *resendOffer {
	o := a.pendingResend
	a.pendingResend = nil
	return o
}

// previewPrompt is the first line of a message, trimmed to a short excerpt.
func previewPrompt(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i]) + "…"
	}
	if r := []rune(s); len(r) > 60 {
		s = strings.TrimSpace(string(r[:60])) + "…"
	}
	return s
}

// hubChoice is what one option of the login hub does.
type hubChoice int

const (
	hubSignIn hubChoice = iota
	hubSwitchAccount
	hubSignOut
	hubUseProvider
	hubConfigure
	hubWallet
	hubTryAgain
)

// hubState is everything the login hub needs to pick its copy and options.
type hubState struct {
	Signed   bool   // a key is stored (keychain, file, or ERGO_API_KEY)
	Rejected bool   // the gateway rejected that key
	Env      bool   // the key comes from ERGO_API_KEY (cannot be replaced here)
	Email    string // saved identity, "" when unknown
	Balance  string // "$12.34", "" when unknown
	CanRetry bool   // a message is waiting to be resent
}

// buildLoginHub returns the hub card for st and the action behind each of
// its options (same order). One card serves /login, the first message
// while signed out, and a rejected key; only the heading and options
// change with the account state.
func buildLoginHub(st hubState) (tools.PromptRequest, []hubChoice) {
	req := tools.PromptRequest{Kind: tools.PromptNextSteps}
	var choices []hubChoice
	add := func(c hubChoice, label, desc string) {
		choices = append(choices, c)
		req.Options = append(req.Options, label)
		req.Descriptions = append(req.Descriptions, desc)
	}
	switch {
	case st.Signed && st.Rejected:
		req.Title = "Sign in again"
		req.Question = "Your TJ Coder session is no longer valid."
		if st.Env {
			req.Question += "\nThe key in ERGO_API_KEY was rejected. Update or unset it, then restart."
		} else {
			add(hubSignIn, "Sign in again", "Get a fresh session for this device.")
		}
	case st.Signed:
		req.Title = "TJ Coder account"
		lines := []string{"You're signed in."}
		if st.Email != "" {
			lines[0] = "Signed in as " + st.Email
		}
		if st.Balance != "" {
			lines = append(lines, "Balance: "+st.Balance)
		}
		req.Question = strings.Join(lines, "\n")
		if !st.Env {
			add(hubSwitchAccount, "Switch account", "Sign out this device first, then sign in with another email.")
		}
		signOutDesc := "Revoke this device's key and return to the start screen."
		if st.Env {
			signOutDesc = "Clear the local sign-in. Unset ERGO_API_KEY to finish; that key is not revoked."
		}
		add(hubSignOut, "Sign out", signOutDesc)
	default:
		req.Title = "Get started"
		req.Question = "Sign in to use TJ Coder, or switch to another provider."
		add(hubSignIn, "Sign in to TJ Coder", "New accounts get $5 credit automatically.")
	}
	add(hubUseProvider, "Use a different provider", "Switch to Ollama, Ollama Cloud, or another provider you've added.")
	add(hubConfigure, "Configure providers", "Add a BYOK OpenAI-compatible key, or edit the defaults.")
	if st.Signed && !st.Rejected {
		add(hubWallet, "Open wallet", "Check your balance or top up. Same as /balance.")
	}
	if st.CanRetry {
		add(hubTryAgain, "Try again", "Resend your last message.")
	}
	return req, choices
}

// openLoginHub shows the login hub as an in-transcript card. offer (may be
// nil) is a message waiting on the sign-in; rejected says the gateway just
// refused the saved key. It must run on the UI goroutine. The key lookup
// (it can touch the OS keychain) and the blocking Ask both run off it.
func (a *App) openLoginHub(offer *resendOffer, rejected bool) {
	if a.tv == nil || a.loginHubOpen {
		return
	}
	if offer != nil {
		a.pendingResend = offer
	} else {
		offer = a.pendingResend
	}
	a.loginHubOpen = true
	rejected = rejected || a.ergoKeyRejected
	balance := ""
	if a.ergoBalanceKnown {
		balance = formatCents(a.ergoBalanceCents)
	}
	go func() {
		// One lookup: TokenStore reports where a key lives without
		// returning it, and each keychain call can block for seconds.
		store := auth.TokenStore()
		st := hubState{CanRetry: offer != nil}
		if store != "" {
			st.Signed = true
			st.Rejected = rejected
			st.Env = store == "env"
			st.Balance = balance
			if id, ok := auth.LoadErgoIdentity(); ok {
				st.Email = id.Email
			}
		}
		req, choices := buildLoginHub(st)
		ans, err := a.Ask(context.Background(), req)
		a.postUI(func() {
			a.loginHubOpen = false
			if err != nil || ans.Cancelled || ans.Index < 0 || ans.Index >= len(choices) {
				return
			}
			a.runHubChoice(choices[ans.Index], offer, st)
		})
	}()
}

// runHubChoice carries out one hub option. UI goroutine.
func (a *App) runHubChoice(c hubChoice, offer *resendOffer, st hubState) {
	switch c {
	case hubSignIn:
		a.openSignInForm(st.Email)
	case hubSwitchAccount:
		// Revoke this device's key first, so the old account is not left
		// signed in behind the new one.
		a.signOutThen(func(bool) {
			a.pendingResend = offer
			a.openSignInForm("")
		})
	case hubSignOut:
		a.signOutThen(func(fully bool) {
			a.pendingResend = offer
			if fully {
				a.openLoginHub(nil, false)
			}
		})
	case hubUseProvider:
		a.openProviderPick()
	case hubConfigure:
		a.openProvidersModal()
	case hubWallet:
		a.handleBalanceCommand()
	case hubTryAgain:
		if offer = a.takeResend(); offer != nil && offer.Run != nil {
			offer.Run()
		}
	}
}

// providerPickDesc is the dim line under a provider on the pick card.
func providerPickDesc(p providers.Config, active bool) string {
	where := strings.TrimPrefix(strings.TrimPrefix(p.BaseURL, "https://"), "http://")
	var parts []string
	switch {
	case p.Auth == providers.AuthAPIKey && p.APIKey == "":
		parts = append(parts, "needs an API key")
	case p.Auth == providers.AuthAPIKey:
		parts = append(parts, "API key saved")
	default:
		parts = append(parts, "no key needed")
	}
	parts = append(parts, where)
	if active {
		parts = append(parts, "current")
	}
	return strings.Join(parts, " · ")
}

// openProviderPick shows the "Use a different provider" follow-up card:
// every registered provider except the TJ Coder account, then Back.
func (a *App) openProviderPick() {
	reg := providers.Load()
	var cfgs []providers.Config
	req := tools.PromptRequest{
		Kind:     tools.PromptNextSteps,
		Title:    "Use a different provider",
		Question: "Pick where your messages should go.",
	}
	for _, p := range reg.Providers {
		if p.Auth == providers.AuthDeviceCode {
			continue
		}
		cfgs = append(cfgs, p)
		req.Options = append(req.Options, p.Name)
		req.Descriptions = append(req.Descriptions, providerPickDesc(p, p.Name == reg.Active))
	}
	if len(cfgs) == 0 {
		a.appendActivity("No other providers yet — add one with /providers add")
		a.openProvidersModal()
		return
	}
	req.Options = append(req.Options, "Back")
	req.Descriptions = append(req.Descriptions, "Return to the sign-in options.")
	go func() {
		ans, err := a.Ask(context.Background(), req)
		if err != nil || ans.Cancelled || ans.Index < 0 {
			return
		}
		a.postUI(func() {
			if ans.Index >= len(cfgs) {
				a.openLoginHub(nil, false)
				return
			}
			a.activateProviderChoice(cfgs[ans.Index])
		})
	}()
}

// activateProviderChoice switches to cfg, asking for its API key first when
// it needs one and has none. UI goroutine.
func (a *App) activateProviderChoice(cfg providers.Config) {
	use := func(c providers.Config) {
		a.useProvider(c)
		if a.pendingResend != nil {
			a.appendActivity("Your message is still waiting — /login → Try again resends it")
		}
	}
	if cfg.Auth == providers.AuthAPIKey && cfg.APIKey == "" {
		a.openAPIKeyLoginThen(cfg, use)
		return
	}
	use(cfg)
}

// useProvider makes cfg the active provider: registry, saved preference,
// and the live client. It is what /providers use, the provider manager's
// Activate, /login <provider> and the hub all share. UI goroutine.
func (a *App) useProvider(cfg providers.Config) {
	reg := providers.Load()
	reg.Active = cfg.Name
	if err := providers.Save(reg); err != nil {
		a.appendActivity(fmt.Sprintf(a.palette.Theme.Err("Provider activate failed")+": %v", err))
		return
	}
	if err := session.SetProvider(cfg.Name); err != nil {
		a.appendActivity(fmt.Sprintf(a.palette.Theme.Err("Provider preference save failed")+": %v", err))
	}
	a.switchProvider(cfg)
}

// markAccountProviderActive records the TJ Coder account provider as the
// active one after a sign-in (the live client was already swapped by
// swapErgoProvider), so the next launch starts where the user left off.
func (a *App) markAccountProviderActive() {
	cfg, ok := accountProvider(providers.Load())
	if !ok {
		return
	}
	reg := providers.Load()
	if reg.Active == cfg.Name {
		return
	}
	reg.Active = cfg.Name
	if err := providers.Save(reg); err != nil {
		return
	}
	_ = session.SetProvider(cfg.Name)
}

// accountProvider finds the registry entry that uses the account sign-in.
func accountProvider(reg providers.Registry) (providers.Config, bool) {
	for _, p := range reg.Providers {
		if p.Name == accountProviderName {
			return p, true
		}
	}
	for _, p := range reg.Providers {
		if p.Auth == providers.AuthDeviceCode {
			return p, true
		}
	}
	return providers.Config{}, false
}

// resolveLoginProvider maps the argument of /login <provider> to a registry
// entry: names match case-insensitively, and "ergo" (the old name) means
// the TJ Coder account.
func resolveLoginProvider(reg providers.Registry, arg string) (providers.Config, bool) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if arg == "" {
		return providers.Config{}, false
	}
	for _, p := range reg.Providers {
		if strings.ToLower(p.Name) == arg {
			return p, true
		}
	}
	if arg == "ergo" || arg == "ergo-gateway" {
		return accountProvider(reg)
	}
	return providers.Config{}, false
}

// handleLoginSlash dispatches /login:
//
//	/login             → the sign-in hub (state-aware)
//	/login <provider>  → go straight to that provider
func (a *App) handleLoginSlash(parts []string) {
	if len(parts) < 2 {
		a.openLoginHub(nil, false)
		return
	}
	reg := providers.Load()
	cfg, ok := resolveLoginProvider(reg, parts[1])
	if !ok {
		names := make([]string, 0, len(reg.Providers))
		for _, p := range reg.Providers {
			names = append(names, p.Name)
		}
		a.appendActivity(fmt.Sprintf("Unknown provider %q. Available: %s. /login on its own shows the options.", parts[1], strings.Join(names, ", ")))
		return
	}
	a.loginToProvider(cfg)
}

// loginToProvider signs in to cfg (if it needs it) and makes it active.
func (a *App) loginToProvider(cfg providers.Config) {
	switch cfg.Auth {
	case providers.AuthDeviceCode:
		go func() {
			signed := auth.TokenStore() != ""
			a.postUI(func() {
				if signed {
					a.appendActivity("Already signed in to TJ Coder")
					a.useProvider(cfg)
					return
				}
				a.openSignInForm("")
			})
		}()
	case providers.AuthAPIKey:
		if cfg.APIKey == "" {
			a.openAPIKeyLoginThen(cfg, a.useProvider)
			return
		}
		a.useProvider(cfg)
	default:
		a.useProvider(cfg)
	}
}

// loginArgHints feeds "/login <provider>" into the argument palette: the
// registered providers, filtered as you type. ok is false once the input is
// not a provider prefix.
func (a *App) loginArgHints(text string) (string, []wsArg, bool) {
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return "", nil, false
	}
	body := strings.TrimPrefix(text, "/")
	i := strings.IndexAny(body, " \t")
	if i == -1 || !strings.EqualFold(body[:i], "login") {
		return "", nil, false
	}
	rest := strings.TrimLeft(body[i+1:], " \t")
	if strings.ContainsAny(rest, " \t") {
		return "", nil, false
	}
	q := strings.ToLower(rest)
	var items []wsArg
	for _, p := range providers.Load().Providers {
		if q != "" && !strings.HasPrefix(strings.ToLower(p.Name), q) {
			continue
		}
		desc := "no sign-in needed"
		switch p.Auth {
		case providers.AuthDeviceCode:
			desc = "sign in with your email"
		case providers.AuthAPIKey:
			desc = "uses an API key"
		}
		items = append(items, wsArg{label: p.Name, insert: p.Name, desc: desc})
	}
	if len(items) == 0 {
		return "", nil, false
	}
	return "provider:", items, true
}
