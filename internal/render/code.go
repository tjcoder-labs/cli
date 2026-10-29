package render

import (
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/tjcoder-labs/cli/internal/theme"
)

// langAliases normalises the info strings people put after ``` fences.
var langAliases = map[string]string{
	"ts": "typescript", "tsx": "tsx", "js": "javascript", "jsx": "react", "mjs": "javascript", "cjs": "javascript",
	"sh": "bash", "shell": "bash", "zsh": "bash", "console": "bash", "shell-session": "bash",
	"yml": "yaml", "py": "python", "python3": "python", "rs": "rust", "golang": "go",
	"jsonc": "json", "json5": "json", "ndjson": "json", "htm": "html", "xhtml": "html", "svg": "xml",
	"md": "markdown", "patch": "diff", "c++": "cpp", "cxx": "cpp", "h": "c", "hpp": "cpp",
	"dockerfile": "docker", "tf": "terraform", "hcl": "terraform", "ps1": "powershell",
	"postgres": "postgresql", "psql": "postgresql", "mysql": "mysql", "plaintext": "", "text": "", "txt": "",
}

// lexerFor picks a chroma lexer from a fence language or a file path.
// It returns nil for plain text.
func lexerFor(lang, path string) chroma.Lexer {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, " {,"); i >= 0 {
		lang = lang[:i]
	}
	if a, ok := langAliases[lang]; ok {
		lang = a
		if lang == "" {
			return nil
		}
	}
	if lang != "" {
		if l := lexers.Get(lang); l != nil {
			return chroma.Coalesce(l)
		}
	}
	if path != "" {
		if l := lexers.Match(filepath.Base(path)); l != nil {
			return chroma.Coalesce(l)
		}
	}
	return nil
}

// HasLexer reports whether lang names a language we can highlight.
func HasLexer(lang string) bool { return lexerFor(lang, "") != nil }

type tokStyle struct{ fg, attr string }

// styleFor maps a chroma token type to a palette role. Status-like
// token kinds (diff insert/delete, errors) are expressed through shade
// and attributes, never through an off-scheme hue.
func styleFor(t chroma.TokenType, th theme.Theme) tokStyle {
	exact := map[chroma.TokenType]tokStyle{
		chroma.KeywordConstant:       {th.Bright, ""},
		chroma.KeywordType:           {th.Deep, "b"},
		chroma.NameFunction:          {th.Primary, ""},
		chroma.NameFunctionMagic:     {th.Primary, ""},
		chroma.NameClass:             {th.Deep, "b"},
		chroma.NameBuiltin:           {th.Deep, ""},
		chroma.NameBuiltinPseudo:     {th.Deep, ""},
		chroma.NameTag:               {th.Bright, ""},
		chroma.NameAttribute:         {th.Primary, ""},
		chroma.NameDecorator:         {th.Primary, "i"},
		chroma.NameNamespace:         {th.Deep, ""},
		chroma.NameException:         {th.Bright, "b"},
		chroma.NameConstant:          {th.Bright, ""},
		chroma.NameVariable:          {th.Text, ""},
		chroma.NameProperty:          {th.Primary, ""},
		chroma.NameLabel:             {th.Primary, ""},
		chroma.NameEntity:            {th.Deep, ""},
		chroma.LiteralStringEscape:   {th.Bright, ""},
		chroma.LiteralStringDoc:      {th.Dim, "i"},
		chroma.LiteralStringSymbol:   {th.Bright, ""},
		chroma.LiteralStringRegex:    {th.Bright, ""},
		chroma.LiteralStringInterpol: {th.Bright, ""},
		chroma.CommentPreproc:        {th.Primary, ""},
		chroma.CommentPreprocFile:    {th.Soft, ""},
		chroma.GenericInserted:       {th.Bright, "b"},
		chroma.GenericDeleted:        {th.Dim, "s"},
		chroma.GenericHeading:        {th.Primary, "b"},
		chroma.GenericSubheading:     {th.Primary, ""},
		chroma.GenericEmph:           {th.Text, "i"},
		chroma.GenericStrong:         {th.Text, "b"},
		chroma.GenericPrompt:         {th.Dim, ""},
		chroma.GenericOutput:         {th.Dim, ""},
		chroma.GenericError:          {th.Bright, "bu"},
		chroma.GenericTraceback:      {th.Dim, ""},
		chroma.Error:                 {th.Text, ""},
	}
	if s, ok := exact[t]; ok {
		return s
	}
	switch t.Category() {
	case chroma.Keyword:
		return tokStyle{th.Bright, "b"}
	case chroma.LiteralString:
		return tokStyle{th.Soft, ""}
	case chroma.LiteralNumber:
		return tokStyle{th.Deep, ""}
	case chroma.Comment:
		return tokStyle{th.Dim, "i"}
	case chroma.Operator:
		return tokStyle{th.Dim, ""}
	case chroma.Punctuation:
		return tokStyle{th.Dim, ""}
	}
	switch {
	case t.InCategory(chroma.Literal) && t.InSubCategory(chroma.LiteralString):
		return tokStyle{th.Soft, ""}
	case t.InCategory(chroma.Literal):
		return tokStyle{th.Deep, ""}
	}
	return tokStyle{th.Text, ""}
}

// Code highlights src and returns one Line per source line. Unknown
// languages render as plain body text. The result always has exactly
// as many lines as strings.Split(src, "\n").
func Code(lang, path, src string, th theme.Theme) []Line {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\t", "    ")
	want := strings.Count(src, "\n") + 1
	plain := func() []Line {
		raw := strings.Split(src, "\n")
		out := make([]Line, len(raw))
		for i, r := range raw {
			out[i] = Line{{Text: r, Fg: th.Text}}
		}
		return out
	}
	lx := lexerFor(lang, path)
	if lx == nil {
		return plain()
	}
	it, err := lx.Tokenise(nil, src)
	if err != nil {
		return plain()
	}
	lines := []Line{{}}
	for tok := it(); tok != chroma.EOF; tok = it() {
		st := styleFor(tok.Type, th)
		parts := strings.Split(tok.Value, "\n")
		for i, p := range parts {
			if i > 0 {
				lines = append(lines, Line{})
			}
			if p == "" {
				continue
			}
			cur := &lines[len(lines)-1]
			if n := len(*cur); n > 0 && (*cur)[n-1].Fg == st.fg && (*cur)[n-1].Attr == st.attr {
				(*cur)[n-1].Text += p
				continue
			}
			*cur = append(*cur, Span{Text: p, Fg: st.fg, Attr: st.attr})
		}
	}
	for len(lines) > want {
		lines = lines[:len(lines)-1]
	}
	for len(lines) < want {
		lines = append(lines, Line{})
	}
	return lines
}
