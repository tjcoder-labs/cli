// Package render turns markdown, structured data and source code into
// tview markup styled exclusively from the active theme palette.
//
// Everything that reaches the screen is built from Spans (text plus an
// explicit foreground, optional background and attribute set). Text is
// escaped when a span is emitted, so model output such as "[red]" or
// "[::b]" always renders literally and can never inject tview tags.
package render

import (
	"strings"

	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
)

// Span is one run of identically styled text.
type Span struct {
	Text string
	Fg   string // "#rrggbb"; "" = view default
	Bg   string // "#rrggbb"; "" = view default
	Attr string // tview attribute letters, e.g. "b", "iu"
}

// Line is one rendered (logical) line.
type Line []Span

// Markup returns the tview markup for the line. Every span carries its
// complete style and resets afterwards, so lines can be concatenated,
// split or prefixed without style bleeding between them.
func (l Line) Markup() string {
	var b strings.Builder
	for _, s := range l {
		if s.Text == "" {
			continue
		}
		plain := s.Fg == "" && s.Bg == "" && s.Attr == ""
		if plain {
			// Still bracket the text with a reset tag so that pieces of
			// a would-be tag split across spans can never re-assemble.
			b.WriteString("[-:-:-]")
			b.WriteString(Escape(s.Text))
			continue
		}
		fg, bg, attr := s.Fg, s.Bg, s.Attr
		if fg == "" {
			fg = "-"
		}
		if bg == "" {
			bg = "-"
		}
		if attr == "" {
			attr = "-"
		}
		b.WriteString("[" + fg + ":" + bg + ":" + attr + "]")
		b.WriteString(Escape(s.Text))
		b.WriteString("[-:-:-]")
		if strings.ContainsRune(s.Attr, 'u') {
			// tcell keeps underline outside the attribute mask, so the
			// "-" attribute reset does not clear it.
			b.WriteString("[::U]")
		}
	}
	return b.String()
}

// Plain returns the line's visible text.
func (l Line) Plain() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Width returns the display width of the line in terminal cells.
func (l Line) Width() int {
	w := 0
	for _, s := range l {
		w += uniseg.StringWidth(s.Text)
	}
	return w
}

// Escape escapes text so tview prints it verbatim. tview.Escape covers
// tags ending in "]"; a lone trailing "[" is harmless.
func Escape(s string) string { return tview.Escape(s) }

// Join renders lines as markup separated by newlines.
func Join(lines []Line) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = l.Markup()
	}
	return strings.Join(parts, "\n")
}

// WithBg returns a copy of l with every span's background set to bg.
func (l Line) WithBg(bg string) Line {
	out := make(Line, len(l))
	for i, s := range l {
		s.Bg = bg
		out[i] = s
	}
	return out
}

// Wrap splits l into visual lines no wider than width cells, breaking
// after whitespace where possible and hard-breaking long words. A width
// <= 0 disables wrapping.
func Wrap(l Line, width int) []Line {
	if width <= 0 || l.Width() <= width {
		return []Line{l}
	}
	type cell struct {
		r  rune
		w  int
		si int
	}
	var cells []cell
	for si, s := range l {
		for _, r := range s.Text {
			cells = append(cells, cell{r, uniseg.StringWidth(string(r)), si})
		}
	}
	build := func(cs []cell) Line {
		var out Line
		for _, c := range cs {
			st := l[c.si]
			if n := len(out); n > 0 && out[n-1].Fg == st.Fg && out[n-1].Bg == st.Bg && out[n-1].Attr == st.Attr {
				out[n-1].Text += string(c.r)
				continue
			}
			st.Text = string(c.r)
			out = append(out, st)
		}
		return out
	}
	var lines []Line
	for len(cells) > 0 {
		w, cut, lastSpace := 0, 0, -1
		for cut < len(cells) && w+cells[cut].w <= width {
			if cells[cut].r == ' ' || cells[cut].r == '\t' {
				lastSpace = cut
			}
			w += cells[cut].w
			cut++
		}
		if cut == len(cells) {
			lines = append(lines, build(cells))
			break
		}
		if cut == 0 { // a single cell wider than width
			cut = 1
		} else if lastSpace > 0 {
			cut = lastSpace + 1
		}
		lines = append(lines, build(cells[:cut]))
		cells = cells[cut:]
	}
	return lines
}
