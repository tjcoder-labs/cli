package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Ownership describes who owns a browser tab from the agent's point of view.
type Ownership string

const (
	// OwnMine is a tab created or claimed by this session.
	OwnMine Ownership = "mine"
	// OwnOther is a tab claimed by another coder session/agent.
	OwnOther Ownership = "other-agent"
	// OwnUnclaimed is a tab the user (or something else) opened.
	OwnUnclaimed Ownership = "unclaimed"
)

// registryRecord is one entry in the on-disk tab ownership table.
type registryRecord struct {
	SessionID string    `json:"session_id"`
	Agent     string    `json:"agent,omitempty"`
	Claimed   bool      `json:"claimed,omitempty"` // true=claimed, false=created
	CreatedAt time.Time `json:"created_at"`
}

// Registry is a persistent tab ownership table so multiple concurrent agents
// sharing one Chrome instance never operate on each other's tabs.
type Registry struct {
	mu   sync.Mutex
	path string
	self string // this session's identity
	seen map[string]registryRecord
}

// registryFile is the on-disk schema.
type registryFile struct {
	Records map[string]registryRecord `json:"records"` // targetID -> record
}

// DefaultRegistryPath returns the shared, user-level registry path. It lives
// outside any workspace so all coder sessions on the machine coordinate.
func DefaultRegistryPath() string {
	if dir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(dir, ".local", "share", "coder", "browser-tabs.json")
	}
	return filepath.Join(os.TempDir(), "coder-browser-tabs.json")
}

// NewRegistry opens (creating if needed) the ownership table at path.
// selfID uniquely identifies this agent session (e.g. "<pid>:<agent>").
func NewRegistry(path, selfID string) (*Registry, error) {
	if path == "" {
		path = DefaultRegistryPath()
	}
	r := &Registry{path: path, self: selfID, seen: map[string]registryRecord{}}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = map[string]registryRecord{}
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f registryFile
	if err := json.Unmarshal(data, &f); err != nil {
		// Corrupt registry: back it up and start fresh rather than failing.
		_ = os.Rename(r.path, r.path+".corrupt")
		return nil
	}
	if f.Records != nil {
		r.seen = f.Records
	}
	return nil
}

func (r *Registry) saveLocked() error {
	f := registryFile{Records: r.seen}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Register marks targetID owned by this session. claimed=false means the tab
// was created by us; claimed=true means we took over an existing tab.
func (r *Registry) Register(targetID, agent string, claimed bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen[targetID] = registryRecord{SessionID: r.self, Agent: agent, Claimed: claimed, CreatedAt: time.Now()}
	return r.saveLocked()
}

// Release drops ownership of targetID if this session owns it.
func (r *Registry) Release(targetID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.seen[targetID]; ok && rec.SessionID == r.self {
		delete(r.seen, targetID)
		return r.saveLocked()
	}
	return nil
}

// OwnershipOf classifies a target relative to this session.
func (r *Registry) OwnershipOf(targetID string) Ownership {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.seen[targetID]
	if !ok {
		return OwnUnclaimed
	}
	if rec.SessionID == r.self {
		return OwnMine
	}
	return OwnOther
}

// ErrNotOwned is returned when an action is attempted on a tab this session
// does not own.
var ErrNotOwned = errors.New("tab is not owned by this session (claim it first)")

// RequireOwned returns ErrNotOwned unless targetID belongs to this session.
func (r *Registry) RequireOwned(targetID string) error {
	if r.OwnershipOf(targetID) != OwnMine {
		return fmt.Errorf("%w: %s", ErrNotOwned, targetID)
	}
	return nil
}

// Claim transfers an unclaimed (or own) target to this session. It refuses to
// claim a tab owned by another session unless force is set.
func (r *Registry) Claim(targetID, agent string, force bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.seen[targetID]; ok && rec.SessionID != r.self && rec.SessionID != "" {
		if !force {
			return fmt.Errorf("tab %s already claimed by another session (%s)", targetID, rec.SessionID)
		}
	}
	r.seen[targetID] = registryRecord{SessionID: r.self, Agent: agent, Claimed: true, CreatedAt: time.Now()}
	return r.saveLocked()
}

// GC removes records for targetIDs that no longer exist, and entries from
// dead sessions. live is the set of current targetIDs.
func (r *Registry) GC(live map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	for id := range r.seen {
		if !live[id] {
			delete(r.seen, id)
			changed = true
		}
	}
	if changed {
		_ = r.saveLocked()
	}
}
