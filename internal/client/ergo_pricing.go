package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ergoPricingWait bounds how long a finished chat waits for the
// background /v1/models pricing fetch (PrefetchPricing, started when the
// provider is set up) before giving up on a cost estimate for it.
var ergoPricingWait = 2 * time.Second

// ergoModelsEntry is the subset of a GET /v1/models entry the CLI reads.
type ergoModelsEntry struct {
	ID       string `json:"id"`
	OwnedBy  string `json:"owned_by"`
	AliasFor string `json:"alias_for"`
	// PricingCentsPer1M is the gateway's published rate (US cents per
	// million tokens). Pricing is owned by the gateway; the CLI never
	// hardcodes it.
	PricingCentsPer1M *ModelPricing `json:"pricing_cents_per_1m_tokens,omitempty"`
}

// storePricing records rates from a /v1/models listing.
func (e *Ergo) storePricing(entries []ergoModelsEntry) {
	rates := map[string]ModelPricing{}
	for _, m := range entries {
		if m.PricingCentsPer1M == nil || !m.PricingCentsPer1M.Known() {
			continue
		}
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		rates[id] = *m.PricingCentsPer1M
		if m.AliasFor != "" {
			rates[strings.TrimSpace(m.AliasFor)] = *m.PricingCentsPer1M
		}
	}
	e.pricingMu.Lock()
	if e.pricing == nil {
		e.pricing = map[string]ModelPricing{}
	}
	for k, v := range rates {
		e.pricing[k] = v
	}
	e.pricingMu.Unlock()
}

// PrefetchPricing loads the gateway's published rates once per client,
// in the background (GET /v1/models). Errors are ignored: no rates just
// means no cost estimate. A chat that finishes while the fetch is in
// flight waits briefly for it. Safe to call repeatedly.
func (e *Ergo) PrefetchPricing() {
	if e == nil || e.apiKey == "" {
		return
	}
	e.pricingOnce.Do(func() {
		done := make(chan struct{})
		e.pricingMu.Lock()
		e.pricingDone = done
		e.pricingMu.Unlock()
		go func() {
			defer close(done)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = e.fetchV1Models(ctx)
		}()
	})
}

// fetchV1Models GETs /v1/models (always the OpenAI-compatible listing,
// even when chat speaks the Ollama protocol) and records its pricing.
func (e *Ergo) fetchV1Models(ctx context.Context) ([]ergoModelsEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	ApplyErgoIdentityHeaders(req)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, &ErgoAuthError{Message: strings.TrimSpace(decodeErgoError(resp.Body))}
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ergo list models: %s", resp.Status)
	}
	var decoded struct {
		Data []ergoModelsEntry `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	e.storePricing(decoded.Data)
	return decoded.Data, nil
}

// PricingFor returns the gateway-published rate for model, if any.
func (e *Ergo) PricingFor(model string) (ModelPricing, bool) {
	e.pricingMu.Lock()
	defer e.pricingMu.Unlock()
	if p, ok := e.pricing[model]; ok {
		return p, true
	}
	if n := NormalizeErgoModel(model); n != model {
		if p, ok := e.pricing[n]; ok {
			return p, true
		}
	}
	return ModelPricing{}, false
}

// annotateUsage fills msg.Usage from the gateway's report and, when the
// gateway gave tokens but no cost, estimates the cost from its rates.
func (e *Ergo) annotateUsage(msg *Message, model string) {
	if msg.Usage == nil {
		if msg.PromptEvalCount == 0 && msg.EvalCount == 0 {
			return // nothing reported; the runner estimates tokens (no cost)
		}
		msg.Usage = &Usage{PromptTokens: msg.PromptEvalCount, CompletionTokens: msg.EvalCount}
	}
	if msg.Usage.HasCost() {
		return
	}
	e.pricingMu.Lock()
	done := e.pricingDone
	e.pricingMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(ergoPricingWait):
		}
	}
	if p, ok := e.PricingFor(model); ok {
		msg.Usage.CostUSD = p.CostUSD(msg.Usage.PromptTokens, msg.Usage.CompletionTokens)
		msg.Usage.CostSource = CostEstimate
	}
}
