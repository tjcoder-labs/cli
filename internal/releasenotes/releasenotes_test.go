package releasenotes

import (
	"strings"
	"testing"
)

const sample = `{"schema_version":1,"releases":[
 {"version":"1.3.0","covers_from":"1.2.1","date":"2026-10-03","title":"C","summary":"s","changes":{"added":[{"text":"c","highlight":true}],"fixed":[{"text":"d","breaking":true}]}},
 {"version":"1.2.0","date":"2026-10-02","title":"B","summary":"s","changes":{"fixed":[{"text":"b"}]}},
 {"version":"1.0.0","date":"2026-10-01","title":"A","summary":"s","changes":{"added":[{"text":"a"}]}}
]}`

func mustParse(t *testing.T, s string) *Notes {
	t.Helper()
	n, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func versions(rs []Release) string {
	var v []string
	for _, r := range rs {
		v = append(v, r.Version)
	}
	return strings.Join(v, ",")
}

func TestQueries(t *testing.T) {
	n := mustParse(t, sample)
	if r, ok := n.Latest(); !ok || r.Version != "1.3.0" {
		t.Fatalf("Latest = %v %v", r.Version, ok)
	}
	for v, want := range map[string]string{"1.2.0": "1.2.0", "v1.0.0": "1.0.0", "1.2.5": "1.3.0", "1.2.1": "1.3.0", "1.1.0": ""} {
		r, ok := n.ForVersion(v)
		if (want == "") == ok || r.Version != want {
			t.Errorf("ForVersion(%s) = %q %v, want %q", v, r.Version, ok, want)
		}
	}
	for from, want := range map[string]string{"1.0.0": "1.3.0,1.2.0", "1.2.0": "1.3.0", "1.3.0": "", "": "1.3.0,1.2.0,1.0.0", "v0.9.0": "1.3.0,1.2.0,1.0.0"} {
		if got := versions(n.Since(from)); got != want {
			t.Errorf("Since(%q) = %q, want %q", from, got, want)
		}
	}
	if got := versions(n.Between("1.0.0", "1.2.0")); got != "1.2.0" {
		t.Errorf("Between = %q", got)
	}
	r, _ := n.Latest()
	if h := r.Highlights(); len(h) != 1 || h[0].Text != "c" || !r.Breaking() {
		t.Errorf("Highlights/Breaking wrong: %v %v", h, r.Breaking())
	}
}

func TestParseIsLenientForNewerFiles(t *testing.T) {
	n := mustParse(t, `{"schema_version":2,"future":1,"releases":[{"version":"2.0.0","date":"2027-01-01","title":"t","summary":"s","new_field":true,"changes":{"improved":[{"text":"x"}]}}]}`)
	if r, ok := n.Latest(); !ok || r.Version != "2.0.0" {
		t.Fatal("newer schema not readable")
	}
	if _, err := Parse([]byte(`{"releases":[]}`)); err == nil {
		t.Fatal("missing schema_version accepted")
	}
}

func TestEmbeddedQueries(t *testing.T) {
	r, ok := Latest()
	if !ok {
		t.Fatal("no embedded release notes")
	}
	if got, ok := ForVersion(r.Version); !ok || got.Version != r.Version {
		t.Fatal("ForVersion(latest) failed")
	}
	if len(Since(r.Version)) != 0 || len(Since("0.0.1")) == 0 {
		t.Fatal("Since on embedded notes wrong")
	}
}

func TestValidateSample(t *testing.T) {
	if errs := Validate([]byte(sample)); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestValidateCatchesProblems(t *testing.T) {
	rel := func(v, date, cat, text string) string {
		return `{"version":"` + v + `","date":"` + date + `","title":"t","summary":"s","changes":{"` + cat + `":[{"text":"` + text + `"}]}}`
	}
	doc := func(rs ...string) string {
		return `{"schema_version":1,"releases":[` + strings.Join(rs, ",") + `]}`
	}
	ok := rel("1.0.0", "2026-10-01", "added", "x")
	cases := map[string]struct{ doc, want string }{
		"unsorted":         {doc(ok, rel("1.1.0", "2026-10-01", "added", "x")), "newest first"},
		"duplicate":        {doc(ok, ok), "duplicate version"},
		"bad date":         {doc(rel("1.0.0", "2026-13-01", "added", "x")), "YYYY-MM-DD"},
		"bad date format":  {doc(rel("1.0.0", "10/01/2026", "added", "x")), "YYYY-MM-DD"},
		"unknown category": {doc(rel("1.0.0", "2026-10-01", "improved", "x")), `unknown category "improved"`},
		"empty text":       {doc(rel("1.0.0", "2026-10-01", "fixed", " ")), "text is empty"},
		"bad version":      {doc(rel("v1.0", "2026-10-01", "fixed", "x")), "semver"},
		"unknown field":    {`{"schema_version":1,"releases":[{"version":"1.0.0","date":"2026-10-01","title":"t","summary":"s","changes":{"added":[{"text":"x","colour":1}]}}]}`, "does not match the schema"},
		"wrong schema":     {`{"schema_version":9,"releases":[` + ok + `]}`, "schema_version is 9"},
		"no changes":       {`{"schema_version":1,"releases":[{"version":"1.0.0","date":"2026-10-01","title":"t","summary":"s","changes":{}}]}`, "changes is empty"},
		"bad command":      {`{"schema_version":1,"releases":[{"version":"1.0.0","date":"2026-10-01","title":"t","summary":"s","changes":{"added":[{"text":"x","commands":["add"]}]}}]}`, "must look like /name"},
		"overlap":          {doc(`{"version":"1.2.0","covers_from":"1.0.0","date":"2026-10-01","title":"t","summary":"s","changes":{"added":[{"text":"x"}]}}`, ok), "overlaps"},
	}
	for name, c := range cases {
		errs := Validate([]byte(c.doc))
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), c.want) {
				found = true
				if !strings.Contains(e.Error(), "go run ./cmd/releasenotes") {
					t.Errorf("%s: error lacks fix hint: %v", name, e)
				}
			}
		}
		if !found {
			t.Errorf("%s: want error containing %q, got %v", name, c.want, errs)
		}
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.9.10", "0.9.9", 1}, {"v1.0.0", "1.0.0", 0}, {"1.0.0-rc1", "1.0.0", -1}, {"0.9.202", "0.9.201", 1}, {"1.2.3", "1.10.0", -1},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestMarkdownRendersEntries(t *testing.T) {
	md := Markdown(mustParse(t, sample))
	for _, want := range []string{"## 1.2.1 – 1.3.0 — 2026-10-03", "### Added", "- **Breaking:** d", "## 1.0.0 — 2026-10-01"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}
