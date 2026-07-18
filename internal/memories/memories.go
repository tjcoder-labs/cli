// Package memories owns the in-memory store of long-lived notes the
// user (or agent) wants to keep around for a session. Memories are
// stored in session.json alongside tasks/articles, and surfaced both
// in the TUI's right-column list and in the agent's prompt via the
// manage_items tool.
package memories

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/session"
)

// Memory is the public alias for the persisted type so callers don't
// have to import internal/session just to refer to a memory.
type Memory = session.Memory

// List is an ordered, in-memory view of the memories stored in a
// session. Mutations go through Store, which produces a fresh List
// each time so reads stay lock-free.
type List struct {
	items []Memory
	byID  map[string]int
}

// Load reads the memory slice from a session.State and returns a
// sorted List. An empty/nil slice yields a non-nil empty list.
func Load(state session.State) *List {
	l := &List{
		items: append([]Memory(nil), state.Memories...),
		byID:  make(map[string]int, len(state.Memories)),
	}
	for i, m := range l.items {
		l.byID[m.ID] = i
	}
	l.sort()
	return l
}

// Len returns the number of memories.
func (l *List) Len() int { return len(l.items) }

// All returns a copy of the memories in display order.
func (l *List) All() []Memory {
	out := make([]Memory, len(l.items))
	copy(out, l.items)
	return out
}

// Get returns the memory with the given id and whether it was found.
func (l *List) Get(id string) (Memory, bool) {
	if l == nil {
		return Memory{}, false
	}
	idx, ok := l.byID[id]
	if !ok {
		return Memory{}, false
	}
	return l.items[idx], true
}

// IDs returns the memory IDs in display order. Useful for the TUI
// panel's selection cursor.
func (l *List) IDs() []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l.items))
	for i, m := range l.items {
		out[i] = m.ID
	}
	return out
}

func (l *List) sort() {
	sort.SliceStable(l.items, func(i, j int) bool {
		return l.items[i].UpdatedAt > l.items[j].UpdatedAt
	})
	l.byID = make(map[string]int, len(l.items))
	for i, m := range l.items {
		l.byID[m.ID] = i
	}
}

// Store is the mutation surface for memories. It produces a fresh
// ordered List on demand and never touches the disk itself; the
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
func (s *Store) List(state session.State) *List { return Load(state) }

// CreateInput captures the parameters for a new memory. Empty Title is
// rejected; Source defaults to "local" when blank.
type CreateInput struct {
	Title  string
	Body   string
	Tags   []string
	Source string
}

// CreateResult contains the new memory and the updated state. State
// is the canonical return so the caller can persist it directly.
type CreateResult struct {
	Memory Memory
	State  session.State
}

// Create appends a new memory to the state's slice. The new ID is a
// ULID, CreatedAt and UpdatedAt are set to the store's clock, and
// Source defaults to "local" when blank.
func (s *Store) Create(state session.State, in CreateInput) (CreateResult, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return CreateResult{}, fmt.Errorf("title is required")
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "local"
	}
	now := s.clock().UTC().Format(time.RFC3339)
	m := Memory{
		ID:        s.ids.New(),
		Title:     title,
		Body:      in.Body,
		Tags:      append([]string(nil), in.Tags...),
		CreatedAt: now,
		UpdatedAt: now,
		Source:    source,
	}
	state.Memories = append(append([]Memory(nil), state.Memories...), m)
	return CreateResult{Memory: m, State: state}, nil
}

// UpdateInput specifies which fields to change on an existing memory.
type UpdateInput struct {
	ID     string
	Title  string
	Body   string
	Tags   []string
	Source string
}

// Update mutates the memory with the given ID. Unknown IDs return
// (zero, state, false) without modifying the state.
func (s *Store) Update(state session.State, in UpdateInput) (UpdateResult, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return UpdateResult{}, fmt.Errorf("id is required")
	}
	now := s.clock().UTC().Format(time.RFC3339)
	mems := append([]Memory(nil), state.Memories...)
	for i := range mems {
		if mems[i].ID != id {
			continue
		}
		if in.Title != "" {
			mems[i].Title = strings.TrimSpace(in.Title)
		}
		if in.Body != "" {
			mems[i].Body = in.Body
		}
		if in.Tags != nil {
			mems[i].Tags = append([]string(nil), in.Tags...)
		}
		if in.Source != "" {
			mems[i].Source = strings.TrimSpace(in.Source)
		}
		mems[i].UpdatedAt = now
		state.Memories = mems
		return UpdateResult{Memory: mems[i], State: state, OK: true}, nil
	}
	return UpdateResult{State: state, OK: false}, nil
}

// UpdateResult contains the updated memory and the new state.
type UpdateResult struct {
	Memory Memory
	State  session.State
	OK     bool
}

// Delete removes the memory with the given ID. Returns the new state
// and whether a memory was actually removed.
func (s *Store) Delete(state session.State, id string) (session.State, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return state, false
	}
	kept := make([]Memory, 0, len(state.Memories))
	removed := false
	for _, m := range state.Memories {
		if m.ID == id && !removed {
			removed = true
			continue
		}
		kept = append(kept, m)
	}
	if removed {
		state.Memories = kept
	}
	return state, removed
}

// IDGenerator produces unique IDs. The default is the package's
// ULID-backed generator, but callers can swap in a deterministic
// one for tests.
type IDGenerator interface {
	New() string
}

var DefaultIDGenerator IDGenerator = ulidGenerator{}
