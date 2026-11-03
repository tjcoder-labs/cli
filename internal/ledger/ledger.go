// Package ledger implements a generalized, open-ended event ledger used to
// track arbitrary events and enforce deduplication of interactions (messages,
// correspondences, social actions, or any other data requiring a record).
//
// The ledger is deliberately schema-free: entries are identified by a
// (namespace, key) pair, where both are free strings, and carry an optional
// arbitrary JSON "data" payload plus an optional time-to-live. This replaces
// the older, enum-constrained social interaction log with a general facility.
package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry is one recorded event in the ledger.
type Entry struct {
	Namespace string          `json:"namespace"` // free-form scope: "email", "social", "billing", ...
	Key       string          `json:"key"`       // stable dedup identity within the namespace
	Kind      string          `json:"kind,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"` // arbitrary payload
	Note      string          `json:"note,omitempty"`
	At        time.Time       `json:"at"`
	TTL       time.Duration   `json:"ttl,omitempty"` // expiry after At; 0 = never
}

// Expired reports whether the entry's TTL has elapsed relative to now.
func (e Entry) Expired(now time.Time) bool {
	if e.TTL <= 0 {
		return false
	}
	return now.After(e.At.Add(e.TTL))
}

// ID returns the dedup identity: lowercased "namespace:key".
func (e Entry) ID() string {
	return ID(e.Namespace, e.Key)
}

// ID returns the normalized dedup identity for a namespace/key pair.
func ID(namespace, key string) string {
	return strings.ToLower(strings.TrimSpace(namespace) + ":" + strings.TrimSpace(key))
}

// Ledger is the persisted event ledger.
type Ledger struct {
	Version   int               `json:"version"`
	ByID      map[string]*Entry `json:"by_id"`
	Recent    []*Entry          `json:"recent"` // newest last
	MaxRecent int               `json:"-"`
	path      string            `json:"-"`
}

// DefaultPath returns the shared ledger path under the data dir.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "share", "coder", "ledger.json")
}

// Load opens (or initializes) the ledger at path.
func Load(path string) (*Ledger, error) {
	l := &Ledger{
		Version:   1,
		ByID:      map[string]*Entry{},
		Recent:    []*Entry{},
		MaxRecent: 1000,
		path:      path,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, l); err != nil {
		return nil, err
	}
	if l.ByID == nil {
		l.ByID = map[string]*Entry{}
	}
	if l.MaxRecent == 0 {
		l.MaxRecent = 1000
	}
	l.path = path
	return l, nil
}

// Save persists the ledger.
func (l *Ledger) Save() error {
	if l.path == "" {
		return fmt.Errorf("ledger: no path set")
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(l.path, data, 0o600)
}

// Get returns the live entry for namespace/key, or nil. Expired entries are
// pruned and reported as absent.
func (l *Ledger) Get(namespace, key string) *Entry {
	if l == nil {
		return nil
	}
	e, ok := l.ByID[ID(namespace, key)]
	if !ok {
		return nil
	}
	if e.Expired(time.Now()) {
		l.deleteByID(e.ID())
		return nil
	}
	return e
}

// Has reports whether namespace/key is recorded (and not expired).
func (l *Ledger) Has(namespace, key string) (*Entry, bool) {
	e := l.Get(namespace, key)
	return e, e != nil
}

// Record adds or updates an entry. Returns true if it was new, false if it
// replaced an existing live entry.
func (l *Ledger) Record(e Entry) (bool, error) {
	if strings.TrimSpace(e.Namespace) == "" || strings.TrimSpace(e.Key) == "" {
		return false, fmt.Errorf("namespace and key are required")
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	e.Namespace = strings.TrimSpace(e.Namespace)
	id := e.ID()
	_, existed := l.ByID[id]
	cp := e
	l.ByID[id] = &cp
	// Replace the prior entry in Recent (same ID) rather than appending a
	// duplicate, so ListRecent/Count never double-count a re-recorded key.
	replaced := false
	for i, r := range l.Recent {
		if r.ID() == id {
			l.Recent[i] = &cp
			replaced = true
			break
		}
	}
	if !replaced {
		l.Recent = append(l.Recent, &cp)
	}
	if l.MaxRecent > 0 && len(l.Recent) > l.MaxRecent {
		l.Recent = l.Recent[len(l.Recent)-l.MaxRecent:]
	}
	if err := l.Save(); err != nil {
		return !existed, err
	}
	return !existed, nil
}

// Delete removes namespace/key. Returns true if it was present.
func (l *Ledger) Delete(namespace, key string) bool {
	return l.deleteByID(ID(namespace, key))
}

func (l *Ledger) deleteByID(id string) bool {
	_, ok := l.ByID[id]
	if !ok {
		return false
	}
	delete(l.ByID, id)
	// Rebuild Recent without the removed entry.
	kept := l.Recent[:0]
	for _, e := range l.Recent {
		if e.ID() != id {
			kept = append(kept, e)
		}
	}
	l.Recent = kept
	return true
}

// Clear removes every entry in a namespace. Returns the count removed.
func (l *Ledger) Clear(namespace string) int {
	n := 0
	for _, e := range l.Recent {
		if strings.EqualFold(e.Namespace, namespace) {
			n++
		}
	}
	for id, e := range l.ByID {
		if strings.EqualFold(e.Namespace, namespace) {
			delete(l.ByID, id)
		}
	}
	kept := l.Recent[:0]
	for _, e := range l.Recent {
		if !strings.EqualFold(e.Namespace, namespace) {
			kept = append(kept, e)
		}
	}
	l.Recent = kept
	return n
}

// ListRecent returns up to n live entries (newest first), optionally filtered
// by namespace and kind.
func (l *Ledger) ListRecent(n int, namespace, kind string) []*Entry {
	now := time.Now()
	out := make([]*Entry, 0, n)
	for i := len(l.Recent) - 1; i >= 0 && len(out) < n; i-- {
		e := l.Recent[i]
		if e.Expired(now) {
			continue
		}
		if namespace != "" && !strings.EqualFold(e.Namespace, namespace) {
			continue
		}
		if kind != "" && !strings.EqualFold(e.Kind, kind) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Count returns the number of live entries matching namespace and kind.
func (l *Ledger) Count(namespace, kind string) int {
	n := 0
	now := time.Now()
	for _, e := range l.Recent {
		if e.Expired(now) {
			continue
		}
		if namespace != "" && !strings.EqualFold(e.Namespace, namespace) {
			continue
		}
		if kind != "" && !strings.EqualFold(e.Kind, kind) {
			continue
		}
		n++
	}
	return n
}

// CountSince returns the number of live entries matching namespace and kind
// whose At is on or after since.
func (l *Ledger) CountSince(namespace, kind string, since time.Time) int {
	if l == nil {
		return 0
	}
	n := 0
	for _, e := range l.Recent {
		if e.At.Before(since) {
			continue
		}
		if namespace != "" && !strings.EqualFold(e.Namespace, namespace) {
			continue
		}
		if kind != "" && !strings.EqualFold(e.Kind, kind) {
			continue
		}
		n++
	}
	return n
}
