// Package releasenotes reads the structured release notes in
// release-notes/releases.json (embedded in the binary) and answers
// "what changed" questions for the update system.
//
// The JSON is the source of truth; CHANGELOG.md is generated from it
// with `go run ./cmd/releasenotes`. Parse is lenient (unknown fields,
// categories and newer schema versions are tolerated) so an older
// binary can read a newer file fetched from the repository. Validate is
// strict and is what the repository's tests enforce.
package releasenotes

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	notesdata "github.com/tjcoder-labs/cli/release-notes"
)

// SchemaVersion is the newest releases.json format this package knows.
// Schema changes must be additive; bump this (and the file's
// schema_version) when adding fields.
const SchemaVersion = 1

// Categories lists the allowed change categories in display order.
var Categories = []string{"added", "changed", "deprecated", "removed", "fixed", "security"}

// Notes is the parsed releases.json.
type Notes struct {
	SchemaVersion int       `json:"schema_version"`
	Releases      []Release `json:"releases"`
}

// Release is one entry. Version is the package.json version it ships
// in; CoversFrom, when set, is the first version of a batch the entry
// summarises.
type Release struct {
	Version         string            `json:"version"`
	CoversFrom      string            `json:"covers_from,omitempty"`
	Date            string            `json:"date"`
	Title           string            `json:"title"`
	Summary         string            `json:"summary"`
	MinUpgradeFrom  string            `json:"min_upgrade_from,omitempty"`
	RequiresRestart bool              `json:"requires_restart,omitempty"`
	Changes         map[string][]Item `json:"changes"`
	Notes           []string          `json:"notes,omitempty"`
}

// Item is one change.
type Item struct {
	Text      string   `json:"text"`
	Details   string   `json:"details,omitempty"`
	Commands  []string `json:"commands,omitempty"`
	Breaking  bool     `json:"breaking,omitempty"`
	Highlight bool     `json:"highlight,omitempty"`
}

// Parse decodes releases.json leniently.
func Parse(data []byte) (*Notes, error) {
	var n Notes
	if err := json.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("releasenotes: %w", err)
	}
	if n.SchemaVersion < 1 {
		return nil, fmt.Errorf("releasenotes: missing or invalid schema_version")
	}
	return &n, nil
}

var (
	embeddedOnce sync.Once
	embedded     *Notes
	embeddedErr  error
)

// Embedded returns the release notes compiled into this binary.
func Embedded() (*Notes, error) {
	embeddedOnce.Do(func() { embedded, embeddedErr = Parse(notesdata.ReleasesJSON) })
	return embedded, embeddedErr
}

func embeddedOrEmpty() *Notes {
	n, err := Embedded()
	if err != nil {
		return &Notes{}
	}
	return n
}

// Latest returns the newest embedded entry.
func Latest() (Release, bool) { return embeddedOrEmpty().Latest() }

// ForVersion returns the embedded entry describing version v.
func ForVersion(v string) (Release, bool) { return embeddedOrEmpty().ForVersion(v) }

// Since returns embedded entries newer than installed, newest first.
func Since(installed string) []Release { return embeddedOrEmpty().Since(installed) }

// Latest returns the entry with the highest version.
func (n *Notes) Latest() (Release, bool) {
	var best Release
	found := false
	for _, r := range n.Releases {
		if !validSemver(r.Version) {
			continue
		}
		if !found || Compare(r.Version, best.Version) > 0 {
			best, found = r, true
		}
	}
	return best, found
}

// ForVersion returns the entry for v: an exact match, or the batch
// entry whose covers_from..version range contains v.
func (n *Notes) ForVersion(v string) (Release, bool) {
	v = normalize(v)
	for _, r := range n.Releases {
		if validSemver(r.Version) && Compare(r.Version, v) == 0 {
			return r, true
		}
	}
	for _, r := range n.Releases {
		if r.CoversFrom != "" && validSemver(r.CoversFrom) && validSemver(r.Version) &&
			Compare(r.CoversFrom, v) <= 0 && Compare(v, r.Version) <= 0 {
			return r, true
		}
	}
	return Release{}, false
}

// Since returns entries newer than installed, newest first. An empty
// or invalid installed version returns everything.
func (n *Notes) Since(installed string) []Release {
	return n.Between(installed, "")
}

// Between returns entries with from < version <= to, newest first. An
// empty to means no upper bound. Use it with the latest version from an
// update check to show what an upgrade brings.
func (n *Notes) Between(from, to string) []Release {
	from, to = normalize(from), normalize(to)
	var out []Release
	for _, r := range n.Releases {
		if !validSemver(r.Version) {
			continue
		}
		if validSemver(from) && Compare(r.Version, from) <= 0 {
			continue
		}
		if validSemver(to) && Compare(r.Version, to) > 0 {
			continue
		}
		out = append(out, r)
	}
	sortNewestFirst(out)
	return out
}

func sortNewestFirst(rs []Release) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && Compare(rs[j].Version, rs[j-1].Version) > 0; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

// Highlights returns the items marked highlight, in category order.
func (r Release) Highlights() []Item {
	var out []Item
	for _, c := range Categories {
		for _, it := range r.Changes[c] {
			if it.Highlight {
				out = append(out, it)
			}
		}
	}
	return out
}

// Breaking reports whether any item is marked breaking.
func (r Release) Breaking() bool {
	for _, items := range r.Changes {
		for _, it := range items {
			if it.Breaking {
				return true
			}
		}
	}
	return false
}

// Compare compares two semver strings (leading "v" allowed) and
// returns -1, 0 or 1. A pre-release sorts before its release. It
// mirrors internal/update.Compare without importing it, so the update
// package can depend on this one.
func Compare(a, b string) int {
	a, b = normalize(a), normalize(b)
	ac, ap := splitPre(a)
	bc, bp := splitPre(b)
	as, bs := strings.Split(ac, "."), strings.Split(bc, ".")
	for i := 0; i < 3; i++ {
		x, y := num(as, i), num(bs, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	case ap < bp:
		return -1
	default:
		return 1
	}
}

func normalize(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

func splitPre(v string) (core, pre string) {
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

func num(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, _ := strconv.Atoi(parts[i])
	return n
}

func validSemver(v string) bool {
	core, pre := splitPre(normalize(v))
	if strings.Contains(v, "-") && pre == "" {
		return false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
