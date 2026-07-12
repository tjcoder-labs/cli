// Package highlight turns a (path, line range) into a tview-ready
// string with line numbers and chroma syntax coloring. The output is
// a tview dynamic-color string (the same format produced by the rest
// of the TUI) so callers can drop it straight into a TextView.
package highlight

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// segment is a single window into a file. Start and End are both
// 1-based and inclusive. Start == 0 means "from the beginning";
// End == 0 means "to the end of the file".
type Segment struct {
	Path  string
	Start int
	End   int
	Body  string // raw file contents
}

// Render produces a syntax-colored, line-numbered rendering of the
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
	window := strings.Join(lines[start-1:end], "\n")

	// Pick a lexer by extension. Fall back to plain text so we
	// never crash on an unknown file type.
	lexer := lexers.Match(seg.Path)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	// quickhighlight produces chroma-formatted output with the
	// 16-color palette, which translates cleanly into tview's hex
	// color tags via the mapping in ToTview.
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	it, err := lexer.Tokenise(nil, window)
	if err != nil {
		return "", err
	}

	var body strings.Builder
	for _, tok := range it.Tokens() {
		body.WriteString(ToTview(tok.Value, tok.Type))
	}

	// Re-split on newlines and re-attach line numbers so the
	// reader can see exactly where each line lives in the file.
	numbered := NumberLines(body.String(), start)
	return Header(seg.Path, start, end, total) + numbered + "\n", nil
}

// NumberLines prefixes each line of `colored` with a right-aligned
// line number. The line-number column is dimmed via tview color
// tags so it reads as a gutter rather than competing with the code.
func NumberLines(colored string, startLine int) string {
	// Replace literal "\n" with newlines (chroma may have
	// collapsed them in some tokens) and split on real newlines.
	lines := strings.Split(colored, "\n")
	width := len(fmt.Sprintf("%d", startLine+len(lines)-1))
	pad := strings.Repeat(" ", width)
	gutterFmt := fmt.Sprintf("[#564A70]%%%dd[-] [#564A70]│[-] ", width)
	var b strings.Builder
	for i, line := range lines {
		// chroma sometimes emits an empty trailing line because
		// the original window ended with "\n"; skip it so the
		// user doesn't see a phantom empty row.
		if i == len(lines)-1 && line == "" {
			break
		}
		fmt.Fprintf(&b, gutterFmt, startLine+i)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	_ = pad
	return b.String()
}

// Header returns the small status line shown above the rendered
// code, e.g. "[path]  L120-L145 of 480".
func Header(path string, start, end, total int) string {
	return fmt.Sprintf(
		"[#A77CF8]%s[-]  [#968CB2]L%d-L%d of %d[-]\n\n",
		path, start, end, total,
	)
}

// ToTview maps a chroma token to a tview dynamic-color string.
// chroma names its categories in a hierarchy; we only need the
// 16-color subset that quickhighlight emits, so this stays short.
func ToTview(text string, tt chroma.TokenType) string {
	color, ok := chromaToHex[tt]
	if !ok {
		return text
	}
	return fmt.Sprintf("[%s]%s[-]", color, text)
}

// chromaToHex is a hand-rolled mapping from the most common chroma
// sub-categories to tview hex colors that read well on the dark
// panel background. Categories not in the map fall through to
// uncolored text.
var chromaToHex = map[chroma.TokenType]string{
	// Keywords / control flow
	chroma.Keyword:          "#C4A5FF", // lavender
	chroma.KeywordConstant: "#C4A5FF",
	chroma.KeywordDeclaration: "#C4A5FF",
	chroma.KeywordNamespace: "#C4A5FF",
	chroma.KeywordPseudo:   "#968CB2",
	chroma.KeywordReserved: "#C4A5FF",
	chroma.KeywordType:     "#7C3AED", // violet
	// Names (identifiers, functions, classes)
	chroma.Name:               "#E8E2F5",
	chroma.NameAttribute:      "#A77CF8",
	chroma.NameBuiltin:        "#A77CF8",
	chroma.NameBuiltinPseudo:  "#968CB2",
	chroma.NameClass:          "#A77CF8",
	chroma.NameConstant:       "#E8E2F5",
	chroma.NameDecorator:      "#C73CDC", // orchid
	chroma.NameEntity:         "#E8E2F5",
	chroma.NameException:      "#C73CDC",
	chroma.NameFunction:       "#A77CF8",
	chroma.NameLabel:          "#E8E2F5",
	chroma.NameNamespace:      "#E8E2F5",
	chroma.NameOther:          "#E8E2F5",
	chroma.NameTag:            "#C4A5FF",
	chroma.NameVariable:       "#E8E2F5",
	chroma.NameVariableClass:  "#E8E2F5",
	chroma.NameVariableGlobal: "#E8E2F5",
	chroma.NameVariableInstance: "#E8E2F5",
	// Literals
	chroma.Literal:           "#E8E2F5",
	chroma.LiteralDate:       "#C4A5FF",
	chroma.LiteralNumber:     "#C4A5FF",
	chroma.LiteralString:     "#A6E3A1", // soft green for strings
	chroma.LiteralStringBacktick: "#A6E3A1",
	chroma.LiteralStringChar:    "#A6E3A1",
	chroma.LiteralStringDoc:     "#968CB2",
	chroma.LiteralStringEscape:  "#C73CDC",
	chroma.LiteralStringHeredoc:  "#A6E3A1",
	chroma.LiteralStringInterpol: "#C4A5FF",
	chroma.LiteralStringOther:   "#A6E3A1",
	chroma.LiteralStringRegex:   "#C73CDC",
	chroma.LiteralStringSymbol:  "#A6E3A1",
	// Operators / punctuation
	chroma.Operator:   "#968CB2",
	chroma.OperatorWord: "#C4A5FF",
	chroma.Punctuation: "#968CB2",
	// Comments
	chroma.Comment:           "#564A70", // faint
	chroma.CommentHashbang:   "#564A70",
	chroma.CommentMultiline:  "#564A70",
	chroma.CommentPreproc:    "#968CB2",
	chroma.CommentPreprocFile: "#968CB2",
	chroma.CommentSingle:     "#564A70",
	chroma.CommentSpecial:    "#968CB2",
	// Misc
	chroma.Other:          "#E8E2F5",
	chroma.Text:           "#E8E2F5",
	chroma.TextWhitespace: "#564A70",
	chroma.Error:          "#C73CDC",
}
