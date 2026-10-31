package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

// Mock provider that satisfies client.Provider but returns fake ergo.
type mockErgoProvider struct {
	client.Provider
	authenticated bool
	model         string
}

func (m *mockErgoProvider) Authenticated() bool { return m.authenticated }
func (m *mockErgoProvider) Chat(context.Context, client.ChatRequest, func(client.StreamEvent) error) (*client.ChatResponse, error) {
	return nil, nil
}
func (m *mockErgoProvider) ListAllModels(context.Context) ([]client.ModelInfo, error) {
	return []client.ModelInfo{
		{Name: "tjcoder/ergo-flash:cloud", Family: "tjcoder"},
		{Name: "tjcoder/ergo-pro:cloud", Family: "tjcoder"},
		{Name: "tjcoder/ergo-genius:cloud", Family: "tjcoder"},
	}, nil
}
func (m *mockErgoProvider) ListModels(context.Context) ([]client.ModelInfo, error) { return nil, nil }
func (m *mockErgoProvider) Key() string { return "tjcoder" }

func TestManageModelTool(t *testing.T) {
	tool := manageModelTool{}
	if got, want := tool.Definition().Function.Name, "manage_model"; got != want {
		t.Fatalf("expected name %q, got %q", want, got)
	}

	// Test unauthenticated ergo
	state := &session.State{}
	mock := &mockErgoProvider{authenticated: false}
	_, err := tool.Execute(context.Background(), nil, client.ExecEnv{
		Provider: mock,
		SessionState: state,
	})
	if err == nil {
		t.Fatal("expected error for unauthenticated ergo")
	}

	// Authenticated, default case (should keep current model)
	mock.authenticated = true
	state.CurrentModel = "tjcoder/ergo-pro:cloud"
	res, err := tool.Execute(context.Background(), nil, client.ExecEnv{
		Provider: mock,
		SessionState: state,
		PersistSession: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result struct {
		Model    string `json:"model"`
		Think    string `json:"think"`
		AutoTier bool   `json:"auto_tier"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal([]byte(res.Content), &result); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if result.Model != "tjcoder/ergo-pro:cloud" {
		t.Fatalf("expected model unchanged, got %s", result.Model)
	}
	if result.Think != "auto" {
		t.Fatalf("expected think auto, got %s", result.Think)
	}
	if result.AutoTier {
		t.Fatalf("expected autoTier false, got %t", result.AutoTier)
	}

	// Test direction up
	res, err = tool.Execute(context.Background(), []byte(`{"direction":"up"}`), client.ExecEnv{
		Provider: mock,
		SessionState: state,
		PersistSession: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("unexpected error up: %v", err)
	}
	if err := json.Unmarshal([]byte(res.Content), &result); err != nil {
		t.Fatalf("bad json up: %v", err)
	}
	if result.Model != "tjcoder/ergo-genius:cloud" {
		t.Fatalf("expected genius, got %s", result.Model)
	}
	if state.CurrentModel != "tjcoder/ergo-genius:cloud" {
		t.Fatalf("session not updated")
	}

	// Test direction down
	res, err = tool.Execute(context.Background(), []byte(`{"direction":"down"}`), client.ExecEnv{
		Provider: mock,
		SessionState: state,
		PersistSession: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("unexpected error down: %v", err)
	}
	if err := json.Unmarshal([]byte(res.Content), &result); err != nil {
		t.Fatalf("bad json down: %v", err)
	}
	if result.Model != "tjcoder/ergo-pro:cloud" {
		t.Fatalf("expected pro, got %s", result.Model)
	}
}