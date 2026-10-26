package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
)

// ergoAuthTimeout bounds every ergo network probe the TUI fires (key
// validation, balance refresh).
const ergoAuthTimeout = 12 * time.Second

// ergoLoginPrompt returns a prominent sign-in notice when the active provider
// is ergo but no credentials are configured, and "" otherwise. ergo is the
// default provider, so we tell the user to /login instead of silently
// falling back to ollama.
func ergoLoginPrompt(p client.Provider) string {
	if e, ok := p.(*client.Ergo); ok && !e.Authenticated() {
		return "[yellow::b]ergo is your default provider but you are not signed in.[-:-:-] Run [::b]/login[-:-:-] to sign in (or start with --provider ollama to use a local model)."
	}
	return ""
}

// handleLoginCommand prompts for a scoped ergo API key (scope "ergo-chat")
// and persists it under ~/.local/share/coder. The key is never echoed in
// the transcript or written to session state — it only lives in memory
// and in the 0600 token file managed by internal/auth.
func (a *App) handleLoginCommand() {
	form := tview.NewForm()
	a.styleModalForm(form)
	var key string
	form.AddPasswordField("ergo API key", "", 40, '*', func(s string) { key = s })
	form.AddButton("Sign in", func() {
		key = strings.TrimSpace(key)
		if key == "" {
			a.appendActivity("Sign-in cancelled: empty key")
			return
		}
		// Validate against the gateway before persisting so a typo doesn't
		// lock the user into a broken session.
		if err := a.validateErgoKey(key); err != nil {
			a.appendActivity(fmt.Sprintf("[red]ergo sign-in failed[-]: %v", err))
			return
		}
		if err := auth.SaveErgoToken(key); err != nil {
			a.appendActivity(fmt.Sprintf("[red]ergo sign-in failed[-]: %v", err))
			return
		}
		a.swapErgoProvider(key)
		a.appendActivity("Signed in to ergo (key stored at " + auth.ErgoTokenPath() + ")")
		a.closeModal()
	})
	form.AddButton("Cancel", func() { a.closeModal() })
	a.showModal("ergo Sign-in", form)
}

// handleLogoutCommand removes the persisted ergo key and detaches the
// client-side credential so the next chat surfaces a clean auth error.
func (a *App) handleLogoutCommand() {
	if err := auth.ClearErgoToken(); err != nil {
		a.appendActivity(fmt.Sprintf("[red]ergo sign-out failed[-]: %v", err))
		return
	}
	a.swapErgoProvider("")
	a.appendActivity("Signed out of ergo")
}

// handleBalanceCommand fetches the wallet balance. The gateway may not
// expose /v1/wallet/balance yet; we degrade gracefully with a clear hint.
func (a *App) handleBalanceCommand() {
	key := auth.ResolveErgoToken()
	if key == "" {
		a.appendActivity("Not signed in to ergo — run /login first")
		return
	}
	a.appendActivity("Fetching ergo balance…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ergoAuthTimeout)
		defer cancel()
		bal, err := fetchErgoBalance(ctx, key)
		a.tv.QueueUpdateDraw(func() {
			if err != nil {
				a.appendActivity(fmt.Sprintf("[red]ergo balance unavailable[-]: %v", err))
				return
			}
			a.appendActivity(fmt.Sprintf("ergo balance: %s", bal))
		})
	}()
}

// ergoBaseURL honors ERGO_BASE_URL for staging/dev gateways.
func ergoBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("ERGO_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return client.ErgoDefaultBaseURL
}

// validateErgoKey checks the key with a cheap models-list call. The ergo
// client maps 401/403 to ErgoAuthError so failure text reads naturally.
func (a *App) validateErgoKey(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), ergoAuthTimeout)
	defer cancel()
	prov := client.NewErgo(ergoBaseURL(), key, ergoAuthTimeout)
	_, err := prov.ListModels(ctx)
	return err
}

// swapErgoProvider hot-swaps the TUI's chat provider to an ergo client
// carrying `key` (empty string leaves it unauthenticated → friendly 401
// on the next chat).
func (a *App) swapErgoProvider(key string) {
	a.provider = client.NewErgo(ergoBaseURL(), key, 0)
	a.refreshHeader()
}

// fetchErgoBalance asks the ergo gateway for the caller's wallet balance.
// Returns "$12.34", or a descriptive error when the gateway doesn't serve
// the endpoint yet (404 → hint to the dashboard).
func fetchErgoBalance(ctx context.Context, key string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ergoBaseURL()+"/v1/wallet/balance", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: ergoAuthTimeout}).Do(req)
	if err != nil {
		return "", fmt.Errorf("wallet balance request failed: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("endpoint not available yet — check https://app.tjcoder.com")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("session expired — run /login again")
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("wallet balance: %s", resp.Status)
	}
	var body struct {
		BalanceCents int64 `json:"balance_cents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return fmt.Sprintf("$%.2f", float64(body.BalanceCents)/100.0), nil
}
