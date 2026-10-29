package tooling

import (
	"encoding/json"
	"fmt"

	"github.com/tjcoder-labs/cli/internal/client"
)

// contextTracker turns provider-reported token counts into ContextUsage
// readings for one Run. The provider's prompt count already covers the
// system prompt, tool schema, injected context and history, so it is the
// base; only messages appended after that report are estimated.
type contextTracker struct {
	model    string
	window   int
	fixedEst int // estimate for system prompt + tool schema before any report
	base     client.ContextUsage
	reported bool
}

func newContextTracker(model string, window int, systemPrompt string, defs []client.ToolDefinition) *contextTracker {
	chars := len(systemPrompt)
	if len(defs) > 0 {
		if b, err := json.Marshal(defs); err == nil {
			chars += len(b)
		}
	}
	return &contextTracker{model: model, window: window, fixedEst: (chars + 3) / 4}
}

// assistant records the reading after history's last message (an
// assistant reply) was appended.
func (t *contextTracker) assistant(history []client.Message) client.ContextUsage {
	msg := history[len(history)-1]
	sent := history[:len(history)-1]
	// A prompt count far below the last reading is a KV-cache artefact
	// (some Ollama builds report only newly evaluated tokens); fall back
	// to the estimate rather than showing the context shrink mid-run.
	if msg.PromptEvalCount <= 0 || (t.reported && msg.PromptEvalCount < t.base.Used/2) {
		return t.estimate(history)
	}
	// The completion is now in history but not yet in any prompt count:
	// add it once. Reasoning tokens are in EvalCount but are not re-sent,
	// so estimate the visible reply when the model was thinking.
	completion, estimated := msg.EvalCount, false
	if msg.Thinking != "" || completion <= 0 {
		completion, estimated = client.EstimateTokens([]client.Message{msg}), true
	}
	overhead := msg.PromptEvalCount - client.EstimateTokens(sent)
	if overhead < 0 {
		overhead = 0
	}
	t.base = client.ContextUsage{
		Used:      msg.PromptEvalCount + completion,
		Window:    t.window,
		Estimated: estimated,
		Overhead:  overhead,
		Messages:  len(history),
		Model:     t.model,
	}
	t.reported = true
	return t.base
}

// estimate returns the reading for history: the last report plus an
// estimate of everything appended since (e.g. tool results).
func (t *contextTracker) estimate(history []client.Message) client.ContextUsage {
	u := t.base
	if t.reported && u.Messages <= len(history) {
		u.Used += client.EstimateTokens(history[u.Messages:])
	} else {
		u = client.ContextUsage{Used: t.fixedEst + client.EstimateTokens(history), Overhead: t.fixedEst}
	}
	u.Window, u.Model, u.Messages, u.Estimated = t.window, t.model, len(history), true
	return u
}

func contextEvent(u client.ContextUsage) Event {
	return Event{Type: EventContext, Text: FormatContextUsage(u), Usage: &u}
}

// FormatContextUsage renders a reading as a one-line "ctx:" summary.
func FormatContextUsage(u client.ContextUsage) string {
	approx := ""
	if u.Estimated {
		approx = "~"
	}
	pct, ok := u.Percent()
	if !ok {
		return fmt.Sprintf("ctx: %s%s tokens used (window unknown)", approx, formatTokensCompact(u.Used))
	}
	s := fmt.Sprintf("ctx: %s%s / %s used (%s%d%%)", approx, formatTokensCompact(u.Used), formatTokensCompact(u.Window), approx, pct)
	if u.Over() {
		return s + ", over limit by " + formatTokensCompact(u.Used-u.Window)
	}
	return s + ", remaining=" + formatTokensCompact(u.Window-u.Used)
}
