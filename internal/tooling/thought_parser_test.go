package tooling

import (
	"strings"
	"testing"
)

// feed streams s through the parser one rune at a time (worst-case chunking)
// and returns the concatenated reasoning and commentary output.
func feed(s string) (reasoning, commentary string) {
	p := &thoughtParser{}
	for _, r := range s {
		rr, cc := p.Add(string(r))
		reasoning += rr
		commentary += cc
	}
	rr, cc := p.Flush()
	return reasoning + rr, commentary + cc
}

func TestThoughtParser_SuppressesToolCallMarkup(t *testing.T) {
	openA, closeA := "<tool_call>", "</tool_call>"
	openB, closeB := "\u2039tool_call\u203a", "\u2039/tool_call\u203a"

	cases := []struct {
		name       string
		input      string
		wantComm   string // exact commentary expected
		wantNoMark bool   // commentary must not contain wrapper fragments
	}{
		{
			name:       "angle bracket wrapper",
			input:      "Installing tools. " + openA + `{"name":"run_command","arguments":{}}` + closeA + " Done.",
			wantComm:   "Installing tools.  Done.",
			wantNoMark: true,
		},
		{
			name:       "single angle quote wrapper",
			input:      "Scanning. " + openB + `{"name":"run_command","arguments":{}}` + closeB + " Finished.",
			wantComm:   "Scanning.  Finished.",
			wantNoMark: true,
		},
		{
			name:       "think and tool_call interleaved",
			input:      "<think>plan</think>Prose. " + openA + `{"name":"x"}` + closeA + " Tail.",
			wantComm:   "Prose.  Tail.",
			wantNoMark: true,
		},
		{
			name:       "pipe delimited wrapper",
			input:      "Working. <tool_call|>" + `{"name":"run_command","arguments":{}}` + "<tool_call|> Next.",
			wantComm:   "Working.  Next.",
			wantNoMark: true,
		},
		{
			name:       "double pipe delimited wrapper",
			input:      "Go. <|tool_call|>" + `{"name":"x"}` + "<|/tool_call|> End.",
			wantComm:   "Go.  End.",
			wantNoMark: true,
		},
		{
			name:       "plain prose untouched",
			input:      "Just a normal reply with no markup.",
			wantComm:   "Just a normal reply with no markup.",
			wantNoMark: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reasoning, commentary := feed(tc.input)
			if commentary != tc.wantComm {
				t.Errorf("commentary = %q, want %q", commentary, tc.wantComm)
			}
			if tc.wantNoMark {
				for _, bad := range []string{openA, closeA, openB, closeB, "tool_call"} {
					if strings.Contains(commentary, bad) {
						t.Errorf("commentary leaked marker %q: %q", bad, commentary)
					}
				}
			}
			_ = reasoning
		})
	}
}

func TestThoughtParser_ThinkStillRoutedToReasoning(t *testing.T) {
	reasoning, commentary := feed("<think>reasoning here</think>visible")
	if strings.TrimSpace(reasoning) != "reasoning here" {
		t.Errorf("reasoning = %q, want %q", reasoning, "reasoning here")
	}
	if commentary != "visible" {
		t.Errorf("commentary = %q, want %q", commentary, "visible")
	}
}
