package session

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
)

// TurnUsage is the usage of one model call (one provider response) in a
// session. It is the unit the runner reports after every call and the
// unit other consumers (e.g. a transcript sync) can forward as-is.
type TurnUsage struct {
	Model              string `json:"model"`
	Provider           string `json:"provider,omitempty"`
	PromptTokens       int    `json:"prompt_tokens"`
	CompletionTokens   int    `json:"completion_tokens"`
	CachedPromptTokens int    `json:"cached_prompt_tokens,omitempty"`
	ReasoningTokens    int    `json:"reasoning_tokens,omitempty"`
	// Estimated: the provider reported no usage; counts are chars/4.
	Estimated bool `json:"estimated,omitempty"`
	// CostUSD / CostSource: client.CostGateway (gateway-reported),
	// client.CostEstimate (gateway-published rates) or "" (no price).
	CostUSD         float64 `json:"cost_usd,omitempty"`
	CostSource      string  `json:"cost_source,omitempty"`
	SubCalls        int     `json:"sub_calls,omitempty"`
	SearchCalls     int     `json:"search_calls,omitempty"`
	ToolCalls       int     `json:"tool_calls,omitempty"`
	ResearchCostUSD float64 `json:"research_cost_usd,omitempty"`
	// BalanceCents is the wallet balance after the call, when reported.
	BalanceCents *int64 `json:"balance_cents,omitempty"`
	At           string `json:"at,omitempty"` // RFC3339
}

// Total is prompt + completion tokens.
func (t TurnUsage) Total() int { return t.PromptTokens + t.CompletionTokens }

// NewTurnUsage builds a TurnUsage from a provider usage report.
func NewTurnUsage(model, provider string, u client.Usage) TurnUsage {
	return TurnUsage{
		Model:              model,
		Provider:           provider,
		PromptTokens:       u.PromptTokens,
		CompletionTokens:   u.CompletionTokens,
		CachedPromptTokens: u.CachedPromptTokens,
		ReasoningTokens:    u.ReasoningTokens,
		Estimated:          u.Estimated,
		CostUSD:            u.CostUSD,
		CostSource:         u.CostSource,
		SubCalls:           u.SubCalls,
		SearchCalls:        u.SearchCalls,
		ToolCalls:          u.ToolCalls,
		ResearchCostUSD:    u.ResearchCostUSD,
		BalanceCents:       u.BalanceCents,
		At:                 time.Now().UTC().Format(time.RFC3339),
	}
}

// ModelUsage is the per-model (gateway tier or model name) aggregate.
type ModelUsage struct {
	Model              string `json:"model"`
	Provider           string `json:"provider,omitempty"`
	Requests           int    `json:"requests"`
	PromptTokens       int    `json:"prompt_tokens"`
	CompletionTokens   int    `json:"completion_tokens"`
	CachedPromptTokens int    `json:"cached_prompt_tokens,omitempty"`
	ReasoningTokens    int    `json:"reasoning_tokens,omitempty"`
	// EstimatedRequests counts calls whose tokens were estimated.
	EstimatedRequests int `json:"estimated_requests,omitempty"`
	// GatewayCostUSD sums gateway-reported costs; EstimatedCostUSD sums
	// estimates from gateway rates; UnpricedRequests counts calls with
	// no price at all (local models, unpriced gateway models).
	GatewayCostUSD   float64 `json:"gateway_cost_usd,omitempty"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd,omitempty"`
	GatewayPriced    int     `json:"gateway_priced_requests,omitempty"`
	EstimatePriced   int     `json:"estimate_priced_requests,omitempty"`
	UnpricedRequests int     `json:"unpriced_requests,omitempty"`
	SubCalls         int     `json:"sub_calls,omitempty"`
	SearchCalls      int     `json:"search_calls,omitempty"`
	ToolCalls        int     `json:"tool_calls,omitempty"`
	ResearchCostUSD  float64 `json:"research_cost_usd,omitempty"`
}

// TotalTokens is prompt + completion tokens.
func (m ModelUsage) TotalTokens() int { return m.PromptTokens + m.CompletionTokens }

// CostUSD is the best known cost (gateway-reported plus estimates).
func (m ModelUsage) CostUSD() float64 { return roundUSD(m.GatewayCostUSD + m.EstimatedCostUSD) }

// Priced reports whether any request carried a price.
func (m ModelUsage) Priced() bool { return m.GatewayPriced+m.EstimatePriced > 0 }

// CostIsEstimate reports whether any part of CostUSD is an estimate.
func (m ModelUsage) CostIsEstimate() bool { return m.EstimatePriced > 0 }

func (m *ModelUsage) add(t TurnUsage) {
	m.Requests++
	m.PromptTokens += t.PromptTokens
	m.CompletionTokens += t.CompletionTokens
	m.CachedPromptTokens += t.CachedPromptTokens
	m.ReasoningTokens += t.ReasoningTokens
	if t.Estimated {
		m.EstimatedRequests++
	}
	switch t.CostSource {
	case client.CostGateway:
		m.GatewayCostUSD = roundUSD(m.GatewayCostUSD + t.CostUSD)
		m.GatewayPriced++
	case client.CostEstimate:
		m.EstimatedCostUSD = roundUSD(m.EstimatedCostUSD + t.CostUSD)
		m.EstimatePriced++
	default:
		m.UnpricedRequests++
	}
	m.SubCalls += t.SubCalls
	m.SearchCalls += t.SearchCalls
	m.ToolCalls += t.ToolCalls
	m.ResearchCostUSD = roundUSD(m.ResearchCostUSD + t.ResearchCostUSD)
}

func (m *ModelUsage) merge(o ModelUsage) {
	m.Requests += o.Requests
	m.PromptTokens += o.PromptTokens
	m.CompletionTokens += o.CompletionTokens
	m.CachedPromptTokens += o.CachedPromptTokens
	m.ReasoningTokens += o.ReasoningTokens
	m.EstimatedRequests += o.EstimatedRequests
	m.GatewayCostUSD = roundUSD(m.GatewayCostUSD + o.GatewayCostUSD)
	m.EstimatedCostUSD = roundUSD(m.EstimatedCostUSD + o.EstimatedCostUSD)
	m.GatewayPriced += o.GatewayPriced
	m.EstimatePriced += o.EstimatePriced
	m.UnpricedRequests += o.UnpricedRequests
	m.SubCalls += o.SubCalls
	m.SearchCalls += o.SearchCalls
	m.ToolCalls += o.ToolCalls
	m.ResearchCostUSD = roundUSD(m.ResearchCostUSD + o.ResearchCostUSD)
}

// UsageStats is the session's cumulative token usage, persisted in the
// session file under "usage" (absent in pre-usage session files, which
// load as zero usage).
type UsageStats struct {
	// Models is one row per model id, in first-use order.
	Models []ModelUsage `json:"models,omitempty"`
	// WalletStartCents is the first wallet balance seen in this session
	// (from /v1/wallet/balance or a gateway usage report);
	// WalletLastCents is the latest. Their difference is exactly what
	// /balance shows the session spent.
	WalletStartCents *int64 `json:"wallet_start_cents,omitempty"`
	WalletLastCents  *int64 `json:"wallet_last_cents,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

// Add folds one call into the stats. s must be non-nil (use
// State.EnsureUsage).
func (s *UsageStats) Add(t TurnUsage) {
	model := strings.TrimSpace(t.Model)
	if model == "" {
		model = "unknown"
	}
	idx := -1
	for i := range s.Models {
		if s.Models[i].Model == model {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.Models = append(s.Models, ModelUsage{Model: model, Provider: t.Provider})
		idx = len(s.Models) - 1
	}
	s.Models[idx].add(t)
	if t.BalanceCents != nil {
		s.ObserveWallet(*t.BalanceCents)
	}
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// ObserveWallet records a wallet balance reading.
func (s *UsageStats) ObserveWallet(cents int64) {
	if s.WalletStartCents == nil {
		start := cents
		s.WalletStartCents = &start
	}
	last := cents
	s.WalletLastCents = &last
}

// WalletSpentCents is the wallet decrease since the session's first
// reading (top-ups during the session make it negative or smaller).
func (s *UsageStats) WalletSpentCents() (int64, bool) {
	if s == nil || s.WalletStartCents == nil || s.WalletLastCents == nil {
		return 0, false
	}
	return *s.WalletStartCents - *s.WalletLastCents, true
}

// Totals sums every model row (Model = "Total").
func (s *UsageStats) Totals() ModelUsage {
	t := ModelUsage{Model: "Total"}
	if s == nil {
		return t
	}
	for _, m := range s.Models {
		t.merge(m)
	}
	return t
}

// Empty reports whether nothing has been recorded.
func (s *UsageStats) Empty() bool { return s == nil || len(s.Models) == 0 }

// Clone deep-copies s (nil stays nil).
func (s *UsageStats) Clone() *UsageStats {
	if s == nil {
		return nil
	}
	c := *s
	c.Models = append([]ModelUsage(nil), s.Models...)
	if s.WalletStartCents != nil {
		v := *s.WalletStartCents
		c.WalletStartCents = &v
	}
	if s.WalletLastCents != nil {
		v := *s.WalletLastCents
		c.WalletLastCents = &v
	}
	return &c
}

// EnsureUsage returns st.Usage, allocating it on first use.
func (st *State) EnsureUsage() *UsageStats {
	if st.Usage == nil {
		st.Usage = &UsageStats{}
	}
	return st.Usage
}

// FormatTokens renders a token count compactly: 950, 12.3k, 4.5M.
func FormatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1000)) + "k"
	default:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1_000_000)) + "M"
	}
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

// FormatUSD renders a cost with enough precision for sub-cent calls.
func FormatUSD(v float64) string {
	switch {
	case v == 0:
		return "$0.00"
	case math.Abs(v) < 0.01:
		return fmt.Sprintf("$%.4f", v)
	default:
		return fmt.Sprintf("$%.2f", v)
	}
}

// CostText renders m's cost for a table cell: "$0.42" when every priced
// call was gateway-reported, "~$0.42" when any part is an estimate, and
// "—" when nothing was priced (e.g. local Ollama: tokens only).
func (m ModelUsage) CostText() string {
	if !m.Priced() {
		return "—"
	}
	s := FormatUSD(m.CostUSD())
	if m.CostIsEstimate() {
		s = "~" + s
	}
	return s
}

// ModelLabel is the /usage row label: gateway tiers drop the
// "tjcoder/" prefix and ":cloud" suffix (tjcoder/ergo-genius:cloud →
// ergo-genius); other ids are shown as-is.
func ModelLabel(model string) string {
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "tjcoder/") {
		return strings.TrimSuffix(model[len("tjcoder/"):], ":cloud")
	}
	return model
}

func tokCell(n int, estimated bool) string {
	s := FormatTokens(n)
	if estimated {
		s = "~" + s
	}
	return s
}

// CounterText is the compact footer counter, e.g. "↑ 12.3k ↓ 4.5k tokens"
// ("" before the first call). A leading "~" marks estimated counts.
func (s *UsageStats) CounterText() string {
	if s.Empty() {
		return ""
	}
	t := s.Totals()
	approx := ""
	if t.EstimatedRequests > 0 {
		approx = "~"
	}
	return fmt.Sprintf("↑ %s%s ↓ %s%s tokens", approx, FormatTokens(t.PromptTokens), approx, FormatTokens(t.CompletionTokens))
}

// Markdown renders the /usage table for the session.
func (s *UsageStats) Markdown() string {
	if s.Empty() {
		return "No token usage recorded in this session yet."
	}
	tot := s.Totals()
	showCost := tot.Priced()
	var b strings.Builder
	b.WriteString("| Model | Requests | Prompt | Completion | Total |")
	if showCost {
		b.WriteString(" Cost |")
	}
	b.WriteString("\n|-------|---------:|-------:|-----------:|------:|")
	if showCost {
		b.WriteString("-----:|")
	}
	b.WriteString("\n")
	row := func(label string, m ModelUsage, bold bool) {
		est := m.EstimatedRequests > 0
		if bold {
			label = "**" + label + "**"
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s |", label, m.Requests,
			tokCell(m.PromptTokens, est), tokCell(m.CompletionTokens, est), tokCell(m.TotalTokens(), est))
		if showCost {
			fmt.Fprintf(&b, " %s |", m.CostText())
		}
		b.WriteString("\n")
	}
	for _, m := range s.Models {
		row(ModelLabel(m.Model), m, false)
	}
	row("Total", tot, true)

	var notes []string
	if tot.SubCalls+tot.SearchCalls+tot.ToolCalls > 0 {
		n := fmt.Sprintf("Gateway research steps: %d sub-calls, %d searches, %d tool calls", tot.SubCalls, tot.SearchCalls, tot.ToolCalls)
		if tot.ResearchCostUSD > 0 {
			n += " (" + FormatUSD(tot.ResearchCostUSD) + " of the cost)"
		}
		notes = append(notes, n+".")
	}
	if tot.EstimatedRequests > 0 {
		notes = append(notes, fmt.Sprintf("~ %d of %d requests had no provider usage; tokens are estimated.", tot.EstimatedRequests, tot.Requests))
	}
	if showCost {
		switch {
		case tot.EstimatePriced > 0 && tot.GatewayPriced > 0:
			notes = append(notes, "Cost: gateway-reported where available; ~ marks estimates from the gateway's published rates.")
		case tot.EstimatePriced > 0:
			notes = append(notes, "~ Cost is an estimate from the gateway's published rates; the wallet is the source of truth.")
		default:
			notes = append(notes, "Cost is as reported by the TJ Coder gateway.")
		}
		if tot.UnpricedRequests > 0 {
			notes = append(notes, "— no price: local or unpriced models (tokens only).")
		}
	}
	if spent, ok := s.WalletSpentCents(); ok {
		notes = append(notes, fmt.Sprintf("Wallet: %s now, %s since this session started (same figure /balance shows).",
			FormatUSD(float64(*s.WalletLastCents)/100), signedUSD(spent)))
	}
	if len(notes) > 0 {
		b.WriteString("\n")
		for _, n := range notes {
			b.WriteString(n + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func signedUSD(spentCents int64) string {
	if spentCents >= 0 {
		return "−" + FormatUSD(float64(spentCents)/100)
	}
	return "+" + FormatUSD(float64(-spentCents)/100)
}

// SummaryLine is the one-line headless summary written to stderr by
// `coder ask --usage`.
func (s *UsageStats) SummaryLine() string {
	if s.Empty() {
		return "usage: n/a (no provider usage reported)"
	}
	t := s.Totals()
	approx := ""
	if t.EstimatedRequests > 0 {
		approx = "~"
	}
	line := fmt.Sprintf("usage: ↑ %s%s ↓ %s%s tokens (%s%s total, %d request", approx, FormatTokens(t.PromptTokens),
		approx, FormatTokens(t.CompletionTokens), approx, FormatTokens(t.TotalTokens()), t.Requests)
	if t.Requests != 1 {
		line += "s"
	}
	line += ")"
	if t.Priced() {
		kind := "gateway"
		if t.CostIsEstimate() {
			kind = "estimate"
		}
		line += fmt.Sprintf(" · cost %s (%s)", t.CostText(), kind)
	}
	return line
}

func roundUSD(v float64) float64 { return math.Round(v*1e8) / 1e8 }

// UsageReport is the machine-readable usage summary (the `usage` object
// in `coder ask --format json`).
type UsageReport struct {
	Requests         int     `json:"requests"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Estimated        bool    `json:"estimated,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	// CostSource is "gateway", "estimate", "mixed", or omitted when no
	// call was priced (local models: tokens only).
	CostSource  string             `json:"cost_source,omitempty"`
	SubCalls    int                `json:"sub_calls,omitempty"`
	SearchCalls int                `json:"search_calls,omitempty"`
	ToolCalls   int                `json:"tool_calls,omitempty"`
	ByModel     []UsageReportModel `json:"by_model,omitempty"`
	// Session is the cumulative session total (after this run), set
	// when the run is part of a persisted session.
	Session *UsageReport `json:"session,omitempty"`
	// WalletSpentCents is the wallet decrease since the session's first
	// balance reading, when known (matches /balance).
	WalletSpentCents *int64 `json:"wallet_spent_cents,omitempty"`
}

// UsageReportModel is one by_model entry.
type UsageReportModel struct {
	Model            string  `json:"model"`
	Label            string  `json:"label,omitempty"`
	Provider         string  `json:"provider,omitempty"`
	Requests         int     `json:"requests"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Estimated        bool    `json:"estimated,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	CostSource       string  `json:"cost_source,omitempty"`
	SubCalls         int     `json:"sub_calls,omitempty"`
	SearchCalls      int     `json:"search_calls,omitempty"`
	ToolCalls        int     `json:"tool_calls,omitempty"`
}

func costSource(m ModelUsage) string {
	switch {
	case m.GatewayPriced > 0 && m.EstimatePriced > 0:
		return "mixed"
	case m.GatewayPriced > 0:
		return client.CostGateway
	case m.EstimatePriced > 0:
		return client.CostEstimate
	}
	return ""
}

// Report builds the machine-readable summary of s.
func (s *UsageStats) Report() UsageReport {
	t := s.Totals()
	r := UsageReport{
		Requests: t.Requests, PromptTokens: t.PromptTokens, CompletionTokens: t.CompletionTokens,
		TotalTokens: t.TotalTokens(), Estimated: t.EstimatedRequests > 0,
		CostSource: costSource(t), SubCalls: t.SubCalls, SearchCalls: t.SearchCalls, ToolCalls: t.ToolCalls,
	}
	if t.Priced() {
		r.CostUSD = t.CostUSD()
	}
	if s != nil {
		for _, m := range s.Models {
			e := UsageReportModel{
				Model: m.Model, Label: ModelLabel(m.Model), Provider: m.Provider, Requests: m.Requests,
				PromptTokens: m.PromptTokens, CompletionTokens: m.CompletionTokens, TotalTokens: m.TotalTokens(),
				Estimated: m.EstimatedRequests > 0, CostSource: costSource(m),
				SubCalls: m.SubCalls, SearchCalls: m.SearchCalls, ToolCalls: m.ToolCalls,
			}
			if m.Priced() {
				e.CostUSD = m.CostUSD()
			}
			r.ByModel = append(r.ByModel, e)
		}
		if spent, ok := s.WalletSpentCents(); ok {
			r.WalletSpentCents = &spent
		}
	}
	return r
}
