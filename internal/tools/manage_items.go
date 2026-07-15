package tools

import (
	"fmt"
	"strings"

	"github.com/tjcoder-labs/coder-cli/internal/session"
	"github.com/tjcoder-labs/coder-cli/internal/tracking"
)

// ManageItems is a generic tool for creating, updating, listing, and deleting
// trackable objects (tasks, articles, reminders, etc.). The operation is
// determined by the "action" parameter, and the object type by "type" parameter.
type ManageItems struct {
	trackerRegistry *tracking.Registry
	sessionState    *session.State // Pointer to allow mutations
}

// NewManageItems creates a new item management tool.
func NewManageItems(registry *tracking.Registry, sessionState *session.State) *ManageItems {
	return &ManageItems{
		trackerRegistry: registry,
		sessionState:    sessionState,
	}
}

// Name returns the tool name.
func (m *ManageItems) Name() string {
	return "manage_items"
}

// Description returns the tool description.
func (m *ManageItems) Description() string {
	return `Manage trackable objects (tasks, articles, reminders, etc.). Operations:
- "create": Create a new item. Requires "type" and "data" (object with creation fields).
- "list": List all items of a given type. Requires "type".
- "get": Retrieve a single item by ID. Requires "type" and "id".
- "update": Modify an existing item. Requires "type", "id", and "data" (partial object with fields to update).
- "delete": Remove an item by ID. Requires "type" and "id".
Example: {"action":"create","type":"task","data":{"title":"Fix bug #42"}}`
}

// Schema returns the JSON schema for tool arguments.
func (m *ManageItems) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Operation: create, list, get, update, or delete",
				"enum":        []string{"create", "list", "get", "update", "delete"},
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "Object type: task, article, reminder, etc.",
			},
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Object ID (for get, update, delete)",
			},
			"data": map[string]interface{}{
				"type":        "object",
				"description": "Object data (for create and update)",
			},
		},
		"required": []string{"action", "type"},
	}
}

// Call executes the requested item management operation.
func (m *ManageItems) Call(args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	typeName, _ := args["type"].(string)

	if typeName == "" {
		return "", fmt.Errorf("type is required")
	}

	tracker := m.trackerRegistry.Get(typeName)
	if tracker == nil {
		return "", fmt.Errorf("unknown item type %q (available: %s)", typeName, strings.Join(m.trackerRegistry.Types(), ", "))
	}

	switch strings.ToLower(strings.TrimSpace(action)) {
	case "create":
		return m.handleCreate(tracker, args)
	case "list":
		return m.handleList(tracker)
	case "get":
		return m.handleGet(tracker, args)
	case "update":
		return m.handleUpdate(tracker, args)
	case "delete":
		return m.handleDelete(tracker, args)
	default:
		return "", fmt.Errorf("unknown action %q (valid: create, list, get, update, delete)", action)
	}
}

func (m *ManageItems) handleCreate(tracker tracking.Tracker, args map[string]interface{}) (string, error) {
	var data map[string]interface{}
	if v, ok := args["data"]; ok {
		if d, ok := v.(map[string]interface{}); ok {
			data = d
		}
	}
	if data == nil {
		data = make(map[string]interface{})
	}

	newState, obj, err := tracker.Create(*m.sessionState, data)
	if err != nil {
		return "", err
	}

	// Persist the updated state
	*m.sessionState = newState

	return fmt.Sprintf("Created %s: %s", tracker.Type(), tracker.Format(obj)), nil
}

func (m *ManageItems) handleList(tracker tracking.Tracker) (string, error) {
	items := tracker.List(*m.sessionState)
	if len(items) == 0 {
		return fmt.Sprintf("No %ss found", tracker.Type()), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d %ss:\n", len(items), tracker.Type()))
	for _, item := range items {
		b.WriteString(fmt.Sprintf("  - %s\n", tracker.Format(item)))
	}
	return b.String(), nil
}

func (m *ManageItems) handleGet(tracker tracking.Tracker, args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required for get")
	}

	obj := tracker.Get(*m.sessionState, id)
	if obj == nil {
		return "", fmt.Errorf("%s not found", tracker.Type())
	}

	return fmt.Sprintf("Found %s: %s", tracker.Type(), tracker.Format(obj)), nil
}

func (m *ManageItems) handleUpdate(tracker tracking.Tracker, args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required for update")
	}

	var data map[string]interface{}
	if v, ok := args["data"]; ok {
		if d, ok := v.(map[string]interface{}); ok {
			data = d
		}
	}
	if data == nil {
		data = make(map[string]interface{})
	}

	newState, obj, err := tracker.Update(*m.sessionState, id, data)
	if err != nil {
		return "", err
	}

	// Persist the updated state
	*m.sessionState = newState

	return fmt.Sprintf("Updated %s: %s", tracker.Type(), tracker.Format(obj)), nil
}

func (m *ManageItems) handleDelete(tracker tracking.Tracker, args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required for delete")
	}

	newState, ok, err := tracker.Delete(*m.sessionState, id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%s not found", tracker.Type())
	}

	// Persist the updated state
	*m.sessionState = newState

	return fmt.Sprintf("Deleted %s %s", tracker.Type(), id), nil
}
