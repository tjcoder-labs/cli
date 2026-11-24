package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

type delegationTestRunner struct {
	state *session.State
	err   error
}

func (r *delegationTestRunner) RunDelegation(_ context.Context, state *session.State, _ string, specialist agent.Config) ([]client.Message, error) {
	r.state = state
	state.Tasks = append(state.Tasks, session.Task{ID: "specialist-task", Title: "Report verified"})
	return []client.Message{{Role: "assistant", Content: "Outcome: completed\nArtifacts: report.go\nUnresolved: none"}}, r.err
}

func TestDelegationToolStructuredHandoffAndSharedState(t *testing.T) {
	previous := delegationFactory
	defer RegisterDelegation(previous)
	runner := &delegationTestRunner{}
	RegisterDelegation(func(context.Context, ExecEnv, agent.Config) DelegationRunner { return runner })
	root := t.TempDir()
	state := session.State{History: []client.Message{{Role: "user", Content: "private coordinator context"}}}
	persisted := false
	result, err := (delegationTool{}).Execute(context.Background(), json.RawMessage(`{"agent":"code-reviewer","goal":"Review report","context":"Read only","deliverable":"Findings and verification"}`), ExecEnv{
		WorkspaceRoot: root, SessionState: &state, PersistSession: func() error { persisted = true; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.state == nil || len(runner.state.History) != 0 || len(runner.state.Transcript) != 0 {
		t.Error("specialist inherited coordinator transcript or history")
	}
	for _, tool := range runner.state.EnabledTools {
		if tool == "delegate" {
			t.Fatal("specialist can recursively delegate")
		}
	}
	if len(state.Tasks) != 1 || state.Tasks[0].ID != "specialist-task" || !persisted {
		t.Error("shared task handoff was not persisted")
	}
	if !strings.Contains(result.Content, "Outcome: completed") || !strings.Contains(result.Content, ".ergo-cli-go/delegations") {
		t.Errorf("unexpected result: %s", result.Content)
	}
}

func TestDelegationToolRejectsRecursionAndUnknownTask(t *testing.T) {
	RegisterDelegation(func(context.Context, ExecEnv, agent.Config) DelegationRunner { return &delegationTestRunner{} })
	for _, raw := range []string{
		`{"agent":"ergo","goal":"recurse","deliverable":"none"}`,
		`{"agent":"code-reviewer","goal":"review","deliverable":"report","task_id":"missing"}`,
	} {
		if _, err := (delegationTool{}).Execute(context.Background(), json.RawMessage(raw), ExecEnv{SessionState: &session.State{}}); err == nil {
			t.Errorf("expected rejection for %s", raw)
		}
	}
}

func TestDelegationToolReportsSpecialistFailure(t *testing.T) {
	previous := delegationFactory
	defer RegisterDelegation(previous)
	RegisterDelegation(func(context.Context, ExecEnv, agent.Config) DelegationRunner {
		return &delegationTestRunner{err: errors.New("provider failed")}
	})
	_, err := (delegationTool{}).Execute(context.Background(), json.RawMessage(`{"agent":"code-reviewer","goal":"Review","deliverable":"Report"}`), ExecEnv{WorkspaceRoot: t.TempDir(), SessionState: &session.State{}})
	if err == nil || !strings.Contains(err.Error(), "provider failed") {
		t.Fatalf("expected specialist error, got %v", err)
	}
}
