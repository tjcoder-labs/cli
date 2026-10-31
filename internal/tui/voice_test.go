package tui

import (
	"strings"
	"testing"
)

func TestOptimizeForVoice(t *testing.T) {
	a := &App{}
	cases := []struct {
		in, want string
	}{
		{"This is sentence one. This is sentence two. This is sentence three.", "This is sentence one. This is sentence two."},
		{"Only one sentence.", "Only one sentence."},
		{"First.\n\nSecond. Third.", "First. Second."},
		{"", ""},
		{"No period here at all", "No period here at all."},
		{"  spaced.  out.  ", "spaced. out."},
	}
	for _, c := range cases {
		got := a.optimizeForVoice(c.in)
		if got != c.want {
			t.Errorf("optimizeForVoice(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsVoiceEligible(t *testing.T) {
	a := &App{currentModel: "tjcoder/ergo-pro:cloud"}
	if !a.isVoiceEligible() {
		t.Error("expected eligible for tjcoder model")
	}
	a.currentModel = "ollama/llama3"
	if a.isVoiceEligible() {
		t.Error("expected ineligible for non-tjcoder model")
	}
	a.currentModel = "ergo-flash"
	if !a.isVoiceEligible() {
		t.Error("expected eligible for ergo- model")
	}
}

func TestVoiceToggle(t *testing.T) {
	a := &App{currentModel: "tjcoder/ergo-flash:cloud"}
	if a.voice.Enabled {
		t.Fatal("should start disabled")
	}
	a.toggleVoice()
	if !a.voice.Enabled {
		t.Error("toggle should enable")
	}
	a.toggleVoice()
	if a.voice.Enabled {
		t.Error("second toggle should disable")
	}
}

func TestCommentaryBuffer(t *testing.T) {
	a := &App{currentModel: "tjcoder/ergo-pro:cloud", voice: VoiceState{Enabled: true}}
	a.onCommentary("Hello. ")
	a.onCommentary("This is a longer reply with three sentences. Extra.")
	a.voice.mu.Lock()
	buf := a.voice.buf.String()
	a.voice.mu.Unlock()
	if !strings.Contains(buf, "Hello.") || !strings.Contains(buf, "three") {
		t.Errorf("buffer missing text: %q", buf)
	}
	// flush would call speak; we only check optimize path here
	spoken := a.optimizeForVoice(buf)
	if strings.Count(spoken, ".") > 2 {
		t.Errorf("too many sentences after optimize: %q", spoken)
	}
}
