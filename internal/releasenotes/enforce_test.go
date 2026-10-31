package releasenotes

// Repository rules for release notes (see the top of AGENTS.md). These
// tests fail whenever a change bumps package.json without a matching
// entry in release-notes/releases.json, breaks the format, or leaves
// CHANGELOG.md stale.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	notesdata "github.com/tjcoder-labs/cli/release-notes"
)

const repo = "../.."

func readRepoFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

func TestReleaseNotesFileIsValid(t *testing.T) {
	data := readRepoFile(t, "release-notes/releases.json")
	if !bytes.Equal(data, notesdata.ReleasesJSON) {
		t.Fatal("embedded releases.json differs from the file on disk; rebuild")
	}
	for _, err := range Validate(data) {
		t.Error(err)
	}
	if _, err := Embedded(); err != nil {
		t.Fatalf("embedded release notes do not parse: %v", err)
	}
}

func TestReleaseNotesHaveEntryForPackageVersion(t *testing.T) {
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(readRepoFile(t, "package.json"), &pkg); err != nil {
		t.Fatalf("package.json: %v", err)
	}
	n, err := Parse(readRepoFile(t, "release-notes/releases.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Releases) == 0 || n.Releases[0].Version != pkg.Version {
		top := "(none)"
		if len(n.Releases) > 0 {
			top = n.Releases[0].Version
		}
		t.Fatalf("package.json is version %s but the newest entry in release-notes/releases.json is %s.\n"+
			"Every version bump needs its release notes in the same commit. Either add a new entry at the top of \"releases\":\n"+
			"  {\"version\": %q, \"date\": \"YYYY-MM-DD\", \"title\": \"...\", \"summary\": \"...\", \"changes\": {\"fixed\": [{\"text\": \"...\"}]}}\n"+
			"or, if the newest entry is not released yet, change its \"version\" to %q and set \"covers_from\" to its old version (keep covers_from if it already has one).\n"+
			"Then run `go run ./cmd/releasenotes` to regenerate CHANGELOG.md. See the top of AGENTS.md.",
			pkg.Version, top, pkg.Version, pkg.Version)
	}
}

func TestChangelogIsUpToDate(t *testing.T) {
	n, err := Parse(readRepoFile(t, "release-notes/releases.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(readRepoFile(t, "CHANGELOG.md")), Markdown(n); got != want {
		t.Fatal("CHANGELOG.md is out of date with release-notes/releases.json.\n" +
			"Do not edit CHANGELOG.md by hand: run `go run ./cmd/releasenotes` and commit the result.")
	}
}

// TestSchemaMatchesGo keeps schema.json and the Go types in step, so a
// field added to one is added to the other.
func TestSchemaMatchesGo(t *testing.T) {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       struct {
			Release struct {
				Required   []string `json:"required"`
				Properties struct {
					Changes struct {
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"changes"`
				} `json:"properties"`
			} `json:"release"`
			Item struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"item"`
		} `json:"$defs"`
	}
	raw := readRepoFile(t, "release-notes/schema.json")
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("release-notes/schema.json is not valid JSON: %v", err)
	}
	var relProps struct {
		Defs struct {
			Release struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"release"`
		} `json:"$defs"`
	}
	_ = json.Unmarshal(raw, &relProps)

	check := func(what string, schema map[string]json.RawMessage, goKeys []string) {
		t.Helper()
		got := keys(schema)
		sort.Strings(goKeys)
		if !reflect.DeepEqual(got, goKeys) {
			t.Errorf("%s: schema.json has %v but the Go type has %v; update both (additive changes only, bump schema_version)", what, got, goKeys)
		}
	}
	check("top-level properties", s.Properties, append(jsonKeys(Notes{}), "$schema"))
	check("release properties", relProps.Defs.Release.Properties, jsonKeys(Release{}))
	check("item properties", s.Defs.Item.Properties, jsonKeys(Item{}))
	check("change categories", s.Defs.Release.Properties.Changes.Properties, append([]string(nil), Categories...))
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func jsonKeys(v any) []string {
	var out []string
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}
