package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
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

var ctxUsedRe = regexp.MustCompile(`([\d.]+)\s*([KM]?)\s*/`)

// contextTokensUsed returns the best available count of tokens the
// conversation occupies: the last provider-reported prompt size
// carried in contextInfo when present, else a chars/4 estimate of the
// history (the same heuristic refreshContextForModel uses).
func (a *App) contextTokensUsed() int {
	if m := ctxUsedRe.FindStringSubmatch(strings.TrimPrefix(a.contextInfo, "ctx:")); m != nil {
		if n := parseTokenCount(m[1], m[2]); n > 0 {
			return n
		}
	}
	chars := 0
	for _, m := range a.history {
		chars += len(m.Role) + len(m.Content) + len(m.Thinking) + len(m.ToolName)
		for _, tc := range m.ToolCalls {
			chars += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return chars / 4
}

// compactWindow returns the cached context window for model (0 when
// unknown or not yet resolved). The first lookup for a model is done
// off the UI goroutine because ContextWindow may hit the network
// (Ollama /api/show); the hint re-renders when it lands.
func (a *App) compactWindow(model string) int {
	if model == "" || a.provider == nil {
		return 0
	}
	a.mu.Lock()
	if a.compactWindows == nil {
		a.compactWindows = map[string]int{}
	}
	n, ok := a.compactWindows[model]
	if !ok {
		a.compactWindows[model] = 0 // mark in-flight; a miss stays 0
	}
	a.mu.Unlock()
	if ok {
		return n
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		w, err := a.provider.ContextWindow(ctx, model)
		if err != nil || w <= 0 {
			return
		}
		a.mu.Lock()
		a.compactWindows[model] = w
		a.mu.Unlock()
		if a.tv != nil {
			a.tv.QueueUpdateDraw(func() { a.refreshContextBar() })
		}
	}()
	return 0
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
	used := a.contextTokensUsed()
	window := a.compactWindow(model)
	usage := fmt.Sprintf("context ~%s tokens", strings.ToLower(formatTokensCompactTUI(used)))
	if window > 0 {
		pct := int(float64(used)/float64(window)*100 + 0.5)
		usage = fmt.Sprintf("context %d%% of %s", pct, strings.ToLower(formatTokensCompactTUI(window)))
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
