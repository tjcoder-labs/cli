package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

func TestBackgroundJobReturnsStatusAndOutput(t *testing.T) {
	root := t.TempDir()
	outputPath := filepath.Join(root, "output.log")
	if err := os.WriteFile(outputPath, []byte("Device AA:BB:CC:DD:EE:FF RSSI: -54\n"), 0o600); err != nil {
		t.Fatalf("write output: %v", err)
	}
	state := &session.State{
		BackgroundJobs: []session.BackgroundJob{{
			ID:         "job-test",
			Status:     "completed",
			StartedAt:  "2026-07-24T00:00:00Z",
			OutputPath: outputPath,
		}},
	}

	result, err := (backgroundJobTool{}).Execute(
		context.Background(),
		json.RawMessage(`{"job_id":"job-test"}`),
		ExecEnv{SessionState: state},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"status: completed", "Device AA:BB:CC:DD:EE:FF RSSI: -54"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("result missing %q: %s", want, result.Content)
		}
	}
}

func TestBackgroundJobRejectsUnknownJob(t *testing.T) {
	_, err := (backgroundJobTool{}).Execute(
		context.Background(),
		json.RawMessage(`{"job_id":"missing"}`),
		ExecEnv{SessionState: &session.State{}},
	)
	if err == nil || !strings.Contains(err.Error(), `background job "missing" not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWatchBackgroundJobProgress(t *testing.T) {
	state := &session.State{
		BackgroundJobs: []session.BackgroundJob{{
			ID:        "job-test",
			Status:    "running",
			StartedAt: "2026-11-13T00:00:00Z",
		}},
	}
	notified := make(chan client.Message, 1)
	env := ExecEnv{
		SessionState:    state,
		BackgroundJobMu: &sync.Mutex{},
		PersistSession:  func() error { return nil },
		NotifyBackgroundJob: func(msg client.Message) {
			select {
			case notified <- msg:
			default:
			}
		},
	}

	// Run one tick inline by calling with a tiny interval; the watcher
	// stops when the job is marked terminal.
	done := make(chan struct{})
	go func() {
		watchBackgroundJobProgress(env, "job-test", 1*time.Millisecond)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-notified:
			if msg.Role != "system" || msg.ToolName != "background_job" || !strings.Contains(msg.Content, "job-test still running") {
				t.Fatalf("unexpected progress notice: %+v", msg)
			}
			// Mark terminal (under lock) and confirm the watcher exits.
			env.BackgroundJobMu.Lock()
			state.BackgroundJobs[0].Status = "completed"
			env.BackgroundJobMu.Unlock()
			select {
			case <-done:
				return
			case <-deadline:
				t.Fatal("watcher did not stop")
			}
		case <-deadline:
			t.Fatal("watcher did not persist or stop in time")
		}
	}
}
