package tui

import (
	"regexp"
	"strings"
	"testing"
)

var (
	// Matches a rendered border row (starts with ┌/├/└ after tag-stripping).
	borderLineRe = regexp.MustCompile(`^[┌├└]`)
	// Matches a rendered data row (starts/ends with │ after tag-stripping).
	dataRowRe = regexp.MustCompile(`^│.*│$`)
)

func stripTagsForTest(s string) string {
	return tviewTagRe.ReplaceAllString(s, "")
}

func TestRenderMarkdownTables_Basic(t *testing.T) {
	app := &App{}
	app.palette = darkPalette()
	in := "before\n" +
		"| Name  | Value |\n" +
		"| ----- | ----- |\n" +
		"| foo   | 1     |\n" +
		"| bar   | 2     |\n" +
		"after"
	out := app.renderMarkdownTables(in)
	lines := strings.Split(out, "\n")
	if len(lines) != 8 {
		t.Fatalf("expected 8 lines (1 before + 6 table + 1 after), got %d:\n%s", len(lines), out)
	}
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = stripTagsForTest(l)
	}
	if clean[0] != "before" {
		t.Errorf("line[0] should be untouched: %q", clean[0])
	}
	if clean[7] != "after" {
		t.Errorf("line[7] should be untouched: %q", clean[7])
	}
	if !borderLineRe.MatchString(clean[1]) || !strings.Contains(clean[1], "┌") {
		t.Errorf("line[1] should be top border: %q", clean[1])
	}
	if !dataRowRe.MatchString(clean[2]) {
		t.Errorf("line[2] should be header data row: %q", clean[2])
	}
	if !strings.Contains(clean[2], "Name") || !strings.Contains(clean[2], "Value") {
		t.Errorf("header contents missing: %q", clean[2])
	}
	if !borderLineRe.MatchString(clean[3]) || !strings.Contains(clean[3], "├") {
		t.Errorf("line[3] should be mid border: %q", clean[3])
	}
	if !dataRowRe.MatchString(clean[4]) || !strings.Contains(clean[4], "foo") {
		t.Errorf("line[4] should be first body row: %q", clean[4])
	}
	if !dataRowRe.MatchString(clean[5]) || !strings.Contains(clean[5], "bar") {
		t.Errorf("line[5] should be second body row: %q", clean[5])
	}
	if !borderLineRe.MatchString(clean[6]) || !strings.Contains(clean[6], "└") {
		t.Errorf("line[6] should be bottom border: %q", clean[6])
	}
}

func TestRenderMarkdownTables_UnevenColumns(t *testing.T) {
	app := &App{}
	app.palette = darkPalette()
	in := "| a | b |\n|---|---|\n| onelongvalue | x |"
	out := app.renderMarkdownTables(in)
	// Should handle the body row being wider than the header without panic.
	if !strings.Contains(out, "onelongvalue") {
		t.Errorf("cell content missing from output:\n%s", out)
	}
	// Column 1 width should accommodate "onelongvalue".
	clean := stripTagsForTest(out)
	if !strings.Contains(clean, "─────────────") {
		t.Errorf("expected wide column separator, got:\n%s", clean)
	}
}

func TestRenderMarkdownTables_NoSeparatorLine(t *testing.T) {
	app := &App{}
	app.palette = darkPalette()
	in := "| a | b |\n| not a separator |"
	out := app.renderMarkdownTables(in)
	// Should pass through unchanged.
	if stripTagsForTest(out) != in {
		t.Errorf("expected table-less input to pass through:\n%s", out)
	}
}

func TestRenderMarkdownTables_MultipleTables(t *testing.T) {
	app := &App{}
	app.palette = darkPalette()
	in := "| x |\n|---|\n| 1 |\n" +
		"middle text\n" +
		"| y | z |\n|---|---|\n| 2 | 3 |\n"
	out := app.renderMarkdownTables(in)
	clean := stripTagsForTest(out)
	if !strings.Contains(clean, "middle text") {
		t.Errorf("non-table line lost:\n%s", clean)
	}
	nBorders := strings.Count(clean, "┌")
	if nBorders != 2 {
		t.Errorf("expected 2 top borders, got %d\n%s", nBorders, clean)
	}
}

func TestIsTableRow(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"| a | b |", true},
		{"| a |", true},
		{"|a|b|c|", true},
		{"not a row", false},
		{"| only pipe", false},
		{"|---|---|", false}, // separators are not data rows
		{"|:-:|-|", false},
	}
	for _, c := range cases {
		if got := isTableRow(c.in); got != c.want {
			t.Errorf("isTableRow(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsTableSeparator(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"|---|---|", true},
		{"| --- | :---: | ---: |", true},
		{"|-|", true},
		{"| a | b |", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isTableSeparator(c.in); got != c.want {
			t.Errorf("isTableSeparator(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRuneDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 5},
		{"世界", 4}, // CJK wide
		{"e\u0301", 1}, // combining accent — zero-width
		{"a\u200Bb", 2}, // zero-width space — skipped
	}
	for _, c := range cases {
		if got := runeDisplayWidth(c.in); got != c.want {
			t.Errorf("runeDisplayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
