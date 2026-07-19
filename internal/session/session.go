// Package session owns the on-disk schema for a coder-cli workspace
// session. It is the single source of truth for the JSON written to
// <workspaceRoot>/.ergo-cli-go/session.json, shared by the interactive
// tview TUI and the headless `coder ask` subcommand.
//
// Both call sites must go through Load/Save (and the small in-memory
// helpers) rather than marshalling the struct themselves, so the schema
// can evolve in one place.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

// DirName is the directory created under each workspace root for
// session artifacts. Kept exported so other packages (tests, future
// tooling) can use the same path.
const DirName = ".ergo-cli-go"

// FileName is the canonical session file name.
const FileName = "session.json"

// Path returns the absolute session.json path for a workspace root.
func Path(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, DirName, FileName)
}

// TranscriptEntry is one item in the rendered transcript. The TUI
// renders these back into its on-screen view; the headless ask path
// appends new entries when persisting a turn.
type TranscriptEntry struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
	Author    string `json:"author,omitempty"`
}

// Task is a single item in the session's task list. See SENTINEL_BETA.md
// §3.1 for the full design notes. Fields are deliberately flat so the
// JSON stays greppable.
//
// PrioritySeq is the user/agent-controlled display order. Higher
// values sort first. Zero means "use the default sort" (status rank,
// then recency), so callers that don't care about ordering can simply
// leave it unset.
type Task struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Status      string         `json:"status"`
	Owner       string         `json:"owner"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
	Due         string         `json:"due,omitempty"`
	Priority    string         `json:"priority,omitempty"`
	PrioritySeq int            `json:"priority_seq,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// BackgroundJob tracks a long-running delegated command launched from the
// current session. The main thread keeps a lightweight record here while the
// child process writes to a job-specific log file under .ergo-cli-go/jobs/.
type BackgroundJob struct {
	ID          string `json:"id"`
	Command     string `json:"command"`
	Cwd         string `json:"cwd,omitempty"`
	Status      string `json:"status"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	OutputPath  string `json:"output_path,omitempty"`
	SessionPath string `json:"session_path,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Article is a single item in the session's article list. See
// SENTINEL_BETA.md §4.
type Article struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	Source    string   `json:"source"` // "local" or "ai.tjcoder.com"
}

// Memory is a single item in the session's memory list. A memory is a
// free-form note the user (or agent) wants to keep around for the
// rest of the session — for example, "the API key is in ~/.config/x"
// or "the user's preferred name is TJ". Memories are short, durable
// within a session, and never sent to a remote service.
type Memory struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	Source    string   `json:"source"` // "local" or "ai.tjcoder.com"
}

// State is the full session document. The field set is a superset of
// what either consumer writes today, which lets the TUI and headless
// paths evolve independently without losing each other's data.
type State struct {
	CurrentAgent string            `json:"current_agent"`
	CurrentModel string            `json:"current_model"`
	ToolMax      int               `json:"tool_max,omitempty"`
	EnabledTools []string          `json:"enabled_tools"`
	History      []client.Message  `json:"history"`
	ContextInfo  string            `json:"context_info,omitempty"`
	RefOrder     []string          `json:"ref_order,omitempty"`
	Transcript   []TranscriptEntry `json:"transcript"`
	Reasoning    string            `json:"reasoning,omitempty"`
	Activity     string            `json:"activity,omitempty"`

	// Tasks, Articles, and Memories are added by the SENTINEL_BETA work
	// and the unified-right-pane work. They are omitempty so old session
	// files (which don't have them) decode cleanly into the same struct.
	Tasks          []Task          `json:"tasks,omitempty"`
	Articles       []Article       `json:"articles,omitempty"`
	Memories       []Memory        `json:"memories,omitempty"`
	BackgroundJobs []BackgroundJob `json:"background_jobs,omitempty"`
}

// Load reads the session file for the given workspace root. If the file
// is missing it returns (zero, false, nil) — callers should treat that
// as "no prior session" and fall back to defaults. A corrupt file
// returns the decode error so callers can decide whether to back it up
// or wipe it.
func Load(workspaceRoot string) (State, bool, error) {
	var state State
	data, err := os.ReadFile(Path(workspaceRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return state, false, nil
		}
		return state, false, err
	}

	var raw struct {
		CurrentAgent   string           `json:"current_agent"`
		CurrentModel   string           `json:"current_model"`
		ToolMax        int              `json:"tool_max,omitempty"`
		EnabledTools   []string         `json:"enabled_tools"`
		History        []client.Message `json:"history"`
		ContextInfo    string           `json:"context_info,omitempty"`
		RefOrder       []string         `json:"ref_order,omitempty"`
		Transcript     json.RawMessage  `json:"transcript"`
		Reasoning      string           `json:"reasoning,omitempty"`
		Activity       string           `json:"activity,omitempty"`
		Tasks          []Task           `json:"tasks,omitempty"`
		Articles       []Article        `json:"articles,omitempty"`
		Memories       []Memory         `json:"memories,omitempty"`
		BackgroundJobs []BackgroundJob  `json:"background_jobs,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return state, true, fmt.Errorf("session: decode %s: %w", Path(workspaceRoot), err)
	}

	state.CurrentAgent = raw.CurrentAgent
	state.CurrentModel = raw.CurrentModel
	state.ToolMax = raw.ToolMax
	state.EnabledTools = raw.EnabledTools
	state.History = raw.History
	state.ContextInfo = raw.ContextInfo
	state.RefOrder = raw.RefOrder
	state.Reasoning = raw.Reasoning
	state.Activity = raw.Activity
	state.Tasks = raw.Tasks
	state.Articles = raw.Articles
	state.Memories = raw.Memories
	state.BackgroundJobs = raw.BackgroundJobs
	state.Transcript = parseTranscript(raw.Transcript)
	return state, true, nil
}

func parseTranscript(raw json.RawMessage) []TranscriptEntry {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var entries []TranscriptEntry
	if err := json.Unmarshal(raw, &entries); err == nil {
		return entries
	}
	var legacy string
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil
	}
	return parseLegacyTranscript(legacy)
}

func parseLegacyTranscript(legacy string) []TranscriptEntry {
	stripRe := regexp.MustCompile(`\[[^\]]+\]`)
	var entries []TranscriptEntry
	for _, block := range strings.Split(legacy, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		role := "assistant"
		if strings.Contains(strings.ToLower(block), "you") || strings.Contains(strings.ToLower(block), "[#c4a5ff") {
			role = "user"
		}
		lines := strings.Split(block, "\n")
		var content []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.Contains(strings.ToLower(trimmed), "you") || strings.Contains(strings.ToLower(trimmed), "assistant") {
				continue
			}
			cleaned := stripRe.ReplaceAllString(trimmed, "")
			cleaned = strings.TrimSpace(cleaned)
			if cleaned != "" {
				content = append(content, cleaned)
			}
		}
		if len(content) > 0 {
			entries = append(entries, TranscriptEntry{Role: role, Content: strings.Join(content, "\n")})
		}
	}
	return entries
}

// Save writes the state to the workspace's session file. It creates
// the .ergo-cli-go directory if needed. The file is written atomically
// (temp + rename) so a crash mid-write can't leave a half-written
// session behind.
func Save(workspaceRoot string, state State) error {
	if workspaceRoot == "" {
		return fmt.Errorf("session: empty workspace root")
	}
	dir := filepath.Join(workspaceRoot, DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".session-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, Path(workspaceRoot)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// EnsureDir creates the .ergo-cli-go directory if it doesn't already
// exist. Most callers don't need this — Save handles it — but the TUI
// calls it once at startup so subsequent writes don't pay the
// MkdirAll cost.
func EnsureDir(workspaceRoot string) error {
	return os.MkdirAll(filepath.Join(workspaceRoot, DirName), 0o700)
}

// SortedTaskIDs returns the IDs of t in the order they should be
// displayed (status priority, then most-recently-updated first). It
// does not mutate t.
func SortedTaskIDs(tasks []Task) []string {
	statusRank := map[string]int{
		"doing":     0,
		"todo":      1,
		"blocked":   2,
		"done":      3,
		"cancelled": 4,
	}
	out := make([]string, 0, len(tasks))
	// Stable sort: keep insertion order as the tiebreaker.
	indices := make([]int, len(tasks))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(a, b int) bool {
		ra, ok := statusRank[strings.ToLower(tasks[indices[a]].Status)]
		if !ok {
			ra = 99
		}
		rb, ok := statusRank[strings.ToLower(tasks[indices[b]].Status)]
		if !ok {
			rb = 99
		}
		if ra != rb {
			return ra < rb
		}
		return tasks[indices[a]].UpdatedAt > tasks[indices[b]].UpdatedAt
	})
	for _, i := range indices {
		out = append(out, tasks[i].ID)
	}
	return out
}
