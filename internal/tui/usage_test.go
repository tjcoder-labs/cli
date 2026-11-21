package tui

import (
	"strings"
	"testing"

	"github.com/rivo/tview"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
	"github.com/tjcoder-labs/cli/internal/tooling"
)

func TestUsageSlashCommandRegistered(t *testing.T) {
	found := false
	for _, c := range slashCommands {
		if c.cmd == "usage" {
			found = true
		}
	}
	if !found {
		t.Fatal("/usage missing from the slash palette")
	}
	if slashModeFor([]string{"usage"}) != slashImmediate {
		t.Fatal("/usage must run immediately (read-only)")
	}
}

func TestUsageFooterCounterAndTable(t *testing.T) {
	a := newTestApp(t)
	a.footer = tview.NewTextView().SetDynamicColors(true)

	a.refreshFooter()
	if strings.Contains(a.footer.GetText(true), "tokens") {
		t.Fatal("no counter before the first model call")
	}
	a.recordTurnUsage(session.TurnUsage{Model: "tjcoder/ergo-genius:cloud", Provider: "tjcoder", PromptTokens: 12345, CompletionTokens: 4500, CostUSD: 0.42, CostSource: client.CostGateway, SubCalls: 3})
	if got := a.footer.GetText(true); !strings.Contains(got, "↑ 12.3k ↓ 4.5k tokens") || !strings.Contains(got, "Activity") {
		t.Fatalf("footer = %q", got)
	}

	a.handleSlashCommand("/usage")
	out := a.capturedActivity()
	for _, want := range []string{"ergo-g", "12.3k", "4.5k", "16.8k", "Total", "$0.42", "3 sub-calls", "reported by the TJ Coder"} {
		if !strings.Contains(out, want) {
			t.Errorf("/usage output missing %q:\n%s", want, out)
		}
	}

	// /balance shows the same session spend /usage reports.
	a.observeWalletForUsage(1000)
	a.observeWalletForUsage(958)
	line := a.sessionSpendLine()
	if !strings.Contains(line, "1 request") || !strings.Contains(line, "$0.42 from wallet") {
		t.Fatalf("balance session line = %q", line)
	}
	if !strings.Contains(walletHeader(a.palette, 958, line), "$0.42 from wallet") {
		t.Fatal("wallet header must carry the session spend line")
	}
	if !strings.Contains(a.usageSnapshot().Markdown(), "−$0.42 since this session started") {
		t.Fatal("/usage must report the same wallet spend as /balance")
	}
}

func TestUsageEventPersistsWithSession(t *testing.T) {
	a := newTestApp(t)
	a.footer = tview.NewTextView()
	a.reasoning = tview.NewTextView()
	// handleEvent posts to the (not running) UI loop; call its EventUsage
	// branch directly.
	ev := tooling.Event{Type: tooling.EventUsage, Turn: &session.TurnUsage{Model: "llama3", Provider: "ollama", PromptTokens: 10, CompletionTokens: 5}}
	a.recordTurnUsage(*ev.Turn)
	a.history = []client.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "yo"}}
	if err := a.saveSession(); err != nil {
		t.Fatal(err)
	}
	st, ok, err := session.Load(a.workspaceRoot)
	if err != nil || !ok || st.Usage == nil || st.Usage.Totals().TotalTokens() != 15 {
		t.Fatalf("usage not persisted: ok=%v err=%v usage=%+v", ok, err, st.Usage)
	}
	if st.Usage.Models[0].CostText() != "—" {
		t.Fatal("local Ollama must show tokens only")
	}
}
