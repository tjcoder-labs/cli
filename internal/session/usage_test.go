package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
)

func TestUsageStatsAggregatesByModel(t *testing.T) {
	var s UsageStats
	s.Add(TurnUsage{Model: "tjcoder/ergo-flash:cloud", Provider: "tjcoder", PromptTokens: 1000, CompletionTokens: 200, CostUSD: 0.01, CostSource: client.CostGateway})
	s.Add(TurnUsage{Model: "tjcoder/ergo-genius:cloud", Provider: "tjcoder", PromptTokens: 5000, CompletionTokens: 800, CostUSD: 0.05, CostSource: client.CostEstimate, SubCalls: 3, SearchCalls: 2})
	s.Add(TurnUsage{Model: "tjcoder/ergo-flash:cloud", Provider: "tjcoder", PromptTokens: 1500, CompletionTokens: 300, CostUSD: 0.02, CostSource: client.CostGateway})
	s.Add(TurnUsage{Model: "llama3", Provider: "ollama", PromptTokens: 40, CompletionTokens: 10, Estimated: true})

	if len(s.Models) != 3 {
		t.Fatalf("models = %d, want 3 (first-use order)", len(s.Models))
	}
	flash := s.Models[0]
	if flash.Model != "tjcoder/ergo-flash:cloud" || flash.Requests != 2 || flash.PromptTokens != 2500 || flash.CompletionTokens != 500 {
		t.Fatalf("flash row = %+v", flash)
	}
	if got := flash.CostText(); got != "$0.03" {
		t.Fatalf("flash cost = %q, want gateway $0.03", got)
	}
	if got := s.Models[1].CostText(); got != "~$0.05" {
		t.Fatalf("genius cost = %q, want estimate ~$0.05", got)
	}
	if got := s.Models[2].CostText(); got != "—" {
		t.Fatalf("ollama cost = %q, want tokens-only —", got)
	}
	tot := s.Totals()
	if tot.Requests != 4 || tot.PromptTokens != 7540 || tot.CompletionTokens != 1310 || tot.TotalTokens() != 8850 {
		t.Fatalf("totals = %+v", tot)
	}
	if tot.EstimatedRequests != 1 || tot.UnpricedRequests != 1 || tot.SubCalls != 3 || tot.SearchCalls != 2 {
		t.Fatalf("totals flags = %+v", tot)
	}
	if got := tot.CostUSD(); got != 0.08 {
		t.Fatalf("total cost = %v, want 0.08", got)
	}
}

func TestUsageCounterAndFormat(t *testing.T) {
	var s *UsageStats
	if s.CounterText() != "" {
		t.Fatal("empty stats must render no counter")
	}
	s = &UsageStats{}
	s.Add(TurnUsage{Model: "m", PromptTokens: 12345, CompletionTokens: 4500})
	if got := s.CounterText(); got != "↑ 12.3k ↓ 4.5k tokens" {
		t.Fatalf("counter = %q", got)
	}
	s.Add(TurnUsage{Model: "m", PromptTokens: 1, CompletionTokens: 1, Estimated: true})
	if got := s.CounterText(); !strings.HasPrefix(got, "↑ ~") {
		t.Fatalf("estimated counter = %q, want ~ marker", got)
	}
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1k", 59200: "59.2k", 2_500_000: "2.5M"} {
		if got := FormatTokens(n); got != want {
			t.Errorf("FormatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestUsageMarkdownTable(t *testing.T) {
	var s UsageStats
	s.Add(TurnUsage{Model: "tjcoder/ergo-flash:cloud", PromptTokens: 48200, CompletionTokens: 6100})
	s.Add(TurnUsage{Model: "tjcoder/ergo-pro:cloud", PromptTokens: 11000, CompletionTokens: 1200})
	md := s.Markdown()
	for _, want := range []string{
		"| Model | Requests | Prompt | Completion | Total |",
		"|-------|---------:|-------:|-----------:|------:|",
		"| ergo-flash | 1 | 48.2k | 6.1k | 54.3k |",
		"| ergo-pro | 1 | 11k | 1.2k | 12.2k |",
		"| **Total** | 2 | 59.2k | 7.3k | 66.5k |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Cost") {
		t.Errorf("unpriced usage must not show a Cost column:\n%s", md)
	}

	// Gateway pricing adds a Cost column; research steps and the wallet
	// figure (shared with /balance) are noted under the table.
	s.Add(TurnUsage{Model: "tjcoder/ergo-genius:cloud", PromptTokens: 1000, CompletionTokens: 100, CostUSD: 0.42, CostSource: client.CostGateway, SubCalls: 4, SearchCalls: 2, ResearchCostUSD: 0.1})
	s.ObserveWallet(1000)
	s.ObserveWallet(958)
	md = s.Markdown()
	for _, want := range []string{"Cost |", "| ergo-genius | 1 | 1k | 100 | 1.1k | $0.42 |", "4 sub-calls, 2 searches", "$9.58 now, −$0.42 since this session started"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if got := (&UsageStats{}).Markdown(); !strings.Contains(got, "No token usage") {
		t.Errorf("empty markdown = %q", got)
	}
}

func TestUsagePersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	st := State{CurrentModel: "tjcoder/ergo-pro:cloud"}
	st.EnsureUsage().Add(TurnUsage{Model: "tjcoder/ergo-pro:cloud", Provider: "tjcoder", PromptTokens: 100, CompletionTokens: 20, CostUSD: 0.001, CostSource: client.CostGateway})
	st.Usage.ObserveWallet(500)
	if err := SaveFile(path, st); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadFile(path)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Usage == nil || len(got.Usage.Models) != 1 {
		t.Fatalf("usage not restored: %+v", got.Usage)
	}
	m := got.Usage.Models[0]
	if m.Requests != 1 || m.PromptTokens != 100 || m.CompletionTokens != 20 || m.GatewayCostUSD != 0.001 || m.GatewayPriced != 1 {
		t.Fatalf("restored row = %+v", m)
	}
	if got.Usage.WalletStartCents == nil || *got.Usage.WalletStartCents != 500 {
		t.Fatalf("wallet start not restored")
	}
	// Resume keeps accumulating on top of the stored totals.
	got.EnsureUsage().Add(TurnUsage{Model: "tjcoder/ergo-pro:cloud", PromptTokens: 50, CompletionTokens: 5})
	if tot := got.Usage.Totals(); tot.Requests != 2 || tot.PromptTokens != 150 {
		t.Fatalf("resumed totals = %+v", tot)
	}
}

func TestUsageOldSessionFileBackwardCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	old := `{"current_agent":"software-engineer","current_model":"llama3","enabled_tools":[],"history":[{"role":"user","content":"hi"}],"transcript":[]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, ok, err := LoadFile(path)
	if err != nil || !ok {
		t.Fatalf("load old file: ok=%v err=%v", ok, err)
	}
	if st.Usage != nil {
		t.Fatalf("old file must load with nil usage, got %+v", st.Usage)
	}
	if !st.Usage.Empty() || st.Usage.Totals().Requests != 0 {
		t.Fatal("nil usage must read as empty")
	}
	// Saving without usage keeps the key out of the file.
	if err := SaveFile(path, st); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	if _, has := raw["usage"]; has {
		t.Fatal("usage key written for a session without usage")
	}
}

func TestUsageReport(t *testing.T) {
	var s UsageStats
	s.Add(TurnUsage{Model: "tjcoder/ergo-genius:cloud", Provider: "tjcoder", PromptTokens: 10, CompletionTokens: 5, CostUSD: 0.01, CostSource: client.CostGateway, SubCalls: 2})
	s.Add(TurnUsage{Model: "llama3", Provider: "ollama", PromptTokens: 3, CompletionTokens: 2})
	r := s.Report()
	if r.Requests != 2 || r.TotalTokens != 20 || r.CostUSD != 0.01 || r.CostSource != "gateway" || r.SubCalls != 2 {
		t.Fatalf("report = %+v", r)
	}
	if len(r.ByModel) != 2 || r.ByModel[0].Label != "ergo-genius" || r.ByModel[1].CostSource != "" || r.ByModel[1].CostUSD != 0 {
		t.Fatalf("by_model = %+v", r.ByModel)
	}
	if line := s.SummaryLine(); !strings.Contains(line, "↑ 13 ↓ 7 tokens (20 total, 2 requests)") || !strings.Contains(line, "cost $0.01 (gateway)") {
		t.Fatalf("summary = %q", line)
	}
}
