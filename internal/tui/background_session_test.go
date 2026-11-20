package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

func TestBackgroundJobNoticePersistsAndQueuesOnce(t *testing.T) {
	a, _ := startSimApp(t)
	a.recordBackgroundJobNotice(client.Message{
		Role: "system", ToolName: "background_job", Content: "Background job job-test completed.",
	})
	onLoop(t, a, func() {})
	state, ok, err := session.Load(a.workspaceRoot)
	if err != nil || !ok {
		t.Fatalf("load session: exists=%v err=%v", ok, err)
	}
	if len(state.History) != 1 || state.History[0].Content != "Background job job-test completed." {
		t.Fatalf("persisted history = %+v", state.History)
	}
	// An idle notice remains queued for the next runner request; the
	// callback itself must never start a provider request.
	if got := len(a.runner.DrainBackgroundNotices(nil)); got != 1 {
		t.Fatalf("pending notices = %d, want 1", got)
	}
}

func TestLastInteractionAtSurvivesUnrelatedSave(t *testing.T) {
	a, _ := startSimApp(t)
	stamp := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	onLoop(t, a, func() {
		a.lastInteractionAt = stamp
		a.history = []client.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}
		if err := a.saveSession(); err != nil {
			t.Errorf("save: %v", err)
		}
		// A later unrelated save must preserve the completed-turn time.
		a.currentModel = "another-model"
		if err := a.saveSession(); err != nil {
			t.Errorf("second save: %v", err)
		}
	})
	state, ok, err := session.Load(a.workspaceRoot)
	if err != nil || !ok {
		t.Fatalf("load session: exists=%v err=%v", ok, err)
	}
	if state.LastInteractionAt != stamp {
		t.Fatalf("last interaction = %q, want %q", state.LastInteractionAt, stamp)
	}
}

func TestBackgroundCommandCompletionReachesSession(t *testing.T) {
	a, _ := startSimApp(t)
	result, err := a.registry.Execute(context.Background(), "run_command", json.RawMessage(`{
		"command":"printf finished", "background":true, "check_after_seconds":1
	}`), a.runner.ExecEnv())
	if err != nil {
		t.Fatalf("run background command: %v", err)
	}
	if !strings.Contains(result.Content, "Started background job") {
		t.Fatalf("start result = %q", result.Content)
	}
	waitForText(t, a, "completed", func() string { return a.activity.GetText(true) })
	state, ok, err := session.Load(a.workspaceRoot)
	if err != nil || !ok {
		t.Fatalf("load session: exists=%v err=%v", ok, err)
	}
	if len(state.BackgroundJobs) != 1 || state.BackgroundJobs[0].Status != "completed" {
		t.Fatalf("jobs = %+v", state.BackgroundJobs)
	}
	found := false
	for _, msg := range state.History {
		if msg.ToolName == "background_job" && strings.Contains(msg.Content, "completed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("completion missing from saved history: %+v", state.History)
	}
}
