package client

import "math"

// ContextUsage is one reading of how much of a model's context window a
// conversation occupies. It is the single source of truth for every
// context indicator (status bar, /compact tip, headless "ctx:" lines).
type ContextUsage struct {
	// Used is the number of tokens the next request will occupy:
	// provider-reported prompt+completion tokens of the last turn plus
	// an estimate of anything appended since.
	Used int `json:"used"`
	// Window is the model's context length; 0 means unknown.
	Window int `json:"window,omitempty"`
	// Estimated is true when Used contains a chars/4 estimate rather
	// than only provider-reported counts.
	Estimated bool `json:"estimated,omitempty"`
	// Overhead is the provider-measured cost of everything outside the
	// history (system prompt, tool schema, injected context). 0 = not
	// measured yet.
	Overhead int `json:"overhead,omitempty"`
	// Messages is len(history) the reading covers.
	Messages int `json:"messages"`
	// Model the reading was taken with.
	Model string `json:"model,omitempty"`
}

// Percent returns the share of the window in use, rounded to the nearest
// whole percent. It never reports 100% unless the window is actually
// full, and reports >100% only when Used exceeds the window. ok is
// false when the window is unknown.
func (u ContextUsage) Percent() (pct int, ok bool) {
	if u.Window <= 0 {
		return 0, false
	}
	if u.Used <= 0 {
		return 0, true
	}
	exact := float64(u.Used) * 100 / float64(u.Window)
	switch {
	case u.Used > u.Window:
		return int(math.Ceil(exact)), true
	case u.Used == u.Window:
		return 100, true
	}
	pct = int(math.Round(exact))
	if pct >= 100 {
		pct = 99
	}
	return pct, true
}

// Over reports whether the conversation no longer fits the window.
func (u ContextUsage) Over() bool { return u.Window > 0 && u.Used > u.Window }

// EstimateTokens approximates the prompt tokens history will occupy
// (chars/4 plus a small per-message framing cost). Thinking is excluded
// because providers do not re-send it as prompt.
func EstimateTokens(history []Message) int {
	chars := 0
	for _, m := range history {
		chars += len(m.Content) + len(m.Role) + len(m.ToolName) + 16
		for _, tc := range m.ToolCalls {
			chars += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return (chars + 3) / 4
}
