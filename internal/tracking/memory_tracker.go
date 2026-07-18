package tracking

import (
	"fmt"
	"strings"

	"github.com/tjcoder-labs/cli/internal/memories"
	"github.com/tjcoder-labs/cli/internal/session"
)

// MemoryTracker adapts memories.Store to the generic Tracker
// interface so manage_items can drive it.
type MemoryTracker struct {
	store *memories.Store
}

func NewMemoryTracker() *MemoryTracker {
	return &MemoryTracker{store: memories.NewStore()}
}

func (t *MemoryTracker) Type() string { return "memory" }

func (t *MemoryTracker) List(state session.State) []interface{} {
	list := t.store.List(state)
	all := list.All()
	out := make([]interface{}, len(all))
	for i := range all {
		out[i] = all[i]
	}
	return out
}

func (t *MemoryTracker) Get(state session.State, id string) interface{} {
	list := t.store.List(state)
	m, ok := list.Get(id)
	if !ok {
		return nil
	}
	return m
}

func (t *MemoryTracker) Create(state session.State, input map[string]interface{}) (session.State, interface{}, error) {
	title, _ := input["title"].(string)
	if strings.TrimSpace(title) == "" {
		return state, nil, fmt.Errorf("title is required")
	}
	body, _ := input["body"].(string)
	tags, _ := input["tags"].([]interface{})
	tagStrs := make([]string, 0, len(tags))
	for _, v := range tags {
		if s, ok := v.(string); ok && s != "" {
			tagStrs = append(tagStrs, s)
		}
	}
	source, _ := input["source"].(string)
	res, err := t.store.Create(state, memories.CreateInput{Title: title, Body: body, Tags: tagStrs, Source: source})
	if err != nil {
		return state, nil, err
	}
	return res.State, res.Memory, nil
}

func (t *MemoryTracker) Update(state session.State, id string, input map[string]interface{}) (session.State, interface{}, error) {
	in := memories.UpdateInput{ID: id}
	if v, ok := input["title"].(string); ok {
		in.Title = v
	}
	if v, ok := input["body"].(string); ok {
		in.Body = v
	}
	if v, ok := input["source"].(string); ok {
		in.Source = v
	}
	if raw, ok := input["tags"].([]interface{}); ok {
		tagStrs := make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				tagStrs = append(tagStrs, s)
			}
		}
		in.Tags = tagStrs
	}
	res, err := t.store.Update(state, in)
	if err != nil {
		return state, nil, err
	}
	if !res.OK {
		return state, nil, fmt.Errorf("memory not found")
	}
	return res.State, res.Memory, nil
}

func (t *MemoryTracker) Delete(state session.State, id string) (session.State, bool, error) {
	newState, ok := t.store.Delete(state, strings.TrimSpace(id))
	return newState, ok, nil
}

func (t *MemoryTracker) Format(obj interface{}) string {
	if m, ok := obj.(memories.Memory); ok {
		return fmt.Sprintf("[%s] %s (%s)", m.Source, m.Title, m.ID)
	}
	return fmt.Sprintf("%v", obj)
}
