package render

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/tjcoder-labs/cli/internal/theme"
)

var update = flag.Bool("update", false, "rewrite golden files")

// screen draws markup into a simulated terminal and returns the visible
// text plus a per-cell style dump (one entry per non-blank cell).
func screen(markup string, w, h int) (text string, styles []string) {
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		panic(err)
	}
	defer s.Fini()
	s.SetSize(w, h)
	tv := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	tv.SetText(markup)
	tv.SetRect(0, 0, w, h)
	tv.Draw(s)
	s.Show()
	cells, cw, ch := s.GetContents()
	var b strings.Builder
	for y := 0; y < ch; y++ {
		var row strings.Builder
		for x := 0; x < cw; x++ {
			c := cells[y*cw+x]
			r := " "
			if len(c.Runes) > 0 {
				r = string(c.Runes)
			}
			row.WriteString(r)
			if r != " " {
				fg, bg, attr := c.Style.Decompose()
				if c.Style.GetUnderlineStyle() != tcell.UnderlineStyleNone {
					attr |= tcell.AttrUnderline
				}
				if bg == tview.Styles.PrimitiveBackgroundColor {
					bg = tcell.ColorDefault
				}
				styles = append(styles, fmt.Sprintf("%d,%d %s fg=%s bg=%s a=%d", x, y, r, hexOf(fg), hexOf(bg), attr))
			}
		}
		b.WriteString(strings.TrimRight(row.String(), " "))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n"), styles
}

func hexOf(c tcell.Color) string {
	if c == tcell.ColorDefault {
		return "default"
	}
	return fmt.Sprintf("#%06X", c.Hex())
}

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := "testdata/" + name
	if *update {
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if strings.TrimRight(string(want), "\n") != got {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

var th = theme.Default()

func TestMarkdownGolden(t *testing.T) {
	txt, _ := screen(Markdown(read(t, "sample.md"), th, Options{Width: 70}), 72, 200)
	golden(t, "sample.md.golden", txt)
}

func TestCanvasGoldens(t *testing.T) {
	for _, f := range []string{"sample.md", "sample.json", "sample.xml"} {
		m, _ := Gutter(Source(f, read(t, f), th), 30, 33, 60, th)
		txt, _ := screen(m, 60, 200)
		golden(t, f+".canvas.golden", txt)
	}
}

func TestTagInjectionIsLiteral(t *testing.T) {
	src := "Tags [red]red[-] [::b]bold[::-] [#00ff00]green[-] [\"region\"] [:blue] and `[yellow]` **[green]x**"
	got, styles := screen(Markdown(src, th, Options{}), 200, 3)
	want := "Tags [red]red[-] [::b]bold[::-] [#00ff00]green[-] [\"region\"] [:blue] and [yellow] [green]x"
	if got != want {
		t.Fatalf("injection not escaped:\n got %q\nwant %q", got, want)
	}
	for _, s := range styles {
		if !onScheme(s) {
			t.Errorf("off-scheme cell %s", s)
		}
	}
	// Code highlighting must escape token text too.
	for _, lang := range []string{"go", "json", "xml", "bash", ""} {
		got, _ := screen(Join(Code(lang, "", `x := "[red]y[-]" // [::b]`, th)), 80, 2)
		if !strings.Contains(got, "[red]y[-]") || !strings.Contains(got, "[::b]") {
			t.Errorf("%s: code tags interpreted: %q", lang, got)
		}
	}
}

var (
	hexRe    = regexp.MustCompile(`#[0-9A-Fa-f]{6}`)
	tagHexRe = regexp.MustCompile(`\[[^\[\]]*?(#[0-9A-Fa-f]{6})[:\]]`)
)

func onScheme(cell string) bool {
	allowed := map[string]bool{"default": true}
	for _, h := range th.All() {
		allowed[strings.ToUpper(h)] = true
	}
	for _, h := range hexRe.FindAllString(cell, -1) {
		if !allowed[strings.ToUpper(h)] {
			return false
		}
	}
	return true
}

func TestOnlyPaletteColours(t *testing.T) {
	var outs []string
	outs = append(outs, Markdown(read(t, "sample.md"), th, Options{Width: 70}))
	for _, f := range []string{"sample.md", "sample.json", "sample.xml", "x.go", "x.py", "x.rs", "x.sql", "x.sh", "x.ts", "x.yaml", "x.diff"} {
		body := "package x\n+added\n-removed\n// c\nx = \"s\" + 1\n"
		if strings.HasPrefix(f, "sample") {
			body = read(t, f)
		}
		m, _ := Gutter(Source(f, body, th), 2, 3, 50, th)
		outs = append(outs, m)
	}
	for _, o := range outs {
		_, styles := screen(o, 80, 300)
		for _, s := range styles {
			if !onScheme(s) {
				t.Fatalf("off-scheme cell: %s", s)
			}
		}
		for _, m := range tagHexRe.FindAllStringSubmatch(o, -1) {
			if !onScheme(m[1]) {
				t.Fatalf("off-scheme colour tag in markup: %s", m[0])
			}
		}
	}
}

func TestHeadingStyles(t *testing.T) {
	txt, styles := screen(Markdown("# One\n## Two\n###### Six ###", th, Options{}), 40, 4)
	if txt != "One\nTwo\nSix" {
		t.Fatalf("heading markers not stripped: %q", txt)
	}
	want := []string{
		fmt.Sprintf("0,0 O fg=%s bg=default a=%d", th.Bright, tcell.AttrBold|tcell.AttrUnderline),
		fmt.Sprintf("0,1 T fg=%s bg=default a=%d", th.Bright, tcell.AttrBold),
		fmt.Sprintf("0,2 S fg=%s bg=default a=%d", th.Dim, tcell.AttrItalic),
	}
	for _, w := range want {
		if !contains(styles, w) {
			t.Errorf("missing %q in %v", w, styles[:6])
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if strings.EqualFold(v, x) {
			return true
		}
	}
	return false
}

func TestInlineStyles(t *testing.T) {
	_, styles := screen(Markdown("a **b** *c* ~~d~~ `e` [f](u)", th, Options{}), 60, 2)
	check := func(r string, fg string, attr tcell.AttrMask) {
		t.Helper()
		for _, s := range styles {
			if strings.Contains(s, " "+r+" ") {
				w := fmt.Sprintf("fg=%s", fg)
				if !strings.Contains(strings.ToUpper(s), strings.ToUpper(w)) || !strings.HasSuffix(s, fmt.Sprintf("a=%d", attr)) {
					t.Errorf("%s: got %s want %s a=%d", r, s, w, attr)
				}
				return
			}
		}
		t.Errorf("rune %s not drawn", r)
	}
	check("a", th.Text, 0)
	check("b", th.Text, tcell.AttrBold)
	check("c", th.Text, tcell.AttrItalic)
	check("d", th.Text, tcell.AttrStrikeThrough)
	check("e", th.Soft, 0)
	check("f", th.Bright, tcell.AttrUnderline)
}

func TestUnmatchedDelimitersStayLiteral(t *testing.T) {
	got, _ := screen(Markdown("2 * 3 * 4 and *args and snake_case_name and **open", th, Options{}), 80, 2)
	if got != "2 * 3 * 4 and *args and snake_case_name and **open" {
		t.Fatalf("got %q", got)
	}
}

// Streaming in arbitrary chunk sizes must produce exactly what a
// whole-document render shows (same glyphs and styles in every cell).
func TestStreamMatchesWhole(t *testing.T) {
	src := read(t, "sample.md") + "\n| a | b |\n|---|---|\n| 1 | 2 |\nafter **table** [link](x)\n- [x] done"
	wantTxt, wantSt := screen(Markdown(src, th, Options{Width: 70}), 72, 200)
	rng := rand.New(rand.NewSource(1))
	sizes := []int{1, 2, 3, 5, 8, 13, 0}
	for _, n := range sizes {
		s := NewStream(th, Options{Width: 70})
		var b strings.Builder
		rest := src
		for rest != "" {
			k := n
			if k == 0 {
				k = 1 + rng.Intn(20)
			}
			k = min(k, len(rest))
			b.WriteString(s.Write(rest[:k]))
			rest = rest[k:]
		}
		b.WriteString(s.Flush())
		gotTxt, gotSt := screen(b.String(), 72, 200)
		if gotTxt != wantTxt {
			t.Fatalf("chunk=%d text differs\n--- got ---\n%s\n--- want ---\n%s", n, gotTxt, wantTxt)
		}
		if strings.Join(gotSt, "\n") != strings.Join(wantSt, "\n") {
			for i := range wantSt {
				if i >= len(gotSt) || gotSt[i] != wantSt[i] {
					g := ""
					if i < len(gotSt) {
						g = gotSt[i]
					}
					t.Fatalf("chunk=%d style differs at %d: got %s want %s", n, i, g, wantSt[i])
				}
			}
		}
	}
}

func TestStreamEmitsProgressively(t *testing.T) {
	s := NewStream(th, Options{})
	out := s.Write("Hello there **wor")
	if txt, _ := screen(out, 40, 1); txt != "Hello there" {
		t.Fatalf("partial paragraph: %q", txt)
	}
	out += s.Write("ld** done")
	out += s.Flush()
	if txt, _ := screen(out, 40, 1); txt != "Hello there world done" {
		t.Fatalf("final: %q", txt)
	}
}

func TestCodeLanguagesHighlighted(t *testing.T) {
	samples := map[string]string{
		"go":         "func main() { x := \"s\"; return 1 } // c",
		"typescript": "const x: number = 1; // c",
		"javascript": "function f() { return 'x' } // c",
		"python":     "def f(x):\n    return 'x'  # c",
		"bash":       "for f in *; do echo \"$f\"; done # c",
		"sql":        "SELECT id FROM t WHERE x = 'y'; -- c",
		"rust":       "fn main() { let x: i32 = 5; } // c",
		"json":       `{"a": [1, true, null, "s"]}`,
		"xml":        `<a b="c"><!-- c --></a>`,
		"html":       `<div class="x">hi</div>`,
		"yaml":       "a: 1\nb: [x, 'y'] # c",
		"diff":       "--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new",
	}
	for lang, src := range samples {
		if !HasLexer(lang) {
			t.Errorf("%s: no lexer", lang)
			continue
		}
		fgs := map[string]bool{}
		for _, l := range Code(lang, "", src, th) {
			for _, sp := range l {
				fgs[sp.Fg] = true
			}
		}
		if len(fgs) < 3 {
			t.Errorf("%s: only %d distinct colours: %v", lang, len(fgs), fgs)
		}
	}
	for _, alias := range []string{"ts", "js", "sh", "shell", "py", "rs", "yml", "golang", "jsonc", "c++"} {
		if !HasLexer(alias) {
			t.Errorf("alias %s not recognised", alias)
		}
	}
}

func TestDiffUsesShadeAndAttrs(t *testing.T) {
	ls := Code("diff", "", "+new\n-old", th)
	if ls[0][0].Fg != th.Bright || !strings.Contains(ls[0][0].Attr, "b") {
		t.Errorf("insert: %+v", ls[0])
	}
	if ls[1][0].Fg != th.Dim || !strings.Contains(ls[1][0].Attr, "s") {
		t.Errorf("delete: %+v", ls[1])
	}
}

func TestGutterRangeAndWrap(t *testing.T) {
	lines := Code("", "", "one\ntwo two two two two two two two\nthree\nfour", th)
	m, row := Gutter(lines, 3, 4, 20, th)
	txt, styles := screen(m, 20, 10)
	want := "1 │ one\n2 │ two two two two\n  ┆ two two two two\n3 ▌ three\n4 ▌ four"
	if txt != want {
		t.Fatalf("gutter:\n%s\nwant:\n%s", txt, want)
	}
	if row != 3 {
		t.Errorf("start row = %d, want 3 (after the wrapped line)", row)
	}
	if !contains(styles, fmt.Sprintf("4,3 t fg=%s bg=%s a=0", th.Text, th.BgSelect)) {
		t.Errorf("range line not tinted: %v", styles)
	}
}

func TestWrapBreaksAtSpaces(t *testing.T) {
	l := Line{{Text: "alpha beta ", Fg: th.Text}, {Text: "gamma", Fg: th.Bright, Attr: "b"}, {Text: " supercalifragilistic"}}
	var got []string
	for _, p := range Wrap(l, 12) {
		got = append(got, p.Plain())
	}
	want := []string{"alpha beta ", "gamma ", "supercalifra", "gilistic"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTableFitsWidth(t *testing.T) {
	src := "| k | description |\n|---|---|\n| a | " + strings.Repeat("word ", 20) + "|"
	txt, _ := screen(Markdown(src, th, Options{Width: 40}), 60, 20)
	for _, l := range strings.Split(txt, "\n") {
		if w := len([]rune(l)); w > 40 {
			t.Errorf("table row %d wide: %q", w, l)
		}
	}
	if !strings.Contains(txt, "│ a │ word") {
		t.Errorf("table body missing: %s", txt)
	}
}
