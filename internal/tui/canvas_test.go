package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderCanvasBodyReadsFileAndHighlights(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbravo\ncharlie\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{workspaceRoot: dir}
	out := a.renderCanvasBody("sample.txt", 2, 3)
	if !strings.Contains(out, "CANVAS") {
		t.Errorf("expected CANVAS header, got: %q", out)
	}
	for _, want := range []string{"alpha", "bravo", "charlie", "delta", "sample.txt:2-3"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got: %q", want, out)
		}
	}
}

func TestRenderCanvasBodyMissingFile(t *testing.T) {
	a := &App{workspaceRoot: t.TempDir()}
	out := a.renderCanvasBody("nope.txt", 0, 0)
	if !strings.Contains(out, "could not open") {
		t.Errorf("expected error message, got: %q", out)
	}
}

func TestParseLineRange(t *testing.T) {
	cases := []struct {
		in               string
		wantStart, wantE int
	}{
		{"12", 12, 0},
		{"12-40", 12, 40},
		{"12:40", 12, 40},
		{"  5 - 9 ", 5, 9},
		{"", 0, 0},
		{"abc", 0, 0},
	}
	for _, c := range cases {
		s, e := parseLineRange(c.in)
		if s != c.wantStart || e != c.wantE {
			t.Errorf("parseLineRange(%q) = (%d,%d), want (%d,%d)", c.in, s, e, c.wantStart, c.wantE)
		}
	}
}
