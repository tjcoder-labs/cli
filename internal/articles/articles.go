// Package articles owns the in-memory store of long-form articles
// the user (or agent) has saved for later review. An article is
// conceptually heavier than a memory: it has a body, optional tags,
// and an optional source URL/path. Articles are stored in
// session.json alongside tasks and memories, and surfaced both in
// the TUI's right-column list and in the agent's prompt via the
// manage_items tool.
package articles

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tjcoder-labs/coder-cli/internal/session"
)

// Article is the public alias for the persisted type so callers
// don't have to import internal/session just to refer to an article.
type Article = session.Article

// List is an ordered, in-memory view of the articles stored in a
// session. Mutations go through Store, which produces a fresh List
// each time.
type List struct {
	items []Article
	byID  map[string]int
}

// Load reads the article slice from a session.State and returns a
// sorted List. An empty/nil slice yields a non-nil empty list.
func Load(state session.State) *List {
	l := &List{
		items: append([]Article(nil), state.Articles...),
		byID:  make(map[string]int, len(state.Articles)),
	}
	for i, a := range l.items {
		l.byID[a.ID] = i
	}
	l.sort()
	return l
}

// Len returns the number of articles.
func (l *List) Len() int { return len(l.items) }

// All returns a copy of the articles in display order.
func (l *List) All() []Article {
	out := make([]Article, len(l.items))
	copy(out, l.items)
	return out
}

// Get returns the article with the given id and whether it was found.
func (l *List) Get(id string) (Article, bool) {
	if l == nil {
		return Article{}, false
	}
	idx, ok := l.byID[id]
	if !ok {
		return Article{}, false
	}
	return l.items[idx], true
}

// IDs returns the article IDs in display order. Useful for the TUI
// panel's selection cursor.
func (l *List) IDs() []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l.items))
	for i, a := range l.items {
		out[i] = a.ID
	}
	return out
}

func (l *List) sort() {
	sort.SliceStable(l.items, func(i, j int) bool {
		return l.items[i].UpdatedAt > l.items[j].UpdatedAt
	})
	l.byID = make(map[string]int, len(l.items))
	for i, a := range l.items {
		l.byID[a.ID] = i
	}
}

// Store is the mutation surface for articles. Mirrors the shape of
// tasks.Store and memories.Store so the three can be used
// interchangeably from the TUI.
type Store struct {
	clock func() time.Time
	ids   IDGenerator
}

// NewStore returns a Store with default clock and ID generators.
func NewStore() *Store {
	return &Store{clock: time.Now, ids: DefaultIDGenerator}
}

// List returns the current list, sorted.
func (s *Store) List(state session.State) *List { return Load(state) }

// CreateInput captures the parameters for a new article. Empty Title
// is rejected; Source defaults to "local" when blank.
type CreateInput struct {
	Title  string
	Body   string
	Tags   []string
	Source string
}

// CreateResult contains the new article and the updated state.
type CreateResult struct {
	Article Article
	State   session.State
}

// Create appends a new article to the state's slice.
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
	a := Article{
		ID:        s.ids.New(),
		Title:     title,
		Body:      in.Body,
		Tags:      append([]string(nil), in.Tags...),
		CreatedAt: now,
		UpdatedAt: now,
		Source:    source,
	}
	state.Articles = append(append([]Article(nil), state.Articles...), a)
	return CreateResult{Article: a, State: state}, nil
}

// UpdateInput specifies which fields to change on an existing article.
type UpdateInput struct {
	ID     string
	Title  string
	Body   string
	Tags   []string
	Source string
}

// UpdateResult contains the updated article and the new state.
type UpdateResult struct {
	Article Article
	State   session.State
	OK      bool
}

// Update mutates the article with the given ID.
func (s *Store) Update(state session.State, in UpdateInput) (UpdateResult, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return UpdateResult{}, fmt.Errorf("id is required")
	}
	now := s.clock().UTC().Format(time.RFC3339)
	arts := append([]Article(nil), state.Articles...)
	for i := range arts {
		if arts[i].ID != id {
			continue
		}
		if in.Title != "" {
			arts[i].Title = strings.TrimSpace(in.Title)
		}
		if in.Body != "" {
			arts[i].Body = in.Body
		}
		if in.Tags != nil {
			arts[i].Tags = append([]string(nil), in.Tags...)
		}
		if in.Source != "" {
			arts[i].Source = strings.TrimSpace(in.Source)
		}
		arts[i].UpdatedAt = now
		state.Articles = arts
		return UpdateResult{Article: arts[i], State: state, OK: true}, nil
	}
	return UpdateResult{State: state, OK: false}, nil
}

// Delete removes the article with the given ID.
func (s *Store) Delete(state session.State, id string) (session.State, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return state, false
	}
	kept := make([]Article, 0, len(state.Articles))
	removed := false
	for _, a := range state.Articles {
		if a.ID == id && !removed {
			removed = true
			continue
		}
		kept = append(kept, a)
	}
	if removed {
		state.Articles = kept
	}
	return state, removed
}

// IDGenerator produces unique IDs.
type IDGenerator interface {
	New() string
}

var DefaultIDGenerator IDGenerator = ulidGenerator{}
