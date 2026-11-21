package client

// Cost sources for Usage.CostSource.
const (
	// CostGateway is a cost the TJ Coder gateway reported for the request
	// itself (what the wallet is debited), not a client-side estimate.
	CostGateway = "gateway"
	// CostEstimate is tokens × the gateway's published per-model rates
	// (GET /v1/models pricing_cents_per_1m_tokens). The gateway may bill
	// differently (bands, plan multipliers, whole-credit rounding).
	CostEstimate = "estimate"
)

// Usage is the token accounting for one provider response (one model
// call). It is captured by the provider clients and attached to the
// returned Message; Message.PromptEvalCount/EvalCount stay the
// canonical raw counts for context tracking.
type Usage struct {
	PromptTokens       int `json:"prompt_tokens"`
	CompletionTokens   int `json:"completion_tokens"`
	CachedPromptTokens int `json:"cached_prompt_tokens,omitempty"`
	ReasoningTokens    int `json:"reasoning_tokens,omitempty"`
	// Estimated is true when the provider reported no usage and the
	// counts are the chars/4 estimate used for context management.
	Estimated bool `json:"estimated,omitempty"`

	// CostUSD is set only when a price is known: reported by the gateway
	// (CostSource == CostGateway) or derived from gateway-published rates
	// (CostSource == CostEstimate). Local providers never carry a cost.
	CostUSD    float64 `json:"cost_usd,omitempty"`
	CostSource string  `json:"cost_source,omitempty"`

	// Gateway research / multi-step accounting: sub-model calls,
	// web searches and server-side tool calls the gateway ran (and
	// billed) to produce this response. Zero when not reported.
	SubCalls    int `json:"sub_calls,omitempty"`
	SearchCalls int `json:"search_calls,omitempty"`
	ToolCalls   int `json:"tool_calls,omitempty"`
	// ResearchCostUSD is the part of CostUSD attributable to those
	// extra steps, when the gateway breaks it out.
	ResearchCostUSD float64 `json:"research_cost_usd,omitempty"`

	// BalanceCents is the wallet balance after this request when the
	// gateway reports it (nil otherwise). It lets /usage agree with
	// /balance without another round trip.
	BalanceCents *int64 `json:"balance_cents,omitempty"`
}

// Total is prompt + completion tokens.
func (u Usage) Total() int { return u.PromptTokens + u.CompletionTokens }

// HasCost reports whether a price is attached.
func (u Usage) HasCost() bool { return u.CostSource != "" }

// ModelPricing is a model's rate in US cents per one million tokens, as
// published by the gateway. The CLI never hardcodes rates.
type ModelPricing struct {
	PromptCentsPer1M     float64 `json:"prompt"`
	CompletionCentsPer1M float64 `json:"completion"`
}

// Known reports whether any rate is set.
func (p ModelPricing) Known() bool { return p.PromptCentsPer1M > 0 || p.CompletionCentsPer1M > 0 }

// CostUSD prices prompt/completion tokens at p.
func (p ModelPricing) CostUSD(prompt, completion int) float64 {
	cents := float64(prompt)*p.PromptCentsPer1M/1e6 + float64(completion)*p.CompletionCentsPer1M/1e6
	return cents / 100
}

// estimateReplyTokens approximates a reply's completion tokens
// (visible text, thinking and tool-call arguments; chars/4).
func estimateReplyTokens(m Message) int {
	chars := len(m.Content) + len(m.Thinking)
	for _, tc := range m.ToolCalls {
		chars += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	if chars == 0 {
		return 0
	}
	return (chars + 3) / 4
}

// UsageFromMessage returns the usage for reply, a provider response to
// the request messages sent. Provider-reported usage (reply.Usage, or
// the raw PromptEvalCount/EvalCount every client fills) wins; when the
// provider reported nothing the existing chars/4 context estimator is
// used and the result is marked Estimated.
func UsageFromMessage(sent []Message, reply Message) Usage {
	var u Usage
	if reply.Usage != nil {
		u = *reply.Usage
		if u.BalanceCents != nil {
			b := *u.BalanceCents
			u.BalanceCents = &b
		}
	}
	if u.PromptTokens == 0 {
		u.PromptTokens = reply.PromptEvalCount
	}
	if u.CompletionTokens == 0 {
		u.CompletionTokens = reply.EvalCount
	}
	if u.PromptTokens == 0 && u.CompletionTokens == 0 {
		u.PromptTokens = EstimateTokens(sent)
		u.CompletionTokens = estimateReplyTokens(reply)
		u.Estimated = true
		if u.CostSource == CostEstimate {
			u.CostUSD, u.CostSource = 0, ""
		}
	}
	return u
}
