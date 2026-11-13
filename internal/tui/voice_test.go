package tui

import (
	"strings"
	"testing"

	"github.com/rivo/tview"
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
	if ttsBinary() == "" {
		if a.isVoiceEligible() {
			t.Error("expected ineligible without TTS binary")
		}
		return
	}
	if !a.isVoiceEligible() {
		t.Error("expected eligible with TTS binary present")
	}
}

func TestVoiceToggle(t *testing.T) {
	if ttsBinary() == "" {
		t.Skip("no TTS binary on PATH")
	}
	a := &App{currentModel: "tjcoder/ergo-flash:cloud"}
	// Stub activity so appendActivity doesn't nil-panic on a.TextView.
	a.activity = tview.NewTextView()
	defer func() { a.stopTTS() }()
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
	if ttsBinary() == "" {
		t.Skip("no TTS binary on PATH")
	}
	a := &App{currentModel: "tjcoder/ergo-pro:cloud", voice: VoiceState{Enabled: true}, activity: tview.NewTextView()}
	defer func() { a.stopTTS() }()
	a.onCommentary("Hello. ")
	a.onCommentary("This is a longer reply with three sentences. Extra.")
	a.voice.mu.Lock()
	buf := a.voice.buf.String()
	a.voice.mu.Unlock()
	if !strings.Contains(buf, "Hello.") || !strings.Contains(buf, "three") {
		t.Errorf("buffer missing text: %q", buf)
	}
	spoken := a.optimizeForVoice(buf)
	if strings.Count(spoken, ".") > 2 {
		t.Errorf("too many sentences after optimize: %q", spoken)
	}
}
