package tui

import (
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
)

func TestContextStatusAndTipShareOneComputation(t *testing.T) {
	a := newCompactTipApp(t)
	a.ctxUsage = client.ContextUsage{Used: 79400, Window: 128000, Model: "gpt-4o"}
	status := plain(a.contextStatusText())
	if !strings.Contains(status, "62% of 128K tokens.") {
		t.Fatalf("status = %q", status)
	}
	if tip := a.compactTip("compact"); !strings.Contains(tip, "context 62% of 128k") {
		t.Fatalf("tip = %q disagrees with status %q", tip, status)
	}
}

func TestContextModelSwitchRecomputesWindow(t *testing.T) {
	a := newCompactTipApp(t)
	a.contextWindows["claude-opus-4"] = 200000
	a.ctxUsage = client.ContextUsage{Used: 64000, Window: 128000, Model: "gpt-4o"}
	if s := plain(a.contextStatusText()); !strings.Contains(s, "50% of 128K") {
		t.Fatalf("before switch: %q", s)
	}
	a.currentModel = "claude-opus-4"
	if s := plain(a.contextStatusText()); !strings.Contains(s, "~32% of 200K") {
		t.Fatalf("after switch (new window, estimated): %q", s)
	}
	// Unknown window: explicit, never the previous model's total.
	a.currentModel = "ergo-lite"
	s := plain(a.contextStatusText())
	if !strings.Contains(s, "window unknown") || strings.Contains(s, "%") || strings.Contains(s, "200K") {
		t.Fatalf("unknown window: %q", s)
	}
}

func TestContextCompactionAndClearReset(t *testing.T) {
	a := newCompactTipApp(t)
	a.history = make([]client.Message, 10)
	a.ctxUsage = client.ContextUsage{Used: 120000, Window: 128000, Overhead: 2000, Messages: 10, Model: "gpt-4o"}
	if s := plain(a.contextStatusText()); !strings.Contains(s, "94%") {
		t.Fatalf("pre-compact: %q", s)
	}
	a.history = []client.Message{{Role: "user", Content: "x"}, {Role: "assistant", Content: strings.Repeat("y", 7956)}}
	a.resetContextUsage()
	u := a.contextUsageFor("gpt-4o")
	if want := 2000 + client.EstimateTokens(a.history); u.Used != want || !u.Estimated {
		t.Fatalf("post-compact usage = %+v, want Used=%d estimated", u, want)
	}
	if s := plain(a.contextStatusText()); !strings.Contains(s, "~3% of 128K") {
		t.Fatalf("post-compact: %q", s)
	}
	a.history = nil
	a.ctxUsage = client.ContextUsage{}
	if s := a.contextStatusText(); s != "" {
		t.Fatalf("after /clear the indicator must hide, got %q", s)
	}
}

func TestContextPendingMessagesAreEstimatedOnTopOfReported(t *testing.T) {
	a := newCompactTipApp(t)
	a.history = make([]client.Message, 4)
	a.ctxUsage = client.ContextUsage{Used: 10000, Window: 128000, Messages: 4, Model: "gpt-4o"}
	if u := a.contextUsageFor("gpt-4o"); u.Used != 10000 || u.Estimated {
		t.Fatalf("reported reading must be used as-is: %+v", u)
	}
	extra := client.Message{Role: "user", Content: strings.Repeat("z", 400)}
	a.history = append(a.history, extra)
	if u := a.contextUsageFor("gpt-4o"); u.Used != 10000+client.EstimateTokens([]client.Message{extra}) || !u.Estimated {
		t.Fatalf("pending part only must be estimated: %+v", u)
	}
}

func TestContextOverLimitShownClearly(t *testing.T) {
	a := newCompactTipApp(t)
	a.ctxUsage = client.ContextUsage{Used: 140800, Window: 128000, Model: "gpt-4o"}
	s := plain(a.contextStatusText())
	if !strings.Contains(s, "110% of 128K") || !strings.Contains(s, "over limit") {
		t.Fatalf("over: %q", s)
	}
	if tip := a.compactTip("compact"); !strings.Contains(tip, "110% of 128k (over limit)") {
		t.Fatalf("tip over: %q", tip)
	}
}

func TestContextPercentRounding(t *testing.T) {
	cases := []struct {
		u    client.ContextUsage
		want string
	}{
		{client.ContextUsage{Used: 50, Window: 128000}, "<1%"},
		{client.ContextUsage{Used: 127999, Window: 128000}, "99%"},
		{client.ContextUsage{Used: 128000, Window: 128000}, "100%"},
		{client.ContextUsage{Used: 128001, Window: 128000}, "101%"},
		{client.ContextUsage{Used: 79400, Window: 128000, Estimated: true}, "~62%"},
		{client.ContextUsage{Used: 12000}, "12K"},
	}
	for _, c := range cases {
		if got := contextPercentText(c.u); got != c.want {
			t.Errorf("%+v -> %q, want %q", c.u, got, c.want)
		}
	}
}
