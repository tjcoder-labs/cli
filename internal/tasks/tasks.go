// Package tasks owns the task list for a coder-cli session: the
// in-memory model, the storage adapter on top of internal/session, and
// the formatting helpers used by both the TUI and the model's prompt
// injection.
//
// Tasks are stored inside session.json alongside chat history. They
// survive across turns in the same session and are not synced to any
// remote service in this iteration. See SENTINEL_BETA.md §3.
package tasks

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/session"
)

// Status is the lifecycle state of a task. Custom values are rejected
// at the API boundary so the UI can rely on a fixed set of glyphs.
type Status string

const (
	StatusTodo      Status = "todo"
	StatusDoing     Status = "doing"
	StatusBlocked   Status = "blocked"
	StatusDone      Status = "done"
	StatusCancelled Status = "cancelled"
)

// AllStatuses returns the canonical status list in display order.
func AllStatuses() []Status {
	return []Status{StatusTodo, StatusDoing, StatusBlocked, StatusDone, StatusCancelled}
}

// ValidStatus reports whether s is one of the known statuses.
func ValidStatus(s string) bool {
	switch Status(strings.ToLower(strings.TrimSpace(s))) {
	case StatusTodo, StatusDoing, StatusBlocked, StatusDone, StatusCancelled:
		return true
	}
	return false
}

// NormalizeStatus lower-cases and trims an input string, returning the
// canonical Status value. Callers should check ValidStatus first when
// they need to reject invalid input.
func NormalizeStatus(s string) Status {
	return Status(strings.ToLower(strings.TrimSpace(s)))
}

// Task is the public alias for the persisted task type so callers
// don't need to import internal/session just to refer to a task.
type Task = session.Task

// List is an ordered, in-memory view of the tasks stored in a session.
// It is cheap to construct (just sorts a slice) and safe to read
// without locking because mutations go through Store, which produces
// a fresh List each time.
type List struct {
	items []Task
	byID  map[string]int
}

// Load reads the task slice from a session.State and returns a sorted
// List. An empty/nil slice yields a non-nil empty list.
func Load(state session.State) *List {
	l := &List{
		items: append([]Task(nil), state.Tasks...),
		byID:  make(map[string]int, len(state.Tasks)),
	}
	for i, t := range l.items {
		l.byID[t.ID] = i
	}
	l.sort()
	return l
}

// Len returns the number of tasks.
func (l *List) Len() int { return len(l.items) }

// All returns a copy of the tasks in display order.
func (l *List) All() []Task {
	out := make([]Task, len(l.items))
	copy(out, l.items)
	return out
}

// Get returns the task with the given id and whether it was found.
func (l *List) Get(id string) (Task, bool) {
	if l == nil {
		return Task{}, false
	}
	idx, ok := l.byID[id]
	if !ok {
		return Task{}, false
	}
	return l.items[idx], true
}

// Open returns the non-terminal tasks (todo/doing/blocked) in display
// order. This is what gets injected into the model prompt.
func (l *List) Open() []Task {
	if l == nil {
		return nil
	}
	out := make([]Task, 0, len(l.items))
	for _, t := range l.items {
		switch Status(t.Status) {
		case StatusTodo, StatusDoing, StatusBlocked:
			out = append(out, t)
		}
	}
	return out
}

// IDs returns the task IDs in display order. Useful for the TUI
// panel's selection cursor.
func (l *List) IDs() []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l.items))
	for i, t := range l.items {
		out[i] = t.ID
	}
	return out
}

func (l *List) sort() {
	rank := map[Status]int{
		StatusDoing:     0,
		StatusTodo:      1,
		StatusBlocked:   2,
		StatusDone:      3,
		StatusCancelled: 4,
	}
	// Two-stage sort: first by PrioritySeq desc (manual ordering wins),
	// then by status rank (doing > todo > blocked > done > cancelled),
	// then by UpdatedAt desc. A non-zero PrioritySeq on *any* task
	// flips the whole list into priority mode; otherwise the original
	// default applies.
	hasSeq := false
	for i := range l.items {
		if l.items[i].PrioritySeq != 0 {
			hasSeq = true
			break
		}
	}
	if hasSeq {
		sort.SliceStable(l.items, func(i, j int) bool {
			if l.items[i].PrioritySeq != l.items[j].PrioritySeq {
				return l.items[i].PrioritySeq > l.items[j].PrioritySeq
			}
			return l.items[i].UpdatedAt > l.items[j].UpdatedAt
		})
	} else {
		sort.SliceStable(l.items, func(i, j int) bool {
			ri, oki := rank[Status(l.items[i].Status)]
			if !oki {
				ri = 99
			}
			rj, okj := rank[Status(l.items[j].Status)]
			if !okj {
				rj = 99
			}
			if ri != rj {
				return ri < rj
			}
			return l.items[i].UpdatedAt > l.items[j].UpdatedAt
		})
	}
	// Rebuild the index after sorting.
	l.byID = make(map[string]int, len(l.items))
	for i, t := range l.items {
		l.byID[t.ID] = i
	}
}

// Store is the mutation surface for tasks. All methods produce a fresh
// ordered List and are safe to chain. They do not touch the disk; the
// caller is responsible for persisting the returned State via
// internal/session.
type Store struct {
	clock func() time.Time
	ids   IDGenerator
}

// NewStore returns a Store with default clock and ID generators.
func NewStore() *Store {
	return &Store{
		clock: time.Now,
		ids:   DefaultIDGenerator,
	}
}

// List returns the current list, sorted.
func (s *Store) List(state session.State) *List {
	return Load(state)
}

// CreateInput captures the parameters for a new task. Empty Title is
// rejected; Owner defaults to "agent" when blank.
type CreateInput struct {
	Title string
	Owner string
	Meta  map[string]any
}

// CreateResult contains the new task and the updated state. Either
// field is meaningful on its own, but State is the canonical return:
// the caller can persist it directly.
type CreateResult struct {
	Task  Task
	State session.State
}

// Create appends a new todo task to the state's task slice and
// returns the updated state. The new task's ID is a ULID, CreatedAt
// and UpdatedAt are set to the store's clock, and Status is "todo".
func (s *Store) Create(state session.State, in CreateInput) (CreateResult, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return CreateResult{}, fmt.Errorf("title is required")
	}
	owner := strings.TrimSpace(in.Owner)
	if owner == "" {
		owner = "agent"
	}
	now := s.clock().UTC().Format(time.RFC3339)
	task := Task{
		ID:        s.ids.New(),
		Title:     title,
		Status:    string(StatusTodo),
		Owner:     owner,
		CreatedAt: now,
		UpdatedAt: now,
		Meta:      cloneMeta(in.Meta),
	}
	state.Tasks = append(append([]Task(nil), state.Tasks...), task)
	return CreateResult{Task: task, State: state}, nil
}

// UpdateInput specifies which fields to change on an existing task.
// Nil/empty fields are left untouched.
type UpdateInput struct {
	ID     string
	Status string
	Title  string
	Due    string
	// ClearDue, when true, removes the task's due date even if Due is
	// empty in the input.
	ClearDue bool
	// Priority is one of "low", "normal", "high", "critical", or "" to
	// leave unchanged. "clear" (or ClearPriority) removes the field.
	Priority      string
	ClearPriority bool
	Meta          map[string]any
	// MergeMeta, when true, merges Meta into the existing meta map. When
	// false, Meta replaces it.
	MergeMeta bool
}

// UpdateResult contains the updated task and the new state. Task is
// zero-valued (and ok=false) when the ID was not found.
type UpdateResult struct {
	Task  Task
	State session.State
	OK    bool
}

// Update mutates the task with the given ID. Unknown IDs return
// (zero, state, false) without modifying the state.
func (s *Store) Update(state session.State, in UpdateInput) (UpdateResult, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return UpdateResult{}, fmt.Errorf("id is required")
	}
	now := s.clock().UTC().Format(time.RFC3339)
	tasks := append([]Task(nil), state.Tasks...)
	for i := range tasks {
		if tasks[i].ID != id {
			continue
		}
		if in.Status != "" {
			if !ValidStatus(in.Status) {
				return UpdateResult{}, fmt.Errorf("invalid status %q", in.Status)
			}
			tasks[i].Status = string(NormalizeStatus(in.Status))
		}
		if in.Title != "" {
			tasks[i].Title = strings.TrimSpace(in.Title)
		}
		if in.ClearDue {
			tasks[i].Due = ""
		} else if in.Due != "" {
			if _, err := time.Parse(time.RFC3339, in.Due); err != nil {
				return UpdateResult{}, fmt.Errorf("invalid due %q (expected RFC3339)", in.Due)
			}
			tasks[i].Due = in.Due
		}
		pri := strings.ToLower(strings.TrimSpace(in.Priority))
		if in.ClearPriority {
			tasks[i].Priority = ""
		} else if pri != "" {
			switch pri {
			case "low", "normal", "high", "critical":
				tasks[i].Priority = pri
			default:
				return UpdateResult{}, fmt.Errorf("invalid priority %q", in.Priority)
			}
		}
		if in.Meta != nil {
			if in.MergeMeta {
				if tasks[i].Meta == nil {
					tasks[i].Meta = map[string]any{}
				}
				for k, v := range in.Meta {
					tasks[i].Meta[k] = v
				}
			} else {
				tasks[i].Meta = cloneMeta(in.Meta)
			}
		}
		tasks[i].UpdatedAt = now
		state.Tasks = tasks
		return UpdateResult{Task: tasks[i], State: state, OK: true}, nil
	}
	return UpdateResult{State: state, OK: false}, nil
}

// ResequenceInput is the parameter for Resequence. IDs lists the new
// order: index 0 is highest priority, index len-1 is lowest. Tasks
// that are not in IDs keep their existing PrioritySeq (or get one
// assigned below the listed items).
type ResequenceInput struct {
	IDs []string
}

// Resequence reorders the tasks in state according to the provided ID
// list. The first ID gets the highest PrioritySeq, the next gets
// len-1, and so on. Tasks not mentioned are demoted to a negative
// PrioritySeq so they sort after the listed ones. Returns the new
// state and a list of (id, oldSeq, newSeq) entries describing the
// changes — useful for surfacing in the activity feed.
func (s *Store) Resequence(state session.State, in ResequenceInput) (session.State, []ResequenceChange) {
	if len(in.IDs) == 0 {
		return state, nil
	}
	tasks := append([]Task(nil), state.Tasks...)
	byID := make(map[string]int, len(tasks))
	for i, t := range tasks {
		byID[t.ID] = i
	}
	now := s.clock().UTC().Format(time.RFC3339)
	changes := make([]ResequenceChange, 0, len(in.IDs))
	max := len(in.IDs)
	seen := make(map[string]bool, len(in.IDs))
	for rank, id := range in.IDs {
		idx, ok := byID[strings.TrimSpace(id)]
		if !ok {
			continue
		}
		seen[id] = true
		old := tasks[idx].PrioritySeq
		tasks[idx].PrioritySeq = max - rank
		tasks[idx].UpdatedAt = now
		changes = append(changes, ResequenceChange{ID: id, Old: old, New: tasks[idx].PrioritySeq})
	}
	// Demote unmentioned tasks so they sort to the bottom.
	for i := range tasks {
		if seen[tasks[i].ID] {
			continue
		}
		if tasks[i].PrioritySeq > 0 {
			old := tasks[i].PrioritySeq
			tasks[i].PrioritySeq = -1
			tasks[i].UpdatedAt = now
			changes = append(changes, ResequenceChange{ID: tasks[i].ID, Old: old, New: -1})
		}
	}
	state.Tasks = tasks
	return state, changes
}

// ResequenceChange describes a single ID's old/new PrioritySeq for
// surfacing in the activity feed.
type ResequenceChange struct {
	ID  string
	Old int
	New int
}

// Delete removes the task with the given ID. Returns the new state and
// whether a task was actually removed.
func (s *Store) Delete(state session.State, id string) (session.State, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return state, false
	}
	tasks := make([]Task, 0, len(state.Tasks))
	removed := false
	for _, t := range state.Tasks {
		if t.ID == id && !removed {
			removed = true
			continue
		}
		tasks = append(tasks, t)
	}
	if removed {
		state.Tasks = tasks
	}
	return state, removed
}

// ToggleDone flips a task between "done" and its previous open
// status. The previous open status is captured at toggle time so we
// can restore it (rather than always defaulting to "todo") — that
// way a "doing" task that gets checked off and then un-checked
// returns to "doing" instead of dropping back to the top of the
// todo pile.
//
// The "previous open status" is recorded in task meta under
// `prev_status` so the state survives across sessions. Returns the
// new state, the updated task, and whether the toggle actually
// changed something.
func (s *Store) ToggleDone(state session.State, id string) (session.State, Task, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return state, Task{}, false, fmt.Errorf("id is required")
	}
	tasks := append([]Task(nil), state.Tasks...)
	for i := range tasks {
		if tasks[i].ID != id {
			continue
		}
		current := Status(tasks[i].Status)
		now := s.clock().UTC().Format(time.RFC3339)
		tasks[i].UpdatedAt = now
		if current == StatusDone {
			// Un-check: restore the previously recorded open status
			// (defaulting to "todo" if none is recorded).
			prev := StatusTodo
			if v, ok := tasks[i].Meta["prev_status"].(string); ok && ValidStatus(v) && Status(v) != StatusDone {
				prev = Status(v)
			}
			if tasks[i].Meta == nil {
				tasks[i].Meta = map[string]any{}
			}
			delete(tasks[i].Meta, "prev_status")
			tasks[i].Status = string(prev)
		} else {
			// Mark done: stash the current open status so we can
			// restore it if the user un-checks.
			if tasks[i].Meta == nil {
				tasks[i].Meta = map[string]any{}
			}
			tasks[i].Meta["prev_status"] = string(current)
			tasks[i].Status = string(StatusDone)
		}
		state.Tasks = tasks
		return state, tasks[i], true, nil
	}
	return state, Task{}, false, nil
}

// Clear removes tasks matching the given statuses. If no statuses are
// provided it defaults to "done" and "cancelled" — the same convention
// used by the manage_tasks tool.
func (s *Store) Clear(state session.State, statuses ...string) session.State {
	if len(statuses) == 0 {
		statuses = []string{string(StatusDone), string(StatusCancelled)}
	}
	keep := make(map[Status]struct{}, len(statuses))
	for _, s := range statuses {
		keep[NormalizeStatus(s)] = struct{}{}
	}
	kept := make([]Task, 0, len(state.Tasks))
	for _, t := range state.Tasks {
		if _, drop := keep[Status(t.Status)]; drop {
			continue
		}
		kept = append(kept, t)
	}
	state.Tasks = kept
	return state
}

func cloneMeta(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
