// Package watch runs a shell command for the TUI's /watch canvas and
// keeps a bounded, colour-translated scrollback of its output.
//
// Output is translated as it arrives: SGR colour sequences become tview
// style tags (through tview's own ANSI translator, so 8/256/truecolor
// all work), literal text is tview-escaped so output such as "[red]"
// can never be read as a tag, every other escape sequence (cursor
// movement, OSC titles, …) is dropped, and a bare carriage return
// rewinds the current line the way a terminal would, so progress bars
// redraw in place instead of piling up.
package watch

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/rivo/tview"
)

// DefaultMaxLines is the scrollback kept per watch.
const DefaultMaxLines = 5000

type parseState int

const (
	stText parseState = iota
	stEsc
	stCSI
	stString    // OSC/DCS/PM/APC/SOS body, ended by BEL or ST
	stStringEsc // saw ESC inside a string; '\' ends it
)

// Buffer is an io.Writer that turns raw terminal output into at most
// max tview-tagged lines. It is safe for concurrent use.
type Buffer struct {
	mu    sync.Mutex
	max   int
	lines []string
	// done holds the escaped/tagged part of the current line; run is
	// the literal text since the last tag, escaped when it is flushed.
	done, run strings.Builder
	state     parseState
	csi       bytes.Buffer
	pending   []byte // incomplete UTF-8 sequence carried across writes
	ansi      io.Writer
	tags      bytes.Buffer
	// style is the last full style tag emitted; every new or rewound
	// line starts with it so lines stay self-contained once older
	// lines scroll out of the cap.
	style   string
	rewind  bool
	version uint64
	dropped int
}

// NewBuffer keeps the last max lines (DefaultMaxLines when max <= 0).
func NewBuffer(max int) *Buffer {
	if max <= 0 {
		max = DefaultMaxLines
	}
	b := &Buffer{max: max}
	b.ansi = tview.ANSIWriter(&b.tags)
	return b
}

// Max is the line cap.
func (b *Buffer) Max() int { return b.max }

// Write implements io.Writer. It never fails.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(b.pending) > 0 {
		p = append(b.pending, p...)
		b.pending = nil
	}
	for len(p) > 0 {
		r, size := utf8.DecodeRune(p)
		if r == utf8.RuneError && size <= 1 && !utf8.FullRune(p) {
			b.pending = append([]byte(nil), p...)
			break
		}
		p = p[size:]
		b.feed(r)
	}
	b.version++
	return n, nil
}

func (b *Buffer) feed(r rune) {
	switch b.state {
	case stEsc:
		switch r {
		case '[':
			b.csi.Reset()
			b.state = stCSI
		case ']', 'P', 'X', '^', '_':
			b.state = stString
		default:
			b.state = stText // two-byte escape (ESC c, ESC =, …): drop
		}
		return
	case stCSI:
		if r >= 0x40 && r <= 0x7e {
			if r == 'm' {
				b.applySGR(b.csi.String())
			}
			// K (erase line) after \r is common in progress output;
			// \r already rewound the line, so dropping it is right.
			b.state = stText
			return
		}
		b.csi.WriteRune(r)
		if b.csi.Len() > 64 { // malformed; give up on it
			b.state = stText
		}
		return
	case stString:
		switch r {
		case 0x07:
			b.state = stText
		case 0x1b:
			b.state = stStringEsc
		}
		return
	case stStringEsc:
		if r == '\\' {
			b.state = stText
		} else {
			b.state = stString
		}
		return
	}
	switch r {
	case 0x1b:
		b.state = stEsc
	case '\n':
		b.flushLine()
	case '\r':
		// Rewind: the next text overwrites the line. A following \n
		// still ends the line with what was there (CRLF output).
		b.rewind = true
	case '\t':
		b.text('\t')
	case '\b':
		s := b.run.String()
		if _, sz := utf8.DecodeLastRuneInString(s); sz > 0 {
			b.run.Reset()
			b.run.WriteString(s[:len(s)-sz])
		}
	default:
		if r < 0x20 || r == 0x7f {
			return // other C0 controls (BEL, …)
		}
		b.text(r)
	}
}

func (b *Buffer) text(r rune) {
	if b.rewind {
		b.rewind = false
		b.done.Reset()
		b.run.Reset()
		b.done.WriteString(b.style)
	}
	b.run.WriteRune(r)
}

// applySGR translates one SGR sequence (parameters without ESC[ and m)
// into a tview tag via tview's stateful ANSI writer.
func (b *Buffer) applySGR(params string) {
	b.tags.Reset()
	_, _ = b.ansi.Write([]byte("\x1b[" + params + "m"))
	tag := b.tags.String()
	if tag == "" {
		return
	}
	if b.rewind {
		// Style change right after \r: the rewind still applies to
		// the text that follows, under the new style.
		b.style = tag
		return
	}
	b.done.WriteString(tview.Escape(b.run.String()))
	b.run.Reset()
	b.done.WriteString(tag)
	b.style = tag
}

func (b *Buffer) current() string {
	return b.done.String() + tview.Escape(b.run.String())
}

func (b *Buffer) flushLine() {
	b.rewind = false
	b.lines = append(b.lines, b.current())
	b.done.Reset()
	b.run.Reset()
	b.done.WriteString(b.style)
	if over := len(b.lines) - b.max; over > 0 {
		b.dropped += over
		// Copy instead of reslicing so dropped lines can be freed.
		if over >= b.max/8 || cap(b.lines) > 2*b.max {
			b.lines = append(make([]string, 0, b.max+b.max/4), b.lines[over:]...)
		} else {
			b.lines = b.lines[over:]
		}
	}
}

// partialLine is the unterminated last line, or "" when it has no text.
func (b *Buffer) partialLine() string {
	if b.run.Len() == 0 && b.done.String() == b.style {
		return ""
	}
	return b.current()
}

// Lines returns a copy of the complete lines plus any unterminated
// last line, capped at Max.
func (b *Buffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.lines)+1)
	out = append(out, b.lines...)
	if p := b.partialLine(); p != "" {
		out = append(out, p)
	}
	if len(out) > b.max {
		out = out[len(out)-b.max:]
	}
	return out
}

// Text is Lines joined with newlines, ready for a dynamic-colour
// tview.TextView.
func (b *Buffer) Text() string { return strings.Join(b.Lines(), "\n") }

// Len is the number of lines Lines would return.
func (b *Buffer) Len() int { return len(b.Lines()) }

// Dropped counts lines discarded by the cap since the last Reset.
func (b *Buffer) Dropped() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// Version changes on every Write, Reset or Replace, so a renderer can
// skip redraws when nothing changed.
func (b *Buffer) Version() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.version
}

// Reset empties the buffer and the colour state.
func (b *Buffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resetLocked()
	b.version++
}

func (b *Buffer) resetLocked() {
	b.lines = nil
	b.done.Reset()
	b.run.Reset()
	b.state = stText
	b.pending = nil
	b.style = ""
	b.rewind = false
	b.dropped = 0
	b.tags.Reset()
	b.ansi = tview.ANSIWriter(&b.tags)
}

// Replace swaps in other's content (used by interval mode so each run
// appears at once instead of flickering through an empty screen).
func (b *Buffer) Replace(other *Buffer) {
	lines := other.Lines()
	dropped := other.Dropped()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resetLocked()
	if len(lines) > b.max {
		dropped += len(lines) - b.max
		lines = lines[len(lines)-b.max:]
	}
	b.lines = lines
	b.dropped = dropped
	b.version++
}

// AppendNote adds one literal (escaped) line in the given tview colour
// tag, e.g. a "process exited" marker.
func (b *Buffer) AppendNote(colorTag, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p := b.partialLine(); p != "" {
		b.flushLine()
	}
	line := tview.Escape(text)
	if colorTag != "" {
		line = colorTag + line + "[-:-:-]"
	}
	b.lines = append(b.lines, line)
	if over := len(b.lines) - b.max; over > 0 {
		b.dropped += over
		b.lines = append([]string(nil), b.lines[over:]...)
	}
	b.version++
}
