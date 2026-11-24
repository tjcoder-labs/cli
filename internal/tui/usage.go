package tui

import (
	"fmt"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/render"
	"github.com/tjcoder-labs/cli/internal/session"
)

// recordTurnUsage folds one model call's usage into the session totals
// (persisted with the session) and refreshes the footer counter. Runs
// on the UI goroutine (handleEvent).
func (a *App) recordTurnUsage(t session.TurnUsage) {
	a.usageMu.Lock()
	if a.usage == nil {
		a.usage = &session.UsageStats{}
	}
	a.usage.Add(t)
	a.usageMu.Unlock()
	if t.BalanceCents != nil {
		// The gateway reported the post-debit wallet: keep the status
		// bar balance in step with what /usage and /balance show.
		a.ergoBalanceCents = *t.BalanceCents
		a.ergoBalanceKnown = true
	}
	a.refreshFooter()
}

// usageSnapshot returns a copy of the session usage for persistence or
// rendering (nil when nothing was recorded).
func (a *App) usageSnapshot() *session.UsageStats {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	return a.usage.Clone()
}

// setUsage replaces the session usage (load / clear).
func (a *App) setUsage(u *session.UsageStats) {
	a.usageMu.Lock()
	a.usage = u.Clone()
	a.usageMu.Unlock()
}

// observeWalletForUsage records a /v1/wallet/balance reading so /usage
// can show the same session spend /balance does.
func (a *App) observeWalletForUsage(cents int64) {
	a.usageMu.Lock()
	if a.usage == nil {
		a.usage = &session.UsageStats{}
	}
	a.usage.ObserveWallet(cents)
	a.usageMu.Unlock()
}

// usageCounterSegment is the compact footer counter ("" before the
// first model call), e.g. "↑ 12.3k ↓ 4.5k tokens".
func (a *App) usageCounterSegment() string {
	u := a.usageSnapshot()
	text := u.CounterText()
	if text == "" {
		return ""
	}
	return fmt.Sprintf("[%s]%s[-]", a.palette.HexDim, tview.Escape(text))
}

// sessionSpendLine is the /balance pane's session line, built from the
// same UsageStats /usage renders so the two always agree.
func (a *App) sessionSpendLine() string {
	u := a.usageSnapshot()
	if u.Empty() {
		return ""
	}
	tot := u.Totals()
	s := fmt.Sprintf("this session: %d request", tot.Requests)
	if tot.Requests != 1 {
		s += "s"
	}
	if spent, ok := u.WalletSpentCents(); ok && spent >= 0 {
		s += " · " + session.FormatUSD(float64(spent)/100) + " from wallet"
	} else if tot.Priced() {
		s += " · " + tot.CostText()
	}
	return s + " (/usage)"
}

// handleUsageCommand prints the session's usage table (/usage).
func (a *App) handleUsageCommand() {
	md := "Token usage — this session\n\n" + a.usageSnapshot().Markdown()
	if a.busy || a.transcript == nil {
		// Mid-turn the transcript is streaming; keep the table out of it.
		a.appendActivity(render.Markdown(md, a.palette.Theme, render.Options{Width: innerWidth(a.activity)}))
		return
	}
	fmt.Fprintf(a.transcript, "\n%s\n", a.highlightTranscriptText(md))
	a.transcript.ScrollToEnd()
	a.appendActivity("Printed session token usage (/usage)")
}
