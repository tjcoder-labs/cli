package tracking

import (
	"github.com/alpha-tjcoder/coder-cli/internal/session"
)

// Tracker is the generic interface for managing any trackable object type
// (tasks, articles, reminders, etc.). All object types must implement these
// operations so tools can work generically across them.
type Tracker interface {
	// Type returns the object type name (e.g., "task", "article", "reminder").
	Type() string

	// List returns all objects of this type from the session state.
	List(state session.State) []interface{}

	// Get returns the object with the given ID, or nil if not found.
	Get(state session.State, id string) interface{}

	// Create adds a new object and returns (newState, newObject, error).
	// Input is a map[string]interface{} with fields specific to the type.
	Create(state session.State, input map[string]interface{}) (session.State, interface{}, error)

	// Update modifies an existing object and returns (newState, updatedObject, error).
	// Input is a map[string]interface{} with optional fields to update.
	// Unknown IDs should return an error.
	Update(state session.State, id string, input map[string]interface{}) (session.State, interface{}, error)

	// Delete removes the object with the given ID and returns (newState, deletedOK, error).
	Delete(state session.State, id string) (session.State, bool, error)

	// Format returns a human-readable single-line representation of the object.
	// Used for logging and display in agents.
	Format(obj interface{}) string
}

// Registry holds all registered trackers, keyed by type name.
// This allows tools to dispatch to the appropriate handler without hard-coding
// every object type.
type Registry struct {
	trackers map[string]Tracker
}

// NewRegistry creates an empty tracker registry.
func NewRegistry() *Registry {
	return &Registry{
		trackers: make(map[string]Tracker),
	}
}

// Register adds a tracker for the given type. Panics if the type is already registered.
func (r *Registry) Register(t Tracker) {
	name := t.Type()
	if _, exists := r.trackers[name]; exists {
		panic("tracker already registered for type: " + name)
	}
	r.trackers[name] = t
}

// Get returns the tracker for the given type, or nil if not found.
func (r *Registry) Get(typeName string) Tracker {
	return r.trackers[typeName]
}

// Types returns all registered type names in arbitrary order.
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.trackers))
	for name := range r.trackers {
		out = append(out, name)
	}
	return out
}
