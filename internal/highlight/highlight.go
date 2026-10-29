// Package highlight turns a (path, line range) into a tview-ready
// string with line numbers and syntax colouring. Colours come from the
// central theme (internal/theme) via internal/render, so every token is
// a shade of the primary colour.
package highlight

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"

	"github.com/tjcoder-labs/cli/internal/render"
	"github.com/tjcoder-labs/cli/internal/theme"
)

// Segment is a single window into a file. Start and End are both
// 1-based and inclusive. Start == 0 means "from the beginning";
// End == 0 means "to the end of the file".
type Segment struct {
	Path  string
	Start int
	End   int
	Body  string // raw file contents
}

// Render produces a syntax-coloured, line-numbered rendering of the
// segment. The returned string is suitable for tview.TextView.
func Render(seg Segment) (string, error) {
	if seg.Body == "" {
		return "", fmt.Errorf("empty body")
	}
	lines := strings.Split(seg.Body, "\n")
	total := len(lines)
	start := seg.Start
	if start < 1 {
		start = 1
	}
	end := seg.End
	if end < 1 || end > total {
		end = total
	}
	if start > end {
		return "", fmt.Errorf("invalid range: start=%d end=%d total=%d", start, end, total)
	}
	th := theme.Current()
	window := strings.Join(lines[start-1:end], "\n")
	var body strings.Builder
	for i, l := range render.Code("", seg.Path, window, th) {
		if i > 0 {
			body.WriteByte('\n')
		}
		body.WriteString(l.Markup())
	}
	return Header(seg.Path, start, end, total) + NumberLines(body.String(), start) + "\n", nil
}

// NumberLines prefixes each line of `colored` with a right-aligned,
// faint line number gutter.
func NumberLines(colored string, startLine int) string {
	lines := strings.Split(colored, "\n")
	width := len(fmt.Sprintf("%d", startLine+len(lines)-1))
	faint := theme.Current().Faint
	gutterFmt := fmt.Sprintf("[%s]%%%dd[-] [%s]│[-] ", faint, width, faint)
	var b strings.Builder
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			break
		}
		fmt.Fprintf(&b, gutterFmt, startLine+i)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// Header returns the small status line shown above the rendered
// code, e.g. "path  L120-L145 of 480".
func Header(path string, start, end, total int) string {
	th := theme.Current()
	return fmt.Sprintf("[%s]%s[-]  [%s]L%d-L%d of %d[-]\n\n",
		th.Primary, tview.Escape(path), th.Dim, start, end, total)
}
