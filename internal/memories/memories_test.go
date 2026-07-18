package memories

import (
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/session"
)

func TestFormatPromptBlockIncludesMemories(t *testing.T) {
	state := session.State{
		Memories: []session.Memory{
			{Title: "Prefer concise replies", Body: "Keep answers short and direct."},
			{Title: "Use tests", Body: "Prefer regression tests for behavior changes.", Tags: []string{"eng"}},
		},
	}

	list := Load(state)
	got := FormatPromptBlock(list, 10)
	if !strings.Contains(got, "[memories]") {
		t.Fatalf("expected memories prompt block header, got %q", got)
	}
	if !strings.Contains(got, "Prefer concise replies") {
		t.Fatalf("expected memory title in prompt block, got %q", got)
	}
	if !strings.Contains(got, "Use tests") {
		t.Fatalf("expected second memory title in prompt block, got %q", got)
	}
}
