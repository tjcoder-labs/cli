package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
)

func TestLoadMissingReturnsNotExists(t *testing.T) {
	tmp := t.TempDir()
	state, exists, err := Load(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Fatalf("expected exists=false for missing file")
	}
	if !reflect.DeepEqual(state, State{}) {
		t.Fatalf("expected zero state, got %+v", state)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	in := State{
		CurrentAgent: "software-engineer",
		CurrentModel: "minimax-m3:cloud",
		EnabledTools: []string{"run_command", "read_file"},
		History: []client.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
		ContextInfo: "ctx: 1/100",
		RefOrder:    []string{"AGENTS.md"},
		Transcript: []TranscriptEntry{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi", Timestamp: "1:02 PM"},
		},
		Reasoning: "thinking...",
		Activity:  "13:40:32 Loaded",
		Tasks: []Task{
			{ID: "a", Status: "todo", Title: "one", UpdatedAt: "2026-07-08T13:00:00Z"},
			{ID: "b", Status: "doing", Title: "two", UpdatedAt: "2026-07-08T13:05:00Z"},
		},
		BackgroundJobs: []BackgroundJob{{
			ID:        "job-1",
			Command:   "coder ask -p 'hello'",
			Status:    "running",
			StartedAt: "2026-07-14T03:00:00Z",
			OutputPath: "/tmp/job-1.log",
		}},
	}
	if err := Save(tmp, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, exists, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !exists {
		t.Fatalf("expected exists=true after Save")
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch:\nin:  %+v\nout: %+v", in, out)
	}
}

func TestSaveFileIs0600(t *testing.T) {
	tmp := t.TempDir()
	if err := Save(tmp, State{CurrentAgent: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(Path(tmp))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600, got %o", perm)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "nested", "workspace")
	if err := Save(tmp, State{CurrentAgent: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, DirName)); err != nil {
		t.Fatalf("expected %s to exist: %v", DirName, err)
	}
}

func TestSaveRefusesEmptyWorkspaceRoot(t *testing.T) {
	if err := Save("", State{}); err == nil {
		t.Fatalf("expected error for empty workspace root")
	}
}

func TestLoadCorruptFileReturnsError(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, DirName), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(Path(tmp), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, exists, err := Load(tmp)
	if err == nil {
		t.Fatalf("expected decode error")
	}
	if !exists {
		t.Fatalf("expected exists=true for corrupt-but-present file")
	}
}

func TestLoadOldShapeWithoutTasksOrArticles(t *testing.T) {
	// A session file written by a pre-SENTINEL_BETA binary won't have
	// tasks/articles. Load should still succeed and leave those slices nil.
	tmp := t.TempDir()
	old := map[string]any{
		"current_agent": "software-engineer",
		"current_model": "minimax-m3:cloud",
		"enabled_tools": []string{"run_command"},
		"history":       []any{},
	}
	data, _ := json.Marshal(old)
	if err := os.MkdirAll(filepath.Join(tmp, DirName), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(Path(tmp), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	state, exists, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !exists {
		t.Fatalf("expected exists=true")
	}
	if state.CurrentAgent != "software-engineer" {
		t.Fatalf("agent mismatch: %q", state.CurrentAgent)
	}
	if state.Tasks != nil {
		t.Fatalf("expected nil tasks, got %+v", state.Tasks)
	}
}

func TestLoadLegacyStringTranscript(t *testing.T) {
	tmp := t.TempDir()
	legacy := map[string]any{
		"current_agent": "software-engineer",
		"current_model": "minimax-m3:cloud",
		"enabled_tools": []string{"run_command"},
		"history":       []any{},
		"transcript":   "[#C4A5FF::b]You[-:-:-]\nHello there\n\n[#A77CF8::b]Assistant[-:-:-]\nGeneral Kenobi\n\n",
	}
	data, _ := json.Marshal(legacy)
	if err := os.MkdirAll(filepath.Join(tmp, DirName), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(Path(tmp), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	state, exists, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !exists {
		t.Fatalf("expected exists=true")
	}
	if len(state.Transcript) != 2 {
		t.Fatalf("expected 2 transcript entries, got %d", len(state.Transcript))
	}
	if state.Transcript[0].Role != "user" || state.Transcript[0].Content != "Hello there" {
		t.Fatalf("unexpected first transcript entry: %+v", state.Transcript[0])
	}
	if state.Transcript[1].Role != "assistant" || state.Transcript[1].Content != "General Kenobi" {
		t.Fatalf("unexpected second transcript entry: %+v", state.Transcript[1])
	}
}

func TestSortedTaskIDsOrdering(t *testing.T) {
	tasks := []Task{
		{ID: "old-done", Status: "done", UpdatedAt: "2026-07-01T00:00:00Z"},
		{ID: "new-done", Status: "done", UpdatedAt: "2026-07-08T00:00:00Z"},
		{ID: "todo-a", Status: "todo", UpdatedAt: "2026-07-08T10:00:00Z"},
		{ID: "doing-x", Status: "doing", UpdatedAt: "2026-07-08T09:00:00Z"},
		{ID: "blocked", Status: "blocked", UpdatedAt: "2026-07-08T08:00:00Z"},
		{ID: "cancelled", Status: "cancelled", UpdatedAt: "2026-07-08T07:00:00Z"},
		{ID: "weird", Status: "unknown", UpdatedAt: "2026-07-08T11:00:00Z"},
	}
	got := SortedTaskIDs(tasks)
	wantPrefix := []string{"doing-x", "todo-a", "blocked", "new-done", "old-done", "cancelled"}
	if !strings.HasPrefix(strings.Join(got, ","), strings.Join(wantPrefix, ",")) {
		t.Fatalf("order prefix mismatch:\ngot:  %v\nwant: %v", got, wantPrefix)
	}
	// The unknown-status task should sort after every known status (rank 99).
	if got[len(got)-1] != "weird" {
		t.Fatalf("expected unknown status task last, got order: %v", got)
	}
}

func TestSortedTaskIDsEmpty(t *testing.T) {
	got := SortedTaskIDs(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestPathMatchesExpectedLayout(t *testing.T) {
	got := Path("/tmp/workspace")
	want := filepath.Join("/tmp/workspace", ".ergo-cli-go", "session.json")
	if got != want {
		t.Fatalf("Path(%q) = %q, want %q", "/tmp/workspace", got, want)
	}
	// sort import linter keeps the import list tidy in case of future edits
	_ = sort.Strings
}
