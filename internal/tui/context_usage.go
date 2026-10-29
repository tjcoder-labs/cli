package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
)

// contextUsageFor is the ONE place the context indicator is computed;
// the status bar and the /compact tip both read it. It starts from the
// last runner reading (provider-reported prompt+completion tokens) and
// estimates only what was appended since; when history was replaced
// (/compact, /clear, restore) it falls back to measured overhead plus an
// estimate of the new history. The window is model's own (0 = unknown).
func (a *App) contextUsageFor(model string) client.ContextUsage {
	u := a.ctxUsage
	n := len(a.history)
	switch {
	case a.busy && u.Messages > n:
		// Mid-run reading over the runner's (longer) history: live.
	case u.Used > 0 && u.Messages == n:
	case u.Used > 0 && u.Messages < n:
		u.Used += client.EstimateTokens(a.history[u.Messages:])
		u.Estimated = true
	default:
		u.Used = 0
		if n > 0 {
			u.Used = u.Overhead + client.EstimateTokens(a.history)
		}
		u.Estimated = true
	}
	if u.Model != model || u.Window <= 0 {
		u.Window = a.contextWindowFor(model)
	}
	if u.Model != "" && u.Model != model {
		u.Estimated = true // counts came from another model's tokenizer
	}
	return u
}

// contextPercentText renders u's share of the window: "62%", "~62%"
// when estimated, "<1%" for a sliver, ">100%" values only when the
// conversation is truly over. Without a window it renders the token
// count instead, e.g. "~12K".
func contextPercentText(u client.ContextUsage) string {
	approx := ""
	if u.Estimated {
		approx = "~"
	}
	pct, ok := u.Percent()
	switch {
	case !ok:
		return approx + formatTokensCompactTUI(u.Used)
	case pct == 0 && u.Used > 0:
		return "<1%"
	}
	return fmt.Sprintf("%s%d%%", approx, pct)
}

// contextStatusText is the status-bar context segment ("" = hide).
func (a *App) contextStatusText() string {
	u := a.contextUsageFor(a.currentModel)
	if u.Used <= 0 {
		return ""
	}
	if u.Window <= 0 {
		return fmt.Sprintf("%s tokens (window unknown)", contextPercentText(u))
	}
	s := fmt.Sprintf("%s %s of %s tokens.", a.renderProgressBar(u.Used, u.Window), contextPercentText(u), formatTokensCompactTUI(u.Window))
	if u.Over() {
		s += fmt.Sprintf(" [%s::b]! over limit[-:-:-]", a.palette.HexAlert)
	}
	return s
}

// resetContextUsage drops the provider reading after history was
// replaced; the measured system-prompt/tool overhead is kept so the
// estimate for the new history stays honest.
func (a *App) resetContextUsage() {
	a.ctxUsage = client.ContextUsage{Overhead: a.ctxUsage.Overhead, Model: a.ctxUsage.Model, Window: a.ctxUsage.Window}
}

// recordContextUsage stores a runner reading and seeds the window cache.
func (a *App) recordContextUsage(u client.ContextUsage) {
	a.ctxUsage = u
	if u.Model != "" && u.Window > 0 {
		a.ctxWinMu.Lock()
		if a.contextWindows == nil {
			a.contextWindows = map[string]int{}
		}
		a.contextWindows[u.Model] = u.Window
		a.ctxWinMu.Unlock()
	}
}

// contextWindowFor returns the cached context window for model (0 when
// unknown or not yet resolved). The first lookup for a model is done
// off the UI goroutine because ContextWindow may hit the network
// (Ollama/gateway /api/show); the bar re-renders when it lands.
//
// It only takes ctxWinMu (never mu), so it is safe to call while the
// caller holds mu — saveSession and refreshContextBar reach it from
// submit/submitScheduled with mu held. Using mu here self-deadlocked
// every plain chat message.
func (a *App) contextWindowFor(model string) int {
	if model == "" {
		return 0
	}
	a.ctxWinMu.Lock()
	provider := a.provider
	if provider == nil {
		a.ctxWinMu.Unlock()
		return 0
	}
	if a.contextWindows == nil {
		a.contextWindows = map[string]int{}
	}
	n, ok := a.contextWindows[model]
	if !ok {
		a.contextWindows[model] = 0 // mark in-flight; a miss stays 0
	}
	gen := a.ctxWinGen
	a.ctxWinMu.Unlock()
	if ok {
		return n
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		w, err := provider.ContextWindow(ctx, model)
		if err != nil || w <= 0 {
			return
		}
		a.ctxWinMu.Lock()
		stored := a.ctxWinGen == gen
		if stored {
			if a.contextWindows == nil {
				a.contextWindows = map[string]int{}
			}
			a.contextWindows[model] = w
		}
		a.ctxWinMu.Unlock()
		if stored && a.tv != nil {
			a.postUI(func() { a.refreshContextBar() })
		}
	}()
	return 0
}

// refreshContextForModel re-renders the indicator after a model or
// provider switch: the window is re-resolved for the new model (unknown
// stays explicitly unknown, never the old model's total), and counts
// measured with the previous model are kept but marked estimated.
func (a *App) refreshContextForModel(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	a.contextWindowFor(model)
	a.refreshContextBar()
}

// sessionContextUsage is the reading persisted with the session: the
// current-model view of history, so a restore shows the same number.
func (a *App) sessionContextUsage() *client.ContextUsage {
	u := a.contextUsageFor(a.currentModel)
	if u.Used <= 0 {
		return nil
	}
	u.Messages = len(a.history)
	return &u
}
