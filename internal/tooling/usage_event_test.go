package tooling

import (
	"context"
	"testing"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

type usageProvider struct {
	mockProvider
	reply client.Message
}

func (p *usageProvider) Chat(ctx context.Context, req client.ChatRequest, onEvent func(client.StreamEvent) error) (client.Message, error) {
	return p.reply, nil
}

func TestRunnerEmitsUsagePerModelCall(t *testing.T) {
	cases := []struct {
		name      string
		reply     client.Message
		estimated bool
		cost      string
	}{
		{"gateway", client.Message{Role: "assistant", Content: "done", PromptEvalCount: 50, EvalCount: 7,
			Usage: &client.Usage{PromptTokens: 50, CompletionTokens: 7, CostUSD: 0.01, CostSource: client.CostGateway, SubCalls: 2}}, false, client.CostGateway},
		{"raw counts", client.Message{Role: "assistant", Content: "done", PromptEvalCount: 50, EvalCount: 7}, false, ""},
		{"no usage", client.Message{Role: "assistant", Content: "done"}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &usageProvider{reply: tc.reply}
			r := &Runner{Provider: p, Registry: tools.NewRegistry(p), WorkspaceRoot: t.TempDir()}
			var got []Event
			_, err := r.Run(context.Background(), nil, "hi", agent.Config{Prompt: "sys"}, "tjcoder/ergo-pro:cloud", nil, func(ev Event) {
				if ev.Type == EventUsage {
					got = append(got, ev)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Turn == nil {
				t.Fatalf("want one usage event, got %+v", got)
			}
			turn := got[0].Turn
			if turn.Model != "tjcoder/ergo-pro:cloud" || turn.Estimated != tc.estimated || turn.CostSource != tc.cost {
				t.Fatalf("turn = %+v", turn)
			}
			if !tc.estimated && (turn.PromptTokens != 50 || turn.CompletionTokens != 7) {
				t.Fatalf("tokens = %+v", turn)
			}
			if tc.estimated && turn.PromptTokens == 0 {
				t.Fatalf("estimate fallback missing: %+v", turn)
			}
		})
	}
}
