package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractAndPrettyJSON(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantOK  bool
	}{
		{
			name:   "raw object",
			input:  `{"answer": 42, "items": ["a", "b"]}`,
			wantOK: true,
		},
		{
			name:   "array",
			input:  `[1, 2, 3]`,
			wantOK: true,
		},
		{
			name:   "fenced json",
			input:  "Here you go:\n```json\n{\"ok\": true}\n```\nDone.",
			wantOK: true,
		},
		{
			name:   "fenced without hint",
			input:  "```\n{\"ok\": true}\n```",
			wantOK: true,
		},
		{
			name:   "prose around object",
			input:  "Sure! The object is {\"ok\": true} as you asked.",
			wantOK: true,
		},
		{
			name:   "nested",
			input:  `{"outer": {"inner": [1, 2, {"deep": "val"}]}}`,
			wantOK: true,
		},
		{
			name:   "no json at all",
			input:  "I'm sorry, I can't help with that.",
			wantOK: false,
		},
		{
			name:   "malformed",
			input:  `{"oops": `,
			wantOK: false,
		},
		{
			name:   "braces in prose only",
			input:  "Use the foo() function to do {stuff}.",
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := extractAndPrettyJSON(c.input)
			if ok != c.wantOK {
				t.Fatalf("ok mismatch: want=%v got=%v (output=%q)", c.wantOK, ok, got)
			}
			if !ok {
				return
			}
			if c.want != "" && got != c.want {
				t.Fatalf("output mismatch: want=%q got=%q", c.want, got)
			}
			// Sanity: pretty output should round-trip.
			var probe any
			if err := json.Unmarshal([]byte(got), &probe); err != nil {
				t.Fatalf("output not valid JSON: %v (output=%q)", err, got)
			}
		})
	}
}

func TestExtractAndPrettyXML(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		wantOK bool
	}{
		{name: "raw element", input: `<root><a>1</a></root>`, wantOK: true},
		{name: "with attrs", input: `<root kind="thing"><a>1</a></root>`, wantOK: true},
		{name: "fenced", input: "```xml\n<root><a>1</a></root>\n```", wantOK: true},
		{name: "prose around it", input: "Here you go: <root><a>1</a></root> thanks.", wantOK: true},
		{name: "self-closing", input: `<root><empty/></root>`, wantOK: true},
		{name: "no xml", input: "I'm sorry, I can't help with that.", wantOK: false},
		{name: "malformed", input: `<root><a>1`, wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := extractAndPrettyXML(c.input)
			if ok != c.wantOK {
				t.Fatalf("ok mismatch: want=%v got=%v (output=%q)", c.wantOK, ok, got)
			}
			if !ok {
				return
			}
			// Pretty output should reparse cleanly and preserve the root element.
			if !strings.Contains(got, "<root") {
				t.Fatalf("pretty output missing root tag: %q", got)
			}
		})
	}
}

func TestResolveFormatFlag(t *testing.T) {
	cases := map[string]string{
		"":      "text",
		"text":  "text",
		"TEXT":  "text",
		" plain": "text",
		"json":  "json",
		"xml":   "xml",
	}
	for in, want := range cases {
		if got := resolveFormatFlag(in); got != want {
			t.Errorf("resolveFormatFlag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatInstructionFor(t *testing.T) {
	if got := formatInstructionFor("text"); got != "" {
		t.Errorf("text should produce no instruction, got %q", got)
	}
	if got := formatInstructionFor("json"); !strings.Contains(got, "JSON") {
		t.Errorf("json instruction should mention JSON, got %q", got)
	}
	if got := formatInstructionFor("xml"); !strings.Contains(got, "XML") {
		t.Errorf("xml instruction should mention XML, got %q", got)
	}
}


