package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

type delegationTestRunner struct {
	state         *session.State
	err           error
	sawHistory    int
	sawTranscript int
}

func (r *delegationTestRunner) RunDelegation(_ context.Context, state *session.State, _ string, specialist agent.Config) ([]client.Message, error) {
	r.state = state
	// Capture the isolation at call time: the specialist must receive no
	// coordinator history or transcript, and no delegate/collect tools.
	r.sawHistory = len(state.History)
	r.sawTranscript = len(state.Transcript)
	state.Tasks = append(state.Tasks, session.Task{ID: "specialist-task", Title: "Report verified"})
	state.Memories = append(state.Memories, session.Memory{ID: "specialist-memory", Title: "Durable finding"})
	return []client.Message{{Role: "assistant", Content: "Outcome: completed\nArtifacts: report.go\nUnresolved: none"}}, r.err
}

func TestDelegationToolReturnsImmediatelyAndCollects(t *testing.T) {
	root := t.TempDir()
	state := session.State{History: []client.Message{{Role: "user", Content: "private coordinator context"}}}
	runner := &delegationTestRunner{}
	hub := &delegationHub{records: map[string]*delegationRecord{}}
	env := ExecEnv{WorkspaceRoot: root, SessionState: &state, PersistSession: func() error { return nil }}
	dt := delegationTool{factory: func(context.Context, ExecEnv, agent.Config) DelegationRunner { return runner }, hub: hub}

	result, err := dt.Execute(context.Background(), json.RawMessage(`{"agent":"code-reviewer","goal":"Review report","context":"Read only","deliverable":"Findings and verification"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "running") {
		t.Errorf("delegate should return immediately in running state, got: %s", result.Content)
	}
	// Extract the ID from the result.
	var id string
	for _, line := range strings.Split(result.Content, "\n") {
		if strings.HasPrefix(line, "ID: ") {
			id = strings.TrimPrefix(line, "ID: ")
		}
	}
	if id == "" {
		t.Fatalf("no delegation ID in result: %s", result.Content)
	}

	// The coordinator history must never be touched by the specialist.
	if len(state.History) == 0 || state.History[0].Content != "private coordinator context" {
		t.Error("coordinator history was mutated")
	}
	// Specialist got isolated state. Wait for the background goroutine to
	// finish (poll the hub record status) before asserting on runner.state.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rec := hub.get(id); rec != nil && rec.Status != "running" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runner.state == nil || runner.sawHistory != 0 || runner.sawTranscript != 0 {
		t.Error("specialist inherited coordinator transcript or history")
	}
	for _, tool := range runner.state.EnabledTools {
		if tool == "delegate" || tool == "collect" {
			t.Fatalf("specialist can recursively delegate/collect: %s", tool)
		}
	}

	// collect should merge shared tasks and memories once.
	ct := collectTool{hub: hub}
	res, err := ct.Execute(context.Background(), json.RawMessage(`{"id":"`+id+`"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Outcome: completed") || !strings.Contains(res.Content, ".ergo-cli-go/delegations") {
		t.Errorf("unexpected collect result: %s", res.Content)
	}
	if len(state.Tasks) != 1 || state.Tasks[0].ID != "specialist-task" {
		t.Error("shared task handoff was not merged")
	}
	if len(state.Memories) != 1 || state.Memories[0].ID != "specialist-memory" {
		t.Error("shared memory handoff was not merged")
	}
}

func TestCollectBeforeFinishReturnsRunning(t *testing.T) {
	hub := &delegationHub{records: map[string]*delegationRecord{"x": {Agent: "code-reviewer", Status: "running"}}}
	ct := collectTool{hub: hub}
	res, err := ct.Execute(context.Background(), json.RawMessage(`{"id":"x"}`), ExecEnv{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Preview != "running" {
		t.Errorf("expected running, got %q", res.Preview)
	}
}

func TestDelegationToolRejectsRecursionAndUnknownTask(t *testing.T) {
	hub := &delegationHub{records: map[string]*delegationRecord{}}
	dt := delegationTool{
		factory: func(context.Context, ExecEnv, agent.Config) DelegationRunner { return &delegationTestRunner{} },
		hub:     hub,
	}
	for _, raw := range []string{
		`{"agent":"ergo","goal":"recurse","deliverable":"none"}`,
		`{"agent":"code-reviewer","goal":"review","deliverable":"report","task_id":"missing"}`,
	} {
		if _, err := dt.Execute(context.Background(), json.RawMessage(raw), ExecEnv{SessionState: &session.State{}}); err == nil {
			t.Errorf("expected rejection for %s", raw)
		}
	}
}

func TestDelegationToolReportsSpecialistFailure(t *testing.T) {
	hub := &delegationHub{records: map[string]*delegationRecord{}}
	dt := delegationTool{
		factory: func(context.Context, ExecEnv, agent.Config) DelegationRunner {
			return &delegationTestRunner{err: fmt.Errorf("provider failed")}
		},
		hub: hub,
	}
	state := session.State{}
	env := ExecEnv{WorkspaceRoot: t.TempDir(), SessionState: &state, PersistSession: func() error { return nil }}
	res, err := dt.Execute(context.Background(), json.RawMessage(`{"agent":"code-reviewer","goal":"Review","deliverable":"Report"}`), env)
	if err != nil {
		t.Fatalf("delegate must not block/error synchronously; got %v", err)
	}
	var id string
	for _, line := range strings.Split(res.Content, "\n") {
		if strings.HasPrefix(line, "ID: ") {
			id = strings.TrimPrefix(line, "ID: ")
		}
	}
	// Wait for the goroutine to finish writing the transcript file so
	// TempDir cleanup doesn't race it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rec := hub.get(id); rec != nil && rec.Status != "running" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rec := hub.get(id); rec == nil || rec.Status != "failed" {
		t.Errorf("expected failed status, got %+v", rec)
	}
}
