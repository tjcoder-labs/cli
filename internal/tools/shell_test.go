package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
