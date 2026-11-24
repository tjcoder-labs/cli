package client

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsageFromMessagePrefersProviderCounts(t *testing.T) {
	sent := []Message{{Role: "user", Content: strings.Repeat("x", 400)}}
	u := UsageFromMessage(sent, Message{Role: "assistant", Content: "ok", PromptEvalCount: 120, EvalCount: 30})
	if u.PromptTokens != 120 || u.CompletionTokens != 30 || u.Estimated {
		t.Fatalf("provider counts not used: %+v", u)
	}
	// No usage reported: fall back to the context estimator, marked.
	u = UsageFromMessage(sent, Message{Role: "assistant", Content: strings.Repeat("y", 40)})
	if !u.Estimated || u.PromptTokens != EstimateTokens(sent) || u.CompletionTokens != 10 {
		t.Fatalf("estimate fallback = %+v", u)
	}
	if u.HasCost() {
		t.Fatal("estimated tokens must not carry a cost")
	}
}

func TestErgoUsageParsesGatewayCostAndResearch(t *testing.T) {
	raw := `{"prompt_tokens":900,"completion_tokens":100,"total_tokens":1000,
		"completion_tokens_details":{"reasoning_tokens":40},
		"prompt_tokens_details":{"cached_tokens":200},
		"ergo":{"billed_cents":3,"cost_cents":2.4,"sub_calls":5,"search_calls":2,"tool_calls":1,"research_cost_cents":1.5,"balance_cents":997}}`
	var eu ergoUsage
	if err := json.Unmarshal([]byte(raw), &eu); err != nil {
		t.Fatal(err)
	}
	u := eu.toUsage()
	if u.CostSource != CostGateway || u.CostUSD != 0.03 {
		t.Fatalf("billed cents must win as gateway cost: %+v", u)
	}
	if u.SubCalls != 5 || u.SearchCalls != 2 || u.ToolCalls != 1 || u.ResearchCostUSD != 0.015 {
		t.Fatalf("research fields = %+v", u)
	}
	if u.ReasoningTokens != 40 || u.CachedPromptTokens != 200 || u.BalanceCents == nil || *u.BalanceCents != 997 {
		t.Fatalf("details = %+v", u)
	}
	// OpenRouter-style top-level cost (USD).
	var or ergoUsage
	_ = json.Unmarshal([]byte(`{"prompt_tokens":1,"completion_tokens":1,"cost":0.002}`), &or)
	if u := or.toUsage(); u.CostSource != CostGateway || u.CostUSD != 0.002 {
		t.Fatalf("cost usd = %+v", u)
	}
	// Tokens only: no cost attached by the parser.
	var plain ergoUsage
	_ = json.Unmarshal([]byte(`{"prompt_tokens":1,"completion_tokens":1}`), &plain)
	if u := plain.toUsage(); u.HasCost() {
		t.Fatalf("plain usage must not carry a cost: %+v", u)
	}
}

func ergoUsageServer(t *testing.T, usageJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[
				{"id":"tjcoder/ergo-genius:cloud","pricing_cents_per_1m_tokens":{"prompt":300,"completion":1500}},
				{"id":"tjcoder/ergo-flash:cloud"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
				"data: {\"choices\":[],\"usage\":" + usageJSON + "}\n\ndata: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestErgoChatEstimatesCostFromGatewayRates(t *testing.T) {
	srv := ergoUsageServer(t, `{"prompt_tokens":1000000,"completion_tokens":100000,"total_tokens":1100000}`)
	defer srv.Close()
	e := NewErgo(srv.URL, "k", 0)
	e.PrefetchPricing()
	msg, err := e.Chat(context.Background(), ChatRequest{Model: "tjcoder/ergo-genius:cloud", Messages: []Message{{Role: "user", Content: "q"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Usage == nil || msg.Usage.CostSource != CostEstimate {
		t.Fatalf("want estimate from published rate, got %+v", msg.Usage)
	}
	// 1M prompt × 300¢ + 0.1M completion × 1500¢ = 450¢ = $4.50
	if math.Abs(msg.Usage.CostUSD-4.5) > 1e-9 {
		t.Fatalf("cost = %v, want 4.5", msg.Usage.CostUSD)
	}
	// A model without a published rate gets tokens only.
	msg, _ = e.Chat(context.Background(), ChatRequest{Model: "tjcoder/ergo-flash:cloud"}, nil)
	if msg.Usage == nil || msg.Usage.HasCost() {
		t.Fatalf("unpriced model must carry no cost: %+v", msg.Usage)
	}
}

func TestErgoChatPrefersGatewayReportedCost(t *testing.T) {
	srv := ergoUsageServer(t, `{"prompt_tokens":1000000,"completion_tokens":100000,"total_tokens":1100000,"cost_cents":12.5,"ergo":{"sub_calls":3}}`)
	defer srv.Close()
	e := NewErgo(srv.URL, "k", 0)
	e.PrefetchPricing()
	msg, err := e.Chat(context.Background(), ChatRequest{Model: "tjcoder/ergo-genius:cloud"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Usage == nil || msg.Usage.CostSource != CostGateway || msg.Usage.CostUSD != 0.125 || msg.Usage.SubCalls != 3 {
		t.Fatalf("gateway cost must win over the rate estimate: %+v", msg.Usage)
	}
	if msg.PromptEvalCount != 1000000 || msg.EvalCount != 100000 {
		t.Fatalf("raw counts lost: %+v", msg)
	}
}
