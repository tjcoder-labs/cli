package tui

import (
	"fmt"
	"strings"
	"unicode"
)

// splitMCPArgs groups quoted argv without invoking a shell or expanding values.
func splitMCPArgs(input string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	started := false
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' && quote != '\'' && i+1 < len(runes) {
			next := runes[i+1]
			if next == '\\' || next == '"' || next == '\'' || (quote == 0 && unicode.IsSpace(next)) {
				word.WriteRune(next)
				started = true
				i++
				continue
			}
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			started = true
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
			started = true
		case unicode.IsSpace(r):
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in /mcp command")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}
