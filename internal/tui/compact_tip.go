package tui

import (
	"fmt"
	"strings"
)

// compactionModel resolves the model /compact will summarise with:
// an explicit "/compact <model>" argument wins, then the CompactModel
// config override, then the active model. compactHistory and the
// /compact hint share it so the tip always names the model that will
// actually run.
func (a *App) compactionModel(parts []string) string {
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		return parts[1]
	}
	model := a.currentModel
	if a.config.CompactModel != "" {
		if m := a.sanitizeModel(a.config.CompactModel); m != "" {
			model = m
		}
	}
	return model
}

// compactTip is the /compact hint: the model that will summarise and
// how full the context is relative to THAT model's window, e.g.
// "Compact with ergo/flash · context 62% of 128k". When the window is
// unknown the raw token count is shown without a percentage.
func (a *App) compactTip(input string) string {
	model := a.compactionModel(strings.Fields(input))
	if model == "" {
		model = "current model"
	}
	u := a.contextUsageFor(model)
	pct := contextPercentText(u)
	usage := fmt.Sprintf("context %s tokens", strings.ToLower(pct))
	if u.Window > 0 {
		usage = fmt.Sprintf("context %s of %s", pct, strings.ToLower(formatTokensCompactTUI(u.Window)))
		if u.Over() {
			usage += " (over limit)"
		}
	}
	return fmt.Sprintf("Compact with %s · %s", model, usage)
}

// hintInput is the raw input text the hint line is describing.
func (a *App) hintInput() string {
	if a.input == nil {
		return ""
	}
	return a.input.GetText()
}
