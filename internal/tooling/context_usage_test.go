package tooling

import (
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
)

func TestContextTrackerUsesProviderUsageWithoutDoubleCounting(t *testing.T) {
	tr := newContextTracker("m", 1000, "sys", nil)
	h := []client.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "ok", ToolCalls: []client.ToolCall{{}}, PromptEvalCount: 300, EvalCount: 50}}
	u := tr.assistant(h)
	if u.Used != 350 || u.Estimated || u.Messages != 2 || u.Window != 1000 {
		t.Fatalf("reported: %+v", u)
	}
	tool := client.Message{Role: "tool", Content: strings.Repeat("r", 400)}
	h = append(h, tool)
	if u := tr.estimate(h); u.Used != 350+client.EstimateTokens([]client.Message{tool}) || !u.Estimated {
		t.Fatalf("pending tool result: %+v", u)
	}
	// The next prompt count already includes the previous completion and
	// the tool result: it replaces the base, it is not added to it.
	h = append(h, client.Message{Role: "assistant", Content: "done", PromptEvalCount: 460, EvalCount: 20})
	if u := tr.assistant(h); u.Used != 480 || u.Estimated {
		t.Fatalf("second turn: %+v", u)
	}
}

func TestContextTrackerEstimatesWithoutProviderUsage(t *testing.T) {
	tr := newContextTracker("m", 0, strings.Repeat("s", 4000), nil)
	h := []client.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "ok"}}
	u := tr.assistant(h)
	if !u.Estimated || u.Used != 1000+client.EstimateTokens(h) {
		t.Fatalf("estimate must include the system prompt: %+v", u)
	}
	if !strings.Contains(FormatContextUsage(u), "window unknown") {
		t.Fatalf("format: %q", FormatContextUsage(u))
	}
}

func TestContextTrackerIgnoresReasoningAndCacheArtefacts(t *testing.T) {
	tr := newContextTracker("m", 1000, "", nil)
	h := []client.Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: "abcd", Thinking: "long", PromptEvalCount: 400, EvalCount: 300}}
	if u := tr.assistant(h); u.Used != 400+client.EstimateTokens(h[1:]) {
		t.Fatalf("reasoning tokens are not re-sent: %+v", u)
	}
	h = append(h, client.Message{Role: "assistant", Content: "x", PromptEvalCount: 10, EvalCount: 5})
	if u := tr.assistant(h); u.Used < 400 || !u.Estimated {
		t.Fatalf("cached prompt count must not shrink the context: %+v", u)
	}
}
