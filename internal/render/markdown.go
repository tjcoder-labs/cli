package render

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"

	"github.com/tjcoder-labs/cli/internal/theme"
)

type blockKind int

const (
	bkPara blockKind = iota
	bkBlank
	bkHeading
	bkFence
	bkHR
	bkQuote
	bkUList
	bkOList
	bkTable
)

type block struct {
	kind    blockKind
	level   int    // heading level, quote depth, list nesting
	indent  string // leading whitespace
	marker  string // raw marker text ("##", "-", "12.", ">", "```")
	task    int    // 0 none, 1 open, 2 done
	lang    string // fence info string
	content int    // byte offset where inline content starts
}

// classify inspects the start of a line. ok=false means more input is
// needed before the line kind can be decided (only when !complete).
func classify(line string, complete bool) (block, bool) {
	trim := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trim)]
	off := len(indent)
	need := func() (block, bool) {
		if complete {
			return block{kind: bkPara, indent: indent, content: off}, true
		}
		return block{}, false
	}
	if trim == "" {
		if complete {
			return block{kind: bkBlank}, true
		}
		return block{}, false
	}
	switch c := trim[0]; {
	case c == '`' || c == '~':
		if len(trim) < 3 {
			if strings.Count(trim, string(c)) == len(trim) {
				return need()
			}
			return block{kind: bkPara, indent: indent, content: off}, true
		}
		if strings.HasPrefix(trim, strings.Repeat(string(c), 3)) {
			if !complete {
				return block{}, false
			}
			k := runLen(trim, 0, c)
			info := strings.TrimSpace(trim[k:])
			if c == '`' && strings.Contains(info, "`") {
				break
			}
			return block{kind: bkFence, indent: indent, marker: trim[:k], lang: info}, true
		}
	case c == '#':
		k := runLen(trim, 0, '#')
		if k > 6 {
			break
		}
		if k == len(trim) {
			if complete {
				return block{kind: bkHeading, level: k, indent: indent, marker: trim[:k], content: len(line)}, true
			}
			return block{}, false
		}
		if trim[k] == ' ' || trim[k] == '\t' {
			return block{kind: bkHeading, level: k, indent: indent, marker: trim[:k], content: off + k + 1}, true
		}
	case c == '|':
		if !complete {
			return block{}, false
		}
		return block{kind: bkTable, indent: indent}, true
	case c == '>':
		depth, j := 0, 0
		for j < len(trim) && (trim[j] == '>' || trim[j] == ' ') {
			if trim[j] == '>' {
				depth++
			}
			j++
		}
		if j == len(trim) && !complete {
			return block{}, false
		}
		return block{kind: bkQuote, level: depth, indent: indent, marker: trim[:j], content: off + j}, true
	case c == '-' || c == '*' || c == '_' || c == '+':
		if isHR(trim) {
			if !complete {
				return block{}, false
			}
			return block{kind: bkHR, indent: indent, marker: trim}, true
		}
		if strings.Trim(trim, string(c)+" ") == "" && !complete {
			return block{}, false // could still become a rule
		}
		if c != '_' && len(trim) >= 2 && trim[1] == ' ' {
			b := block{kind: bkUList, level: listLevel(indent), indent: indent, marker: trim[:1], content: off + 2}
			rest := trim[2:]
			if !complete && len(rest) < 4 && (strings.HasPrefix("[ ] ", rest) || strings.HasPrefix("[x] ", strings.ToLower(rest))) {
				return block{}, false
			}
			switch {
			case strings.HasPrefix(rest, "[ ] ") || rest == "[ ]":
				b.task, b.content = 1, b.content+min(4, len(rest))
			case strings.HasPrefix(strings.ToLower(rest), "[x] ") || strings.ToLower(rest) == "[x]":
				b.task, b.content = 2, b.content+min(4, len(rest))
			}
			return b, true
		}
		if len(trim) == 1 {
			return need()
		}
	case c >= '0' && c <= '9':
		k := 0
		for k < len(trim) && k < 9 && trim[k] >= '0' && trim[k] <= '9' {
			k++
		}
		if k == len(trim) {
			return need()
		}
		if trim[k] == '.' || trim[k] == ')' {
			if k+1 == len(trim) {
				if complete {
					return block{kind: bkOList, level: listLevel(indent), indent: indent, marker: trim[:k+1], content: len(line)}, true
				}
				return block{}, false
			}
			if trim[k+1] == ' ' {
				return block{kind: bkOList, level: listLevel(indent), indent: indent, marker: trim[:k+1], content: off + k + 2}, true
			}
		}
	}
	return block{kind: bkPara, indent: indent, content: off}, true
}

func listLevel(indent string) int {
	n := 0
	for _, r := range indent {
		if r == '\t' {
			n += 4
		} else {
			n++
		}
	}
	return n / 2
}

func isHR(t string) bool {
	t = strings.TrimSpace(t)
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	n := 0
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case c:
			n++
		case ' ', '\t':
		default:
			return false
		}
	}
	return n >= 3
}

func isTableSep(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.Contains(t, "-") {
		return false
	}
	for _, r := range t {
		if !strings.ContainsRune("|:- \t", r) {
			return false
		}
	}
	return strings.Contains(t, "|")
}

func splitRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	if strings.HasSuffix(t, "|") && !strings.HasSuffix(t, "\\|") {
		t = t[:len(t)-1]
	}
	var cells []string
	var cur strings.Builder
	inCode := false
	for i := 0; i < len(t); i++ {
		switch {
		case t[i] == '\\' && i+1 < len(t) && t[i+1] == '|':
			cur.WriteByte('|')
			i++
		case t[i] == '`':
			inCode = !inCode
			cur.WriteByte('`')
		case t[i] == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(t[i])
		}
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

var bullets = []string{"•", "◦", "▪"}

func headingStyle(level int, th theme.Theme) (string, string) {
	switch level {
	case 1:
		return th.Bright, "bu"
	case 2:
		return th.Bright, "b"
	case 3:
		return th.Primary, "b"
	case 4:
		return th.Primary, "bi"
	case 5:
		return th.Dim, "b"
	default:
		return th.Dim, "i"
	}
}

// prefix returns the decoration spans for a rendered (non-source) block
// and the base style of its inline content.
func (b block) prefix(th theme.Theme) (Line, string, string) {
	switch b.kind {
	case bkHeading:
		fg, a := headingStyle(b.level, th)
		return nil, fg, a
	case bkQuote:
		bar := strings.Repeat("▎ ", max(1, b.level))
		return Line{{Text: b.indent + bar, Fg: th.Primary}}, th.Soft, "i"
	case bkUList:
		pad := strings.Repeat("  ", b.level)
		switch b.task {
		case 1:
			return Line{{Text: pad + "☐ ", Fg: th.Primary}}, th.Text, ""
		case 2:
			return Line{{Text: pad + "☑ ", Fg: th.Bright, Attr: "b"}}, th.Dim, ""
		}
		return Line{{Text: pad + bullets[b.level%len(bullets)] + " ", Fg: th.Primary}}, th.Text, ""
	case bkOList:
		pad := strings.Repeat("  ", b.level)
		return Line{{Text: pad + b.marker, Fg: th.Primary, Attr: "b"}, {Text: " "}}, th.Text, ""
	}
	if b.indent != "" {
		return Line{{Text: b.indent}}, th.Text, ""
	}
	return nil, th.Text, ""
}

// Options control rendering.
type Options struct {
	Width int // available cells for rules and tables; 0 = unknown
}

// Markdown renders a complete markdown document to tview markup.
func Markdown(src string, th theme.Theme, opt Options) string {
	s := NewStream(th, opt)
	return s.Write(src) + s.Flush()
}

// Stream renders markdown incrementally as it arrives in arbitrary
// chunks (e.g. model token deltas). Concatenating every Write result
// plus Flush equals Markdown(whole text).
type Stream struct {
	th  theme.Theme
	opt Options

	buf     string // unemitted text of the current line
	started bool   // part of the current line has been emitted
	cur     block
	in      inline

	fence      *block
	fenceLines []string
	table      []string
}

// NewStream returns a streaming renderer.
func NewStream(th theme.Theme, opt Options) *Stream {
	return &Stream{th: th, opt: opt}
}

// SetWidth updates the width used for rules and tables.
func (s *Stream) SetWidth(w int) { s.opt.Width = w }

// Reset discards all pending state (e.g. at the start of a new turn).
func (s *Stream) Reset() {
	*s = Stream{th: s.th, opt: s.opt}
}

// Write consumes a chunk and returns markup that is ready to display.
func (s *Stream) Write(chunk string) string {
	chunk = strings.ReplaceAll(chunk, "\r", "")
	s.buf += chunk
	var out strings.Builder
	for {
		i := strings.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := s.buf[:i]
		s.buf = s.buf[i+1:]
		s.finishLine(&out, line, true)
	}
	s.partial(&out)
	return out.String()
}

// Flush renders anything still pending (an unterminated last line, an
// open table or fence) without adding a trailing newline.
func (s *Stream) Flush() string {
	var out strings.Builder
	if s.started || s.buf != "" {
		line := s.buf
		s.buf = ""
		s.finishLine(&out, line, false)
	}
	s.flushTable(&out, false)
	s.fence, s.fenceLines = nil, nil
	return out.String()
}

func (s *Stream) newInline(fg, attr string) inline {
	return inline{th: s.th, fg: fg, attr: attr, codeBg: s.th.BgInput}
}

func (s *Stream) partial(out *strings.Builder) {
	if s.fence != nil || s.buf == "" {
		return
	}
	if !s.started {
		b, ok := classify(s.buf, false)
		if !ok || b.kind == bkTable || b.kind == bkFence || b.kind == bkHR || b.kind == bkBlank {
			return
		}
		if len(s.table) > 0 {
			s.flushTable(out, true)
		}
		pre, fg, attr := b.prefix(s.th)
		out.WriteString(pre.Markup())
		s.cur = b
		s.in = s.newInline(fg, attr)
		s.buf = s.buf[b.content:]
		s.started = true
	}
	cut := strings.LastIndexAny(s.buf, " \t")
	if cut < 0 {
		return
	}
	if s.cur.kind == bkHeading {
		// Hold a possible closing "###" sequence.
		if strings.TrimRight(s.buf[cut+1:], "#") == "" && strings.HasSuffix(strings.TrimRight(s.buf, " \t"), "#") {
			return
		}
	}
	spans, n := s.in.parse(s.buf[:cut+1], false)
	out.WriteString(Line(spans).Markup())
	s.buf = s.buf[n:]
}

func (s *Stream) finishLine(out *strings.Builder, line string, nl bool) {
	end := ""
	if nl {
		end = "\n"
	}
	if s.started {
		text := line
		if s.cur.kind == bkHeading {
			text = trimClosingHashes(text)
		}
		spans, _ := s.in.parse(text, true)
		out.WriteString(Line(spans).Markup() + end)
		s.started = false
		return
	}
	if s.fence != nil {
		b, _ := classify(line, true)
		if b.kind == bkFence && b.lang == "" && strings.HasPrefix(b.marker, s.fence.marker[:1]) && len(b.marker) >= len(s.fence.marker) {
			out.WriteString(Line{{Text: s.fence.indent + b.marker, Fg: s.th.Faint}}.Markup() + end)
			s.fence, s.fenceLines = nil, nil
			return
		}
		s.fenceLines = append(s.fenceLines, line)
		from := 0
		if len(s.fenceLines) > 400 {
			from = len(s.fenceLines) - 200
		}
		hl := Code(s.fence.lang, "", strings.Join(s.fenceLines[from:], "\n"), s.th)
		out.WriteString(hl[len(hl)-1].Markup() + end)
		return
	}
	b, _ := classify(line, true)
	if b.kind == bkTable {
		s.table = append(s.table, line)
		if !nl {
			s.flushTable(out, false)
		}
		return
	}
	if len(s.table) > 0 {
		s.flushTable(out, true)
	}
	switch b.kind {
	case bkBlank:
		out.WriteString(end)
	case bkFence:
		f := b
		s.fence, s.fenceLines = &f, nil
		ln := Line{{Text: b.indent + b.marker, Fg: s.th.Faint}}
		if b.lang != "" {
			ln = append(ln, Span{Text: b.lang, Fg: s.th.Dim, Attr: "i"})
		}
		out.WriteString(ln.Markup() + end)
	case bkHR:
		w := s.opt.Width
		if w <= 0 || w > 80 {
			w = min(max(w, 40), 80)
		}
		out.WriteString(Line{{Text: strings.Repeat("─", w), Fg: s.th.Faint}}.Markup() + end)
	default:
		pre, fg, attr := b.prefix(s.th)
		in := s.newInline(fg, attr)
		text := line[b.content:]
		if b.kind == bkHeading {
			text = trimClosingHashes(text)
		}
		spans, _ := in.parse(text, true)
		out.WriteString(append(pre, spans...).Markup() + end)
	}
}

func trimClosingHashes(s string) string {
	t := strings.TrimRight(s, " \t")
	u := strings.TrimRight(t, "#")
	if u != t && (u == "" || strings.HasSuffix(u, " ")) {
		return strings.TrimRight(u, " ")
	}
	return s
}

// flushTable renders buffered table rows. A run of pipe rows without a
// separator second row is rendered as plain paragraphs.
func (s *Stream) flushTable(out *strings.Builder, trailingNL bool) {
	rows := s.table
	s.table = nil
	if len(rows) == 0 {
		return
	}
	var lines []Line
	if len(rows) >= 2 && isTableSep(rows[1]) {
		lines = s.renderTable(rows)
	} else {
		for _, r := range rows {
			in := s.newInline(s.th.Text, "")
			spans, _ := in.parse(r, true)
			lines = append(lines, spans)
		}
	}
	out.WriteString(Join(lines))
	if trailingNL {
		out.WriteString("\n")
	}
}

type align int

const (
	alLeft align = iota
	alCenter
	alRight
)

func (s *Stream) renderTable(rows []string) []Line {
	th := s.th
	seps := splitRow(rows[1])
	ncol := len(seps)
	aligns := make([]align, ncol)
	for i, c := range seps {
		c = strings.TrimSpace(c)
		switch {
		case strings.HasPrefix(c, ":") && strings.HasSuffix(c, ":"):
			aligns[i] = alCenter
		case strings.HasSuffix(c, ":"):
			aligns[i] = alRight
		}
	}
	var body [][]Line
	for ri, r := range rows {
		if ri == 1 {
			continue
		}
		cells := splitRow(r)
		row := make([]Line, ncol)
		for ci := 0; ci < ncol; ci++ {
			txt := ""
			if ci < len(cells) {
				txt = cells[ci]
			}
			fg, attr := th.Text, ""
			if ri == 0 {
				fg, attr = th.Bright, "b"
			}
			in := s.newInline(fg, attr)
			sp, _ := in.parse(txt, true)
			row[ci] = sp
		}
		body = append(body, row)
	}
	widths := make([]int, ncol)
	for _, row := range body {
		for ci, c := range row {
			widths[ci] = max(widths[ci], c.Width(), 1)
		}
	}
	// Shrink the widest columns until the table fits.
	if s.opt.Width > 0 {
		avail := s.opt.Width - (3*ncol + 1)
		for sum(widths) > avail {
			wi := 0
			for i := range widths {
				if widths[i] > widths[wi] {
					wi = i
				}
			}
			if widths[wi] <= 6 {
				break
			}
			widths[wi]--
		}
	}
	border := func(l, m, r string) Line {
		var b strings.Builder
		b.WriteString(l)
		for i, w := range widths {
			b.WriteString(strings.Repeat("─", w+2))
			if i < ncol-1 {
				b.WriteString(m)
			}
		}
		b.WriteString(r)
		return Line{{Text: b.String(), Fg: th.Faint}}
	}
	out := []Line{border("┌", "┬", "┐")}
	for ri, row := range body {
		wrapped := make([][]Line, ncol)
		h := 1
		for ci, c := range row {
			wrapped[ci] = Wrap(c, widths[ci])
			h = max(h, len(wrapped[ci]))
		}
		for k := 0; k < h; k++ {
			ln := Line{{Text: "│", Fg: th.Faint}}
			for ci := range row {
				var cell Line
				if k < len(wrapped[ci]) {
					cell = trimLine(wrapped[ci][k])
				}
				padTotal := widths[ci] - cell.Width()
				lp := 0
				switch aligns[ci] {
				case alRight:
					lp = padTotal
				case alCenter:
					lp = padTotal / 2
				}
				ln = append(ln, Span{Text: " " + strings.Repeat(" ", max(lp, 0))})
				ln = append(ln, cell...)
				ln = append(ln, Span{Text: strings.Repeat(" ", max(padTotal-lp, 0)) + " "}, Span{Text: "│", Fg: th.Faint})
			}
			out = append(out, ln)
		}
		if ri == 0 {
			out = append(out, border("├", "┼", "┤"))
		}
	}
	out = append(out, border("└", "┴", "┘"))
	return out
}

func trimLine(l Line) Line {
	out := append(Line(nil), l...)
	for len(out) > 0 {
		last := &out[len(out)-1]
		last.Text = strings.TrimRightFunc(last.Text, unicode.IsSpace)
		if last.Text != "" {
			break
		}
		out = out[:len(out)-1]
	}
	return out
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

// SourceMarkdown styles a markdown file line-for-line for the canvas:
// every source line maps to exactly one output line and all markers stay
// visible (faint), so line numbers and ranges keep their meaning.
func SourceMarkdown(src string, th theme.Theme) []Line {
	raw := strings.Split(strings.ReplaceAll(strings.TrimSuffix(src, "\n"), "\r", ""), "\n")
	out := make([]Line, 0, len(raw))
	var fence *block
	var fenceBody []string
	fenceStart := 0
	mk := func(fg, attr string) inline {
		return inline{th: th, fg: fg, attr: attr, showMarks: true}
	}
	for idx, line := range raw {
		line = strings.ReplaceAll(line, "\t", "    ")
		if fence != nil {
			b, _ := classify(line, true)
			if b.kind == bkFence && b.lang == "" && strings.HasPrefix(b.marker, fence.marker[:1]) && len(b.marker) >= len(fence.marker) {
				hl := Code(fence.lang, "", strings.Join(fenceBody, "\n"), th)
				for k, l := range hl {
					if k < len(fenceBody) {
						out[fenceStart+1+k] = l
					}
				}
				out = append(out, Line{{Text: line, Fg: th.Faint}})
				fence = nil
				continue
			}
			fenceBody = append(fenceBody, line)
			out = append(out, Line{{Text: line, Fg: th.Text}})
			continue
		}
		b, _ := classify(line, true)
		switch b.kind {
		case bkBlank:
			out = append(out, Line{})
		case bkFence:
			f := b
			fence, fenceBody, fenceStart = &f, nil, idx
			ln := Line{{Text: b.indent + b.marker, Fg: th.Faint}}
			if b.lang != "" {
				ln = append(ln, Span{Text: line[len(b.indent)+len(b.marker):], Fg: th.Dim, Attr: "i"})
			}
			out = append(out, ln)
		case bkHR:
			out = append(out, Line{{Text: line, Fg: th.Faint}})
		case bkTable:
			if isTableSep(line) {
				out = append(out, Line{{Text: line, Fg: th.Faint}})
				continue
			}
			header := idx+1 < len(raw) && isTableSep(raw[idx+1])
			fg, attr := th.Text, ""
			if header {
				fg, attr = th.Bright, "b"
			}
			ln := Line{{Text: b.indent}}
			cells := strings.Split(strings.TrimSpace(line), "|")
			for ci, c := range cells {
				if ci > 0 {
					ln = append(ln, Span{Text: "|", Fg: th.Faint})
				}
				in := mk(fg, attr)
				sp, _ := in.parse(c, true)
				ln = append(ln, sp...)
			}
			out = append(out, ln)
		default:
			ln := Line{{Text: b.indent}}
			fg, attr := th.Text, ""
			switch b.kind {
			case bkHeading:
				fg, attr = headingStyle(b.level, th)
				ln = append(ln, Span{Text: line[len(b.indent):b.content], Fg: th.Faint})
			case bkQuote:
				fg, attr = th.Soft, "i"
				ln = append(ln, Span{Text: line[len(b.indent):b.content], Fg: th.Primary})
			case bkUList, bkOList:
				ln = append(ln, Span{Text: line[len(b.indent):b.content], Fg: th.Primary, Attr: "b"})
				if b.task == 2 {
					fg = th.Dim
				}
			}
			in := mk(fg, attr)
			sp, _ := in.parse(line[b.content:], true)
			out = append(out, append(ln, sp...))
		}
	}
	if fence != nil { // unterminated fence: highlight what we have
		hl := Code(fence.lang, "", strings.Join(fenceBody, "\n"), th)
		for k, l := range hl {
			if k < len(fenceBody) {
				out[fenceStart+1+k] = l
			}
		}
	}
	return out
}

// Source renders a file for the canvas, choosing the renderer by file
// type: markdown is styled in place, structured data and source code
// are syntax highlighted, everything else is plain text.
func Source(path, body string, th theme.Theme) []Line {
	body = strings.TrimSuffix(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") || strings.HasSuffix(lower, ".mdx"):
		return SourceMarkdown(body, th)
	}
	if lexerFor("", path) != nil {
		return Code("", path, body, th)
	}
	t := strings.TrimSpace(body)
	switch {
	case strings.HasPrefix(t, "{") || strings.HasPrefix(t, "["):
		return Code("json", "", body, th)
	case strings.HasPrefix(t, "<"):
		return Code("xml", "", body, th)
	}
	return Code("", "", body, th)
}

// Gutter renders canvas lines with right-aligned line numbers, marks the
// inclusive [start,end] range (1-based; start<=0 = none) and wraps each
// line to width cells (0 = no wrapping). It returns the markup and the
// visual row at which source line `start` begins, for scrolling.
func Gutter(lines []Line, start, end, width int, th theme.Theme) (string, int) {
	if end < start {
		end = start
	}
	nw := len(strconv.Itoa(len(lines)))
	gw := nw + 3 // "123 │ "
	var b strings.Builder
	row, startRow := 0, 0
	for i, l := range lines {
		n := i + 1
		in := start > 0 && n >= start && n <= end
		if n == start {
			startRow = row
		}
		numFg, numAttr, bar, barFg := th.Faint, "", "│", th.Faint
		if in {
			numFg, numAttr, bar, barFg = th.Bright, "b", "▌", th.Primary
			l = l.WithBg(th.BgSelect)
		}
		parts := Wrap(l, width-gw)
		for k, p := range parts {
			var g Line
			if k == 0 {
				g = Line{{Text: padLeft(strconv.Itoa(n), nw), Fg: numFg, Attr: numAttr}, {Text: " "}, {Text: bar, Fg: barFg}, {Text: " "}}
			} else {
				g = Line{{Text: strings.Repeat(" ", nw+1)}, {Text: "┆", Fg: barFg}, {Text: " "}}
			}
			if row > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(append(g, p...).Markup())
			row++
		}
	}
	return b.String(), startRow
}

func padLeft(s string, w int) string {
	if d := w - uniseg.StringWidth(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}
