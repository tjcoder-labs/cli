package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InteractionKind enumerates the social actions we track for dedup.
type InteractionKind string

const (
	InteractionLike    InteractionKind = "like"    // LinkedIn/Twitter-style "like"
	InteractionUpvote  InteractionKind = "upvote"  // Reddit upvote
	InteractionComment InteractionKind = "comment" // Posted a comment/reply
	InteractionDM      InteractionKind = "dm"      // Sent a direct message / PM
	InteractionSave    InteractionKind = "save"    // Bookmarked
	InteractionOther   InteractionKind = "other"
)

// Interaction is one recorded social action.
type Interaction struct {
	Platform string          `json:"platform"`  // "reddit", "linkedin", "gemini", "google", ...
	Kind     InteractionKind `json:"kind"`
	Target   string          `json:"target"`            // post ID, comment ID, conversation ID, URL slug, etc.
	URL      string          `json:"url,omitempty"`     // canonical URL of the thing interacted with
	Note     string          `json:"note,omitempty"`    // human-readable snippet / subject
	At       time.Time       `json:"at"`
}

// Key returns the dedup key: platform:kind:target.
func (i Interaction) Key() string {
	return strings.ToLower(fmt.Sprintf("%s:%s:%s", i.Platform, i.Kind, i.Target))
}

// InteractionLog is the persisted dedup ledger.
type InteractionLog struct {
	Version    int                      `json:"version"`
	ByKey      map[string]*Interaction  `json:"by_key"`
	Recent     []*Interaction           `json:"recent"` // newest last
	MaxRecent  int                      `json:"-"`
	path       string                   `json:"-"`
}

// DefaultInteractionLogPath returns the shared ledger path under data dir.
func DefaultInteractionLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "share", "coder", "interactions.json")
}

// LoadInteractionLog opens (or initializes) the ledger at path.
func LoadInteractionLog(path string) (*InteractionLog, error) {
	l := &InteractionLog{
		Version:   1,
		ByKey:     map[string]*Interaction{},
		Recent:    []*Interaction{},
		MaxRecent: 500,
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
	if l.ByKey == nil {
		l.ByKey = map[string]*Interaction{}
	}
	if l.MaxRecent == 0 {
		l.MaxRecent = 500
	}
	l.path = path
	return l, nil
}

// Save persists the ledger.
func (l *InteractionLog) Save() error {
	if l.path == "" {
		return fmt.Errorf("interaction log: no path set")
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

// Has reports whether an interaction with platform/kind/target has been recorded.
func (l *InteractionLog) Has(platform string, kind InteractionKind, target string) (*Interaction, bool) {
	if l == nil {
		return nil, false
	}
	probe := Interaction{Platform: strings.ToLower(platform), Kind: kind, Target: target}
	got, ok := l.ByKey[probe.Key()]
	return got, ok
}

// Record adds (or updates) an interaction. Returns true if it was new, false if it replaced.
func (l *InteractionLog) Record(i Interaction) (bool, error) {
	if i.Platform == "" || i.Kind == "" || i.Target == "" {
		return false, fmt.Errorf("platform, kind and target are required")
	}
	if i.At.IsZero() {
		i.At = time.Now().UTC()
	}
	i.Platform = strings.ToLower(i.Platform)
	k := i.Key()
	_, existed := l.ByKey[k]
	cp := i
	l.ByKey[k] = &cp
	l.Recent = append(l.Recent, &cp)
	if l.MaxRecent > 0 && len(l.Recent) > l.MaxRecent {
		l.Recent = l.Recent[len(l.Recent)-l.MaxRecent:]
	}
	if err := l.Save(); err != nil {
		return !existed, err
	}
	return !existed, nil
}

// ListRecent returns the n most recent interactions (newest first).
func (l *InteractionLog) ListRecent(n int) []*Interaction {
	if n <= 0 || n > len(l.Recent) {
		n = len(l.Recent)
	}
	out := make([]*Interaction, 0, n)
	for i := len(l.Recent) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, l.Recent[i])
	}
	return out
}

// Recent returns a shallow copy of the recent list (for tests and callers).
func (l *InteractionLog) RecentList() []*Interaction {
	out := make([]*Interaction, len(l.Recent))
	copy(out, l.Recent)
	return out
}

// CountSince returns the number of recorded interactions matching
// platform and kind whose At time is on or after since. Used by the
// schedule runner to enforce a per-day cap before re-invoking.
func (l *InteractionLog) CountSince(platform string, kind InteractionKind, since time.Time) int {
	if l == nil {
		return 0
	}
	n := 0
	for _, i := range l.Recent {
		if i.At.Before(since) {
			continue
		}
		if platform != "" && !strings.EqualFold(i.Platform, platform) {
			continue
		}
		if kind != "" && i.Kind != kind {
			continue
		}
		n++
	}
	return n
}
