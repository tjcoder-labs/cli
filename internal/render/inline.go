package render

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tjcoder-labs/cli/internal/theme"
)

// inline renders CommonMark-ish inline syntax (emphasis, strong,
// strikethrough, code spans, links, autolinks, backslash escapes) into
// spans on top of a base style.
//
// An opening delimiter is only honoured when its closer is present in
// the same text, so emphasis never stays open across calls. When parse
// is called on a partial (still streaming) line and an opener's closer
// has not arrived yet, parse stops before the opener and reports how
// much it consumed; the caller re-submits the rest once more text
// arrives.
type inline struct {
	th        theme.Theme
	fg, attr  string // base style
	codeBg    string // background for code spans ("" = none)
	showMarks bool   // source view: keep delimiters visible (faint)
	prev      rune   // previous visible rune (0 = start of line)
}

type emState struct{ bold, italic, strike bool }

func (in *inline) style(st emState) (string, string) {
	a := in.attr
	if st.bold {
		a += "b"
	}
	if st.italic {
		a += "i"
	}
	if st.strike {
		a += "s"
	}
	return in.fg, a
}

func isSpace(r rune) bool { return r == 0 || unicode.IsSpace(r) }
func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

const asciiPunct = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// parse renders s. With final=false it may stop early (see type doc).
func (in *inline) parse(s string, final bool) ([]Span, int) {
	var out []Span
	st := emState{}
	add := func(text, fg, bg, attr string) {
		if text == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].Fg == fg && out[n-1].Bg == bg && out[n-1].Attr == attr {
			out[n-1].Text += text
		} else {
			out = append(out, Span{Text: text, Fg: fg, Bg: bg, Attr: attr})
		}
		r, _ := utf8.DecodeLastRuneInString(text)
		in.prev = r
	}
	lit := func(text string) {
		fg, a := in.style(st)
		add(text, fg, "", a)
	}
	mark := func(text string) {
		if in.showMarks {
			add(text, in.th.Faint, "", "")
		} else if text != "" {
			r, _ := utf8.DecodeLastRuneInString(text)
			in.prev = r
		}
	}
	i := 0
	for i < len(s) {
		r, w := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\\' && i+1 < len(s) && strings.IndexByte(asciiPunct, s[i+1]) >= 0:
			if in.showMarks {
				mark("\\")
			}
			lit(s[i+1 : i+2])
			i += 2
			continue

		case r == '`':
			k := runLen(s, i, '`')
			closeAt := findCodeClose(s, i+k, k)
			if closeAt < 0 {
				if !final {
					return out, i
				}
				lit(s[i : i+k])
				i += k
				continue
			}
			body := s[i+k : closeAt]
			if len(body) >= 2 && body[0] == ' ' && body[len(body)-1] == ' ' && strings.TrimSpace(body) != "" {
				body = body[1 : len(body)-1]
			}
			mark(s[i : i+k])
			add(body, in.th.Soft, in.codeBg, "")
			mark(s[closeAt : closeAt+k])
			i = closeAt + k
			continue

		case r == '*' || r == '_':
			k := runLen(s, i, byte(r))
			if k > 3 {
				lit(s[i : i+k])
				i += k
				continue
			}
			next := rune(0)
			if i+k < len(s) {
				next, _ = utf8.DecodeRuneInString(s[i+k:])
			}
			canOpen := !isSpace(next)
			canClose := !isSpace(in.prev)
			if r == '_' {
				canOpen = canOpen && !isAlnum(in.prev)
				canClose = canClose && !isAlnum(next)
			}
			handled := false
			// Closing first.
			if canClose {
				switch {
				case k == 3 && st.bold && st.italic:
					mark(s[i : i+k])
					st.bold, st.italic = false, false
					handled = true
				case k == 2 && st.bold:
					mark(s[i : i+k])
					st.bold = false
					handled = true
				case k == 1 && st.italic:
					mark(s[i : i+k])
					st.italic = false
					handled = true
				}
			}
			if !handled && canOpen {
				closer := strings.Repeat(string(r), k)
				if hasEmphasisCloser(s[i+k:], closer, r) {
					mark(s[i : i+k])
					switch k {
					case 3:
						st.bold, st.italic = true, true
					case 2:
						st.bold = true
					case 1:
						st.italic = true
					}
					handled = true
				} else if !final {
					return out, i
				}
			}
			if !handled {
				lit(s[i : i+k])
			}
			i += k
			continue

		case r == '~' && strings.HasPrefix(s[i:], "~~"):
			next := rune(0)
			if i+2 < len(s) {
				next, _ = utf8.DecodeRuneInString(s[i+2:])
			}
			if st.strike && !isSpace(in.prev) {
				mark("~~")
				st.strike = false
				i += 2
				continue
			}
			if !st.strike && !isSpace(next) {
				if strings.Contains(s[i+2:], "~~") {
					mark("~~")
					st.strike = true
					i += 2
					continue
				}
				if !final {
					return out, i
				}
			}
			lit("~~")
			i += 2
			continue

		case r == '[' || (r == '!' && strings.HasPrefix(s[i:], "![")):
			img := r == '!'
			start := i
			if img {
				start++
			}
			text, url, end, ok := parseLink(s, start)
			if !ok {
				if !final && !strings.Contains(s[start:], "]") {
					return out, i
				}
				if !final && end < 0 {
					return out, i
				}
				lit(s[i : i+w])
				i += w
				continue
			}
			sub := inline{th: in.th, fg: in.th.Bright, attr: "u", codeBg: in.codeBg, showMarks: in.showMarks}
			if img {
				mark("!")
				if !in.showMarks {
					add("▣ ", in.th.Dim, "", "")
				}
			}
			mark("[")
			spans, _ := sub.parse(text, true)
			for _, sp := range spans {
				add(sp.Text, sp.Fg, sp.Bg, sp.Attr)
			}
			if in.showMarks {
				mark("](")
				add(url, in.th.Dim, "", "")
				mark(")")
			} else if url != "" && url != text {
				add(" ("+url+")", in.th.Dim, "", "")
			}
			i = end
			continue

		case r == '<':
			if j := strings.IndexByte(s[i:], '>'); j > 0 {
				inner := s[i+1 : i+j]
				if isAutolink(inner) {
					mark("<")
					add(inner, in.th.Bright, "", "u")
					mark(">")
					i += j + 1
					continue
				}
			} else if !final && isAutolinkPrefix(s[i+1:]) {
				return out, i
			}

		case (r == 'h' || r == 'w') && isSpace(in.prev) || (r == 'h' || r == 'w') && strings.ContainsRune("([", in.prev):
			if n := bareURL(s[i:]); n > 0 {
				add(s[i:i+n], in.th.Bright, "", "u")
				i += n
				continue
			}
		}
		lit(s[i : i+w])
		i += w
	}
	return out, len(s)
}

func runLen(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

func findCodeClose(s string, from, k int) int {
	for j := from; j < len(s); {
		if s[j] == '`' {
			n := runLen(s, j, '`')
			if n == k {
				return j
			}
			j += n
			continue
		}
		j++
	}
	return -1
}

// hasEmphasisCloser reports whether rest contains a right-flanking run
// equal to closer (or a longer run that can close it).
func hasEmphasisCloser(rest, closer string, r rune) bool {
	k := len(closer)
	for j := 0; j < len(rest); {
		if rest[j] == byte(r) {
			n := runLen(rest, j, byte(r))
			prev, _ := utf8.DecodeLastRuneInString(rest[:j])
			next := rune(0)
			if j+n < len(rest) {
				next, _ = utf8.DecodeRuneInString(rest[j+n:])
			}
			ok := j > 0 && !isSpace(prev)
			if r == '_' {
				ok = ok && !isAlnum(next)
			}
			if ok && (n == k || (n == 3 && k < 3)) {
				return true
			}
			j += n
			continue
		}
		if rest[j] == '`' { // skip code spans
			n := runLen(rest, j, '`')
			if c := findCodeClose(rest, j+n, n); c >= 0 {
				j = c + n
				continue
			}
			j += n
			continue
		}
		j++
	}
	return false
}

// parseLink parses "[text](url)" starting at s[i]=='['. end is the index
// after the link, or -1 when the link is not complete (yet).
func parseLink(s string, i int) (text, url string, end int, ok bool) {
	depth := 0
	j := i
	for ; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '[':
			depth++
		case ']':
			depth--
		}
		if depth == 0 {
			break
		}
	}
	if j >= len(s) {
		return "", "", -1, false
	}
	text = s[i+1 : j]
	if j+1 >= len(s) {
		return "", "", -1, false
	}
	if s[j+1] != '(' {
		return "", "", j + 1, false
	}
	k := strings.IndexByte(s[j+2:], ')')
	if k < 0 {
		return "", "", -1, false
	}
	url = strings.TrimSpace(s[j+2 : j+2+k])
	if sp := strings.IndexAny(url, " \t"); sp >= 0 { // drop link title
		url = url[:sp]
	}
	url = strings.Trim(url, "<>")
	return text, url, j + 2 + k + 1, true
}

func isAutolink(s string) bool {
	if strings.ContainsAny(s, " \t<") {
		return false
	}
	for _, p := range []string{"http://", "https://", "mailto:", "ftp://"} {
		if strings.HasPrefix(s, p) && len(s) > len(p) {
			return true
		}
	}
	return false
}

func isAutolinkPrefix(s string) bool {
	if strings.ContainsAny(s, " \t") {
		return false
	}
	for _, p := range []string{"http://", "https://", "mailto:"} {
		if strings.HasPrefix(p, s) || strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// bareURL returns the byte length of a bare http(s):// or www. URL at
// the start of s, trimming trailing punctuation; 0 if none.
func bareURL(s string) int {
	if !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "www.") {
		return 0
	}
	n := 0
	for n < len(s) {
		r, w := utf8.DecodeRuneInString(s[n:])
		if unicode.IsSpace(r) || r == '<' || r == '>' || r == '"' || r == '`' {
			break
		}
		n += w
	}
	for n > 0 && strings.IndexByte(".,:;!?)]'*_", s[n-1]) >= 0 {
		n--
	}
	if n <= len("https://") {
		return 0
	}
	return n
}
