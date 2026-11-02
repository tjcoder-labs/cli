package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

// fakeProvider satisfies client.Provider for testing, serving a generic
// model catalogue (both Ollama-style tags and ergo tiers).
type fakeProvider struct {
	models []client.ModelInfo
}

func (f *fakeProvider) Chat(context.Context, client.ChatRequest, func(client.StreamEvent) error) (client.Message, error) {
	return client.Message{}, nil
}
func (f *fakeProvider) ListModels(context.Context) ([]client.ModelInfo, error) {
	if f.models == nil {
		return []client.ModelInfo{
			{Name: "llama3.2"},
			{Name: "gemma3:cloud"},
			{Name: "tjcoder/ergo-flash:cloud"},
			{Name: "tjcoder/ergo-pro:cloud"},
			{Name: "tjcoder/ergo-genius:cloud"},
		}, nil
	}
	return f.models, nil
}
func (f *fakeProvider) ContextWindow(context.Context, string) (int, error) { return 0, nil }
func (f *fakeProvider) BaseURL() string                                     { return "http://localhost:11434" }

func mockEnv(state *session.State) ExecEnv {
	return ExecEnv{
		Provider:       &fakeProvider{},
		SessionState:   state,
		PersistSession: func() error { return nil },
	}
}

func decodeResult(t *testing.T, res Result) ManageModelResult {
	t.Helper()
	var r ManageModelResult
	if err := json.Unmarshal([]byte(res.Content), &r); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	return r
}

func TestManageModelTool(t *testing.T) {
	tool := manageModelTool{}
	if got, want := tool.Definition().Function.Name, "manage_model"; got != want {
		t.Fatalf("expected name %q, got %q", want, got)
	}

	// Explicit model switch to an Ollama tag (any provider).
	state := &session.State{CurrentModel: "llama3.2"}
	res, err := tool.Execute(context.Background(), []byte(`{"model":"gemma3:cloud"}`), mockEnv(state))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := decodeResult(t, res); got.Model != "gemma3:cloud" {
		t.Fatalf("expected gemma3:cloud, got %s", got.Model)
	}
	if state.CurrentModel != "gemma3:cloud" {
		t.Fatalf("session model not updated: %s", state.CurrentModel)
	}

	// Explicit model switch to an ergo tier.
	state.CurrentModel = "llama3.2"
	res, err = tool.Execute(context.Background(), []byte(`{"model":"tjcoder/ergo-pro:cloud"}`), mockEnv(state))
	if err != nil {
		t.Fatalf("unexpected error ergo: %v", err)
	}
	if got := decodeResult(t, res); got.Model != "tjcoder/ergo-pro:cloud" {
		t.Fatalf("expected ergo-pro, got %s", got.Model)
	}

	// Direction up from an ergo tier.
	state.CurrentModel = "tjcoder/ergo-pro:cloud"
	res, err = tool.Execute(context.Background(), []byte(`{"direction":"up"}`), mockEnv(state))
	if err != nil {
		t.Fatalf("unexpected error up: %v", err)
	}
	if got := decodeResult(t, res); got.Model != "tjcoder/ergo-genius:cloud" {
		t.Fatalf("expected genius, got %s", got.Model)
	}
	if state.CurrentModel != "tjcoder/ergo-genius:cloud" {
		t.Fatalf("session not updated for up")
	}

	// Direction down from an ergo tier.
	res, err = tool.Execute(context.Background(), []byte(`{"direction":"down"}`), mockEnv(state))
	if err != nil {
		t.Fatalf("unexpected error down: %v", err)
	}
	if got := decodeResult(t, res); got.Model != "tjcoder/ergo-pro:cloud" {
		t.Fatalf("expected pro, got %s", got.Model)
	}

	// Non-ergo model with no explicit model/direction: kept unchanged.
	state.CurrentModel = "gemma3:cloud"
	res, err = tool.Execute(context.Background(), nil, mockEnv(state))
	if err != nil {
		t.Fatalf("unexpected error noop: %v", err)
	}
	if got := decodeResult(t, res); got.Model != "gemma3:cloud" {
		t.Fatalf("expected unchanged, got %s", got.Model)
	}

	// Think validation: invalid value coerces to auto.
	res, err = tool.Execute(context.Background(), []byte(`{"model":"llama3.2","think":"bogus"}`), mockEnv(state))
	if err != nil {
		t.Fatalf("unexpected error think: %v", err)
	}
	if got := decodeResult(t, res); got.Think != "auto" {
		t.Fatalf("expected think auto, got %s", got.Think)
	}

	// Unknown model is rejected when the catalogue is known.
	if _, err := tool.Execute(context.Background(), []byte(`{"model":"no-such-model"}`), mockEnv(state)); err == nil {
		t.Fatal("expected error for unknown model")
	}
}
