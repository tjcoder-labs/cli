package releasenotes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var commandPattern = regexp.MustCompile(`^/[a-z][a-z0-9-]*$`)

// fixHint ends every validation error so an agent knows what to do.
const fixHint = "edit release-notes/releases.json (format: release-notes/schema.json, rules: AGENTS.md), then run `go run ./cmd/releasenotes`"

// Validate checks raw releases.json strictly: unknown fields, unknown
// categories, ordering, duplicates, dates and empty text. It returns
// every problem found, each with a fix hint.
func Validate(data []byte) []error {
	var errs []error
	add := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf(format+" — "+fixHint, a...))
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var n struct {
		Schema string `json:"$schema"`
		Notes
	}
	if err := dec.Decode(&n); err != nil {
		add("releases.json does not match the schema: %v", err)
		return errs
	}
	if n.SchemaVersion != SchemaVersion {
		add("schema_version is %d, want %d (bump releasenotes.SchemaVersion and schema.json together, additive changes only)", n.SchemaVersion, SchemaVersion)
	}
	if len(n.Releases) == 0 {
		add("releases is empty")
	}

	known := map[string]bool{}
	for _, c := range Categories {
		known[c] = true
	}
	seen := map[string]bool{}
	for i, r := range n.Releases {
		where := fmt.Sprintf("releases[%d] (version %q)", i, r.Version)
		if !validSemver(r.Version) || strings.HasPrefix(r.Version, "v") {
			add("%s: version must be semver like 1.2.3 without a leading v", where)
			continue
		}
		if seen[r.Version] {
			add("%s: duplicate version; merge the two entries", where)
		}
		seen[r.Version] = true
		if i > 0 && validSemver(n.Releases[i-1].Version) && Compare(n.Releases[i-1].Version, r.Version) <= 0 {
			add("%s: entries must be newest first; move it above %q", where, n.Releases[i-1].Version)
		}
		if r.CoversFrom != "" {
			if !validSemver(r.CoversFrom) || strings.HasPrefix(r.CoversFrom, "v") {
				add("%s: covers_from %q must be semver without a leading v", where, r.CoversFrom)
			} else if Compare(r.CoversFrom, r.Version) >= 0 {
				add("%s: covers_from %q must be lower than version", where, r.CoversFrom)
			} else if i+1 < len(n.Releases) && validSemver(n.Releases[i+1].Version) && Compare(r.CoversFrom, n.Releases[i+1].Version) <= 0 {
				add("%s: covers_from %q overlaps the older entry %q", where, r.CoversFrom, n.Releases[i+1].Version)
			}
		}
		if r.MinUpgradeFrom != "" && (!validSemver(r.MinUpgradeFrom) || strings.HasPrefix(r.MinUpgradeFrom, "v")) {
			add("%s: min_upgrade_from %q must be semver without a leading v", where, r.MinUpgradeFrom)
		}
		if t, err := time.Parse("2006-01-02", r.Date); err != nil || t.Format("2006-01-02") != r.Date {
			add("%s: date %q must be a real YYYY-MM-DD date (America/Denver)", where, r.Date)
		}
		if strings.TrimSpace(r.Title) == "" {
			add("%s: title is empty", where)
		} else if len([]rune(r.Title)) > 80 {
			add("%s: title is longer than 80 characters", where)
		}
		if strings.TrimSpace(r.Summary) == "" {
			add("%s: summary is empty", where)
		}
		if len(r.Changes) == 0 {
			add("%s: changes is empty; add at least one item under added, changed, deprecated, removed, fixed or security", where)
		}
		for cat, items := range r.Changes {
			if !known[cat] {
				add("%s: unknown category %q; use one of %s", where, cat, strings.Join(Categories, ", "))
				continue
			}
			if len(items) == 0 {
				add("%s: category %q has no items; remove it or add one", where, cat)
			}
			for j, it := range items {
				iw := fmt.Sprintf("%s changes.%s[%d]", where, cat, j)
				if strings.TrimSpace(it.Text) == "" {
					add("%s: text is empty", iw)
				} else if len([]rune(it.Text)) > 200 {
					add("%s: text is longer than 200 characters; move the rest into details", iw)
				} else if strings.Contains(it.Text, "\n") {
					add("%s: text must be one line; move the rest into details", iw)
				}
				if it.Details != "" && strings.TrimSpace(it.Details) == "" {
					add("%s: details is blank; remove it", iw)
				}
				for _, c := range it.Commands {
					if !commandPattern.MatchString(c) {
						add("%s: command %q must look like /name", iw, c)
					}
				}
			}
		}
		for j, note := range r.Notes {
			if strings.TrimSpace(note) == "" {
				add("%s notes[%d]: note is empty", where, j)
			}
		}
	}
	return errs
}
