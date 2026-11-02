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

// wrapCell is one rune with its display width and the index of the span
// it came from, so a run of cells can be rebuilt with the original style.
type wrapCell struct {
	r  rune
	w  int
	si int
}

// cellsOf flattens l into display cells.
func cellsOf(l Line) []wrapCell {
	var cells []wrapCell
	for si, s := range l {
		for _, r := range s.Text {
			cells = append(cells, wrapCell{r, uniseg.StringWidth(string(r)), si})
		}
	}
	return cells
}

// buildCells reassembles a run of cells into a Line, merging adjacent
// cells that share a style.
func buildCells(l Line, cs []wrapCell) Line {
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

// Wrap splits l into visual lines no wider than width cells, breaking
// after whitespace where possible and hard-breaking long words. A width
// <= 0 disables wrapping.
func Wrap(l Line, width int) []Line {
	if width <= 0 || l.Width() <= width {
		return []Line{l}
	}
	cells := cellsOf(l)
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
			lines = append(lines, buildCells(l, cells))
			break
		}
		if cut == 0 { // a single cell wider than width
			cut = 1
		} else if lastSpace > 0 {
			cut = lastSpace + 1
		}
		lines = append(lines, buildCells(l, cells[:cut]))
		cells = cells[cut:]
	}
	return lines
}

// codeBreakRank scores how good it is to break *after* the cell at index
// i. Lower is better; -1 means breaking there is not allowed.
//
// The ordering follows what a formatter would do: split at commas first
// (argument and field lists), then at binary operators, then after an
// opening bracket, then fall back to any whitespace. Breaking a string
// literal or an identifier is never allowed, so a wrapped line no longer
// cuts "could | not open" through the middle of a quoted value.
func codeBreakRank(cells []wrapCell, i int) int {
	r := cells[i].r
	var next rune
	if i+1 < len(cells) {
		next = cells[i+1].r
	}
	switch {
	case r == ',':
		return 0
	case r == ' ', r == '\t':
		return 3
	}
	if strings.ContainsRune("+-*/%&|=<>?:", r) && r != 0 {
		// Only treat these as breakable when they are surrounded by
		// spaces, i.e. a binary operator rather than a sign or part of
		// an identifier.
		if (i == 0 || cells[i-1].r == ' ') && (next == ' ' || next == 0) {
			return 1
		}
	}
	if r == '(' || r == '[' || r == '{' {
		return 2
	}
	if next == ')' || next == ']' || next == '}' || next == '.' {
		return 2
	}
	return -1
}

// WrapCode wraps a source line the way a formatter would: it prefers to
// break at commas and operators instead of at the last space, and it
// indents continuation rows under the original leading whitespace so a
// wrapped call still reads as one statement.
//
// Unlike Wrap it never cuts inside a quoted string or identifier unless
// there is no legal break point at all.
func WrapCode(l Line, width int) []Line {
	if width <= 0 || l.Width() <= width {
		return []Line{l}
	}
	cells := cellsOf(l)

	// Continuation indent: the line's own leading whitespace, plus a
	// small step, capped so it can never eat the whole pane.
	lead := 0
	for lead < len(cells) && cells[lead].r == ' ' {
		lead++
	}
	cont := lead + 2
	if max := width / 3; cont > max {
		cont = max
	}
	indent := make([]wrapCell, cont)
	for i := range indent {
		indent[i] = wrapCell{r: ' ', w: 1, si: 0}
	}

	var lines []Line
	first := true
	avail := width
	for len(cells) > 0 {
		if !first {
			avail = width - cont
			if avail < 8 {
				avail = 8
			}
		}
		w, cut := 0, 0
		for cut < len(cells) && w+cells[cut].w <= avail {
			w += cells[cut].w
			cut++
		}
		if cut == 0 { // one cell wider than the pane
			cut = 1
		}
		if cut < len(cells) {
			// Walk back to the best legal break point in this window.
			best, bestRank := -1, 1<<30
			for i := cut - 1; i >= 0; i-- {
				if rk := codeBreakRank(cells, i); rk >= 0 && rk < bestRank {
					best, bestRank = i+1, rk
					if rk == 0 {
						break
					}
				}
			}
			// Do not backtrack so far that the row is mostly empty.
			if best > 0 && best >= cut/3 {
				cut = best
			}
		}
		part := buildCells(l, cells[:cut])
		if !first && cont > 0 {
			part = append(buildCells(l, indent), part...)
		}
		lines = append(lines, part)
		cells = cells[cut:]
		first = false
	}
	return lines
}
