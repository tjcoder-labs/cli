package render

import (
	"strings"
	"testing"
)

// TestWrapCodePrefersCommaBreaks guards the formatter-style wrapping: a
// long call should split at argument commas rather than at the last
// space, so no row cuts through a quoted string.
func TestWrapCodePrefersCommaBreaks(t *testing.T) {
	src := `fmt.Sprintf("could not open %s", path, err)`
	lines := Code("", "", src, th)
	parts := WrapCode(lines[0], 34)
	if len(parts) < 2 {
		t.Fatalf("expected wrapping, got %d row(s)", len(parts))
	}
	joined := ""
	for _, p := range parts {
		joined += plain(p)
	}
	collapse := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if collapse(joined) != collapse(src) {
		t.Fatalf("wrapping lost characters:\n got %q\nwant %q", collapse(joined), collapse(src))
	}
	// A break must not land inside the quoted literal: the opening
	// quote and its closing partner belong to the same row.
	for i, p := range parts {
		if n := strings.Count(plain(p), `"`); n%2 != 0 && i < len(parts)-1 {
			t.Errorf("row %d splits a string literal: %q", i, plain(p))
		}
	}
}

// TestWrapCodeIndentsContinuation checks the Prettier-style hanging
// indent: continued rows line up under the original leading whitespace.
func TestWrapCodeIndentsContinuation(t *testing.T) {
	src := "\treturn a.palette.Theme.Err(\"could not open \" + path), 0"
	lines := Code("", "x.go", src, th)
	parts := WrapCode(lines[0], 40)
	if len(parts) < 2 {
		t.Fatalf("expected wrapping, got %d row(s)", len(parts))
	}
	lead := func(l Line) int {
		return len(plain(l)) - len(strings.TrimLeft(plain(l), " "))
	}
	if lead(parts[1]) <= 4 {
		t.Errorf("continuation not indented past the original indent: %q", plain(parts[1]))
	}
}

// plain flattens a Line to its text.
func plain(l Line) string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}
