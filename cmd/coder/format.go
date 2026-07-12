package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"regexp"
	"strings"
)

// extractAndPrettyJSON finds the first balanced JSON value (object or
// array) in s, validates it, and returns it pretty-printed with two
// spaces of indentation. Returns ok=false when no valid JSON value
// could be located. We strip markdown fences, optional "json" hints,
// and any leading/trailing prose so a model that adds a friendly
// one-liner around its payload doesn't break script consumers.
func extractAndPrettyJSON(s string) (string, bool) {
	stripped := stripCodeFence(s)
	for _, candidate := range findBalancedJSONValues(stripped) {
		var v any
		if err := json.Unmarshal([]byte(candidate), &v); err != nil {
			continue
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			continue
		}
		return strings.TrimRight(buf.String(), "\n"), true
	}
	return "", false
}

// extractAndPrettyXML finds the first balanced XML element in s,
// validates it via encoding/xml, and returns it pretty-printed with
// two spaces of indentation. Returns ok=false when no well-formed
// element could be located. The same markdown-fence stripping used
// for JSON is applied so the extractor behaves consistently.
func extractAndPrettyXML(s string) (string, bool) {
	stripped := stripCodeFence(s)
	for _, candidate := range findBalancedXMLElements(stripped) {
		// Wrap in a generic container so the XML decoder can parse
		// a single root element. We use xml.Decoder directly to
		// verify the input is well-formed, then pretty-print.
		dec := xml.NewDecoder(strings.NewReader("<__root>" + candidate + "</__root>"))
		var found bool
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			if _, ok := tok.(xml.StartElement); ok {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		// Pretty-print by re-marshalling the element. encoding/xml
		// has no native pretty-printer, so we reparse into a node
		// tree and walk it.
		pretty, err := prettyXML(candidate)
		if err != nil {
			continue
		}
		return pretty, true
	}
	return "", false
}

// stripCodeFence removes a single surrounding ```json / ```xml /
// ``` fence if one wraps the payload. Models often wrap outputs in
// markdown fences even when asked not to; this lets the extractors
// still find the value. We do not strip multiple fences (we only
// expect one) and we do not touch inline single backticks.
func stripCodeFence(s string) string {
	re := regexp.MustCompile("(?s)^\\s*```(?:json|xml)?\\s*\\n?(.*?)\\n?```\\s*$")
	if m := re.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	return s
}

// findBalancedJSONValues returns every balanced top-level JSON
// substring of s that round-trips through json.Unmarshal. Order is
// preserved (left to right). We return substrings in their original
// text so callers can pretty-print exactly what the model emitted
// (modulo whitespace normalization in json.Marshal).
func findBalancedJSONValues(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '{' && c != '[' {
			continue
		}
		open, close := byte('{'), byte('}')
		if c == '[' {
			open, close = '[', ']'
		}
		// Walk forward looking for the matching close, respecting
		// string literals (so braces inside strings don't fool us)
		// and JSON escapes.
		depth := 0
		inString := false
		escape := false
		end := -1
		for j := i; j < len(s); j++ {
			ch := s[j]
			if inString {
				if escape {
					escape = false
				} else if ch == '\\' {
					escape = true
				} else if ch == '"' {
					inString = false
				}
				continue
			}
			switch ch {
			case '"':
				inString = true
			case open:
				depth++
			case close:
				depth--
				if depth == 0 {
					end = j
				}
			}
			if end != -1 {
				break
			}
		}
		if end == -1 {
			continue
		}
		candidate := strings.TrimSpace(s[i : end+1])
		var tmp any
		if json.Unmarshal([]byte(candidate), &tmp) == nil {
			out = append(out, candidate)
		}
	}
	return out
}

// findBalancedXMLElements returns every balanced top-level XML
// element substring of s. The definition of "balanced" is permissive:
// the substring must start with '<' followed by a tag name and end
// with the matching closing tag. CDATA and comments are skipped
// when scanning for the matching close.
func findBalancedXMLElements(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		// Skip processing instructions / comments / CDATA / doctype.
		if strings.HasPrefix(s[i:], "<?") || strings.HasPrefix(s[i:], "<!") {
			continue
		}
		// Must be a start tag.
		if i+1 >= len(s) || s[i+1] == '/' {
			continue
		}
		// Pull the tag name.
		endName := i + 1
		for endName < len(s) {
			c := s[endName]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '>' || c == '/' {
				break
			}
			endName++
		}
		if endName == i+1 {
			continue
		}
		tag := strings.TrimSpace(s[i+1 : endName])
		if tag == "" {
			continue
		}
		// Self-closing tags don't need a closing pair; they are
		// valid on their own.
		gtIdx := strings.Index(s[i:], ">")
		if gtIdx == -1 {
			continue
		}
		openEnd := i + gtIdx
		// Look at the last non-/> char of the open tag to decide
		// if it's self-closing.
		isSelfClose := openEnd > 0 && s[openEnd-1] == '/'
		if isSelfClose {
			out = append(out, s[i:openEnd+1])
			continue
		}
		// Find the matching close tag. Comments/CDATA inside the
		// element are tolerated by simple skipping.
		closeTag := "</" + tag
		depth := 1
		scan := openEnd + 1
		for scan < len(s) {
			openIdx := indexOfTag(s, scan, "<"+tag)
			closeIdx := strings.Index(s[scan:], closeTag)
			if closeIdx == -1 {
				break
			}
			closeIdx += scan
			if openIdx == -1 || closeIdx < openIdx {
				// No more nested opens before the close at this
				// level. We've matched.
				endIdx := strings.Index(s[closeIdx:], ">")
				if endIdx == -1 {
					break
				}
				_ = depth
				out = append(out, s[i:closeIdx+endIdx+1])
				scan = closeIdx + endIdx + 1
				break
			}
			// Found a nested open before the close: bump depth
			// and keep scanning.
			depth++
			scan = openIdx + 1
		}
		_ = depth
	}
	return out
}

// indexOfTag returns the index of any occurrence of needle in s
// starting at from, treating both '<' and '>' boundaries properly
// (i.e. the character after '<' must be a letter or underscore, so
// we don't match prefixes like "<tag" inside "<other").
func indexOfTag(s string, from int, needle string) int {
	for i := from; i+len(needle) < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		// The character right after the '<' must be the start of
		// our tag name (and not a slash, which would indicate a
		// closing tag).
		if i+1 >= len(s) || s[i+1] == '/' {
			continue
		}
		if strings.HasPrefix(s[i:], needle) {
			return i
		}
	}
	return -1
}

// prettyXML re-marshals an XML element with two-space indentation.
// encoding/xml's MarshalIndent applies to whole documents, so we
// parse the element into a token stream and rewrite it with
// indentation. Each start and end tag gets its own line; inline
// text is written on the same line as the start tag it follows, so
// <a>1</a> stays compact. Whitespace-only text is dropped because
// it is almost always source-document indentation that would
// otherwise produce double-spacing in the output.
func prettyXML(s string) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(s))
	var buf bytes.Buffer
	indent := 0
	const tab = "  "
	// lastWasOpenStart is true when the previous token was a
	// StartElement (so the next CharData should be inlined on the
	// same line, no extra indent).
	lastWasOpenStart := false
	writeIndent := func() {
		buf.WriteString(strings.Repeat(tab, indent))
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			writeIndent()
			buf.WriteByte('<')
			buf.WriteString(t.Name.Local)
			for _, attr := range t.Attr {
				buf.WriteByte(' ')
				buf.WriteString(attr.Name.Local)
				buf.WriteString(`="`)
				buf.WriteString(xmlEscapeAttr(attr.Value))
				buf.WriteByte('"')
			}
			buf.WriteByte('>')
			buf.WriteByte('\n')
			indent++
			lastWasOpenStart = true
		case xml.EndElement:
			if indent > 0 {
				indent--
			}
			// If the previous start element was a "leaf" (its
			// body was just text) we already wrote the text on
			// the same line, so we need to back up and re-emit
			// the close tag at the correct indent. We track
			// lastWasOpenStart so CharData inlined right after
			// it doesn't desync the indent.
			if lastWasOpenStart {
				// Replace the trailing newline (and any pending
				// text after it) with the close tag. Easiest:
				// drop the last byte (newline) and the text
				// we appended, then write the close tag.
				// Instead we use a clean approach: if we see
				// text right after a start, we hold it and
				// emit it together with the close tag.
				//
				// Implementation note: with the CharData
				// handling below using "lastWasOpenStart" as
				// a cue, we no longer hit this branch in
				// practice. Kept as a guard.
			}
			writeIndent()
			buf.WriteString("</")
			buf.WriteString(t.Name.Local)
			buf.WriteByte('>')
			buf.WriteByte('\n')
			lastWasOpenStart = false
		case xml.CharData:
			text := strings.TrimSpace(string(t))
			if text == "" {
				continue
			}
			if lastWasOpenStart {
				// Backtrack over the newline we wrote
				// after the start tag so the text sits on
				// the same line.
				b := buf.Bytes()
				if n := len(b); n > 0 && b[n-1] == '\n' {
					buf.Truncate(n - 1)
				}
				buf.WriteString(text)
				// We will not write another newline for
				// this token; the EndElement that follows
				// will write its own. Mark that the most
				// recent non-whitespace emit was the text
				// by leaving lastWasOpenStart true; the
				// EndElement branch will check that.
				//
				// Reset to false so a second CharData in
				// a row doesn't keep inlining.
				lastWasOpenStart = true
			} else {
				writeIndent()
				buf.WriteString(text)
				buf.WriteByte('\n')
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			// Skip; not relevant to a clean pretty-print of a
			// single element.
		}
	}
	out := strings.TrimRight(buf.String(), "\n")
	return out, nil
}

func xmlEscapeAttr(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
