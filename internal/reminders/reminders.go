// Package reminders wraps the on-disk reminders.json file written by
// the set_reminder tool. Reminders live in workspace/.ergo-cli-go/
// reminders.json rather than session.json so they survive across
// sessions in the same workspace. The Tracker adapter lets the
// manage_items tool list/delete them uniformly with tasks and
// memories.
//
// A reminder can optionally carry a Prompt and Agent, which turns it
// into a *schedule*: on each cron tick the binary re-invokes itself
// headlessly (coder schedule run <id>) rather than simply echoing a
// message into a log file. Platform and DailyCap, when set, enforce a
// per-day cap on like actions before the schedule is allowed to run.
package reminders

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileName is the canonical reminders file name under
// .ergo-cli-go/. Kept exported for the test harness and any future
// tooling that needs to point at the same path.
const FileName = "reminders.json"

// Entry is the on-disk shape of a single reminder. It is the
// minimum needed to render the list and to surface in the agent's
// prompt; the full cron expression is kept verbatim so we can
// re-install the entry into the user's crontab on demand.
type Entry struct {
	ID        string `json:"id"`
	CronExpr  string `json:"cron_expr"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
	// Installed is true when the entry has been appended to the
	// user's crontab. We persist it so the UI can show an
	// "installed" badge and so the agent can avoid double-installing.
	Installed bool `json:"installed,omitempty"`
	// Prompt, Agent, Platform, and DailyCap turn a passive reminder
	// into an active schedule. When Prompt is non-empty, each cron
	// tick re-invokes the binary headlessly (coder schedule run <id>)
	// with Prompt instead of appending Message to a log file.
	//
	// Agent names the agent config used for the re-invocation.
	// Platform names the social platform whose "like" actions the cap
	// counts (e.g. "linkedin"). DailyCap, when > 0, caps the number
	// of that platform's like actions per calendar day; the schedule
	// runner checks the interaction ledger before each run and skips
	// (exit 0, logged) once the cap is reached.
	Prompt   string `json:"prompt,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Platform string `json:"platform,omitempty"`
	DailyCap int    `json:"daily_cap,omitempty"`
	// LastRun is the RFC3339 UTC timestamp of the last cron tick at
	// which the schedule fired. Written by whichever runner fired it
	// (the in-session TUI scheduler or the headless `coder schedule
	// run` entrypoint) so the other never re-fires the same tick.
	LastRun string `json:"last_run,omitempty"`
}

// Store is the mutation surface for reminders. The on-disk file is
// rewritten in full on every mutation — fine because the file is
// tiny (a handful of JSON objects per user).
type Store struct {
	clock func() time.Time
	ids   IDGenerator
	path  func(workspaceRoot string) string
}

// NewStore returns a Store that reads/writes <workspace>/.ergo-cli-go/
// reminders.json.
func NewStore() *Store {
	return &Store{
		clock: time.Now,
		ids:   DefaultIDGenerator,
		path: func(root string) string {
			return filepath.Join(root, ".ergo-cli-go", FileName)
		},
	}
}

// Load reads the reminders file. A missing file yields a non-nil
// empty List and a nil error — "no reminders yet" is not an error.
func (s *Store) Load(workspaceRoot string) (*List, error) {
	var entries []Entry
	data, err := os.ReadFile(s.path(workspaceRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return &List{items: nil, byID: map[string]int{}}, nil
		}
		return nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, err
		}
	}
	return newList(entries), nil
}

// Save writes the given List to disk, creating parent directories
// as needed.
func (s *Store) Save(workspaceRoot string, list *List) error {
	dir := filepath.Dir(s.path(workspaceRoot))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries := list.All()
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(workspaceRoot), data, 0o600)
}

// CreateInput captures the parameters for a new reminder. CronExpr is
// required. For a passive reminder, Message is required. For a
// schedule (self re-invocation), Prompt is required instead.
type CreateInput struct {
	CronExpr    string
	Message     string
	InstallCron bool
	Prompt      string
	Agent       string
	Platform    string
	DailyCap    int
}

// Create appends a new entry to the file and returns the updated
// list. It does not touch crontab itself — the set_reminder tool
// handles that side effect.
func (s *Store) Create(workspaceRoot string, in CreateInput) (*List, Entry, error) {
	cron := strings.TrimSpace(in.CronExpr)
	msg := strings.TrimSpace(in.Message)
	prompt := strings.TrimSpace(in.Prompt)
	if cron == "" {
		return nil, Entry{}, fmt.Errorf("cron_expr is required")
	}
	if msg == "" && prompt == "" {
		return nil, Entry{}, fmt.Errorf("message or prompt is required")
	}
	now := s.clock().UTC().Format(time.RFC3339)
	entry := Entry{
		ID:        s.ids.New(),
		CronExpr:  cron,
		Message:   msg,
		CreatedAt: now,
		Installed: in.InstallCron,
		Prompt:    prompt,
		Agent:     strings.TrimSpace(in.Agent),
		Platform:  strings.ToLower(strings.TrimSpace(in.Platform)),
		DailyCap:  in.DailyCap,
	}
	list, err := s.Load(workspaceRoot)
	if err != nil {
		return nil, Entry{}, err
	}
	list.items = append(list.items, entry)
	list.sort()
	if err := s.Save(workspaceRoot, list); err != nil {
		return nil, Entry{}, err
	}
	return list, entry, nil
}

// Delete removes the reminder with the given ID.
func (s *Store) Delete(workspaceRoot, id string) (*List, bool, error) {
	list, err := s.Load(workspaceRoot)
	if err != nil {
		return nil, false, err
	}
	kept := make([]Entry, 0, len(list.items))
	removed := false
	for _, e := range list.items {
		if e.ID == id && !removed {
			removed = true
			continue
		}
		kept = append(kept, e)
	}
	if !removed {
		return list, false, nil
	}
	list.items = kept
	if err := s.Save(workspaceRoot, list); err != nil {
		return nil, false, err
	}
	return list, true, nil
}

// FilePath returns the canonical reminders.json path for a
// workspace. Exported so the in-session scheduler can stat the file
// directly (cheap change detection between polls).
func FilePath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".ergo-cli-go", FileName)
}

// MarkRun stamps entry id's LastRun with the given time, persisting
// the update. Used by schedule runners to record that a tick has
// been consumed; on a missing entry it is a no-op.
func (s *Store) MarkRun(workspaceRoot, id string, at time.Time) error {
	list, err := s.Load(workspaceRoot)
	if err != nil {
		return err
	}
	for i := range list.items {
		if list.items[i].ID == id {
			list.items[i].LastRun = at.UTC().Format(time.RFC3339)
			return s.Save(workspaceRoot, list)
		}
	}
	return nil
}

// MarkRun is the package-level convenience used by the in-session
// scheduler (which does not otherwise retain a Store).
func MarkRun(workspaceRoot, id string, at time.Time) error {
	return NewStore().MarkRun(workspaceRoot, id, at)
}

// List is an in-memory view of the reminders file. Sorted newest-first.
type List struct {
	items []Entry
	byID  map[string]int
}

func newList(items []Entry) *List {
	l := &List{items: append([]Entry(nil), items...), byID: map[string]int{}}
	l.sort()
	return l
}

// Len returns the number of entries.
func (l *List) Len() int { return len(l.items) }

// All returns a copy of the entries in display order.
func (l *List) All() []Entry {
	out := make([]Entry, len(l.items))
	copy(out, l.items)
	return out
}

// Get returns the entry with the given id and whether it was found.
func (l *List) Get(id string) (Entry, bool) {
	if l == nil {
		return Entry{}, false
	}
	idx, ok := l.byID[id]
	if !ok {
		return Entry{}, false
	}
	return l.items[idx], true
}

// IDs returns the entry IDs in display order.
func (l *List) IDs() []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l.items))
	for i, e := range l.items {
		out[i] = e.ID
	}
	return out
}

func (l *List) sort() {
	sort.SliceStable(l.items, func(i, j int) bool {
		return l.items[i].CreatedAt > l.items[j].CreatedAt
	})
	l.byID = make(map[string]int, len(l.items))
	for i, e := range l.items {
		l.byID[e.ID] = i
	}
}

// IDGenerator produces unique IDs.
type IDGenerator interface {
	New() string
}

var DefaultIDGenerator IDGenerator = ulidGenerator{}
