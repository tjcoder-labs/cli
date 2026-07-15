package tracking

import (
	"fmt"
	"strings"

	"github.com/tjcoder-labs/coder-cli/internal/session"
	"github.com/tjcoder-labs/coder-cli/internal/tasks"
)

// TaskTracker adapts the existing tasks.Store to the generic Tracker interface.
type TaskTracker struct {
	store *tasks.Store
}

// NewTaskTracker creates a new task tracker backed by a fresh store.
func NewTaskTracker() *TaskTracker {
	return &TaskTracker{
		store: tasks.NewStore(),
	}
}

// Type returns "task".
func (t *TaskTracker) Type() string {
	return "task"
}

// List returns all tasks as []interface{} for generic consumption.
func (t *TaskTracker) List(state session.State) []interface{} {
	list := tasks.Load(state)
	all := list.All()
	out := make([]interface{}, len(all))
	for i := range all {
		out[i] = all[i]
	}
	return out
}

// Get returns the task with the given ID, or nil.
func (t *TaskTracker) Get(state session.State, id string) interface{} {
	list := tasks.Load(state)
	task, ok := list.Get(id)
	if !ok {
		return nil
	}
	return task
}

// Create adds a new task. Input keys:
//   - "title" (required): task title
//   - "owner" (optional): defaults to "agent"
//   - "meta" (optional): map[string]interface{} of additional metadata
func (t *TaskTracker) Create(state session.State, input map[string]interface{}) (session.State, interface{}, error) {
	title := ""
	if v, ok := input["title"]; ok {
		if s, ok := v.(string); ok {
			title = s
		}
	}
	if strings.TrimSpace(title) == "" {
		return state, nil, fmt.Errorf("title is required")
	}

	owner := ""
	if v, ok := input["owner"]; ok {
		if s, ok := v.(string); ok {
			owner = s
		}
	}

	meta := make(map[string]interface{})
	if v, ok := input["meta"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			meta = m
		}
	}

	result, err := t.store.Create(state, tasks.CreateInput{
		Title: title,
		Owner: owner,
		Meta:  meta,
	})
	if err != nil {
		return state, nil, err
	}
	return result.State, result.Task, nil
}

// Update modifies a task. Input keys:
//   - "id" (required): task ID
//   - "status" (optional): one of "todo", "doing", "blocked", "done", "cancelled"
//   - "title" (optional): new title
//   - "due" (optional): RFC3339 due date, or empty to clear
//   - "priority" (optional): "low", "normal", "high", "critical", or empty to clear
//   - "meta" (optional): map[string]interface{}, replaces or merges depending on context
func (t *TaskTracker) Update(state session.State, id string, input map[string]interface{}) (session.State, interface{}, error) {
	status := ""
	if v, ok := input["status"]; ok {
		if s, ok := v.(string); ok {
			status = s
		}
	}

	title := ""
	if v, ok := input["title"]; ok {
		if s, ok := v.(string); ok {
			title = s
		}
	}

	due := ""
	if v, ok := input["due"]; ok {
		if s, ok := v.(string); ok {
			due = s
		}
	}

	priority := ""
	if v, ok := input["priority"]; ok {
		if s, ok := v.(string); ok {
			priority = s
		}
	}

	meta := make(map[string]interface{})
	if v, ok := input["meta"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			meta = m
		}
	}

	result, err := t.store.Update(state, tasks.UpdateInput{
		ID:        id,
		Status:    status,
		Title:     title,
		Due:       due,
		Priority:  priority,
		Meta:      meta,
		MergeMeta: false, // Replace meta by default; caller can set MergeMeta via "merge_meta" flag if needed
	})
	if err != nil {
		return state, nil, err
	}
	if !result.OK {
		return state, nil, fmt.Errorf("task not found")
	}
	return result.State, result.Task, nil
}

// Delete removes a task with the given ID.
func (t *TaskTracker) Delete(state session.State, id string) (session.State, bool, error) {
	newState, ok := t.store.Delete(state, strings.TrimSpace(id))
	return newState, ok, nil
}

// Format returns a single-line representation of a task for logging/display.
func (t *TaskTracker) Format(obj interface{}) string {
	if task, ok := obj.(tasks.Task); ok {
		return fmt.Sprintf("[%s] %s (%s)", task.Status, task.Title, task.ID)
	}
	return fmt.Sprintf("%v", obj)
}
