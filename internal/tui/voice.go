package tui

import (
	"strings"
	"time"
)

// VoiceMode enables telephone-style speech for tjcoder provider models only.
// Barge-in detection aborts TTS and speaks a brief "Go ahead".
// Responses are optimized to 1-2 short sentences.

type VoiceState struct {
	Enabled   bool
	Listening bool
	Speaking  bool
	BargeIn   bool
}

func (a *App) isVoiceEligible() bool {
	// Only tjcoder provider models (tjcoder/ergo-* or similar).
	model := a.currentModel()
	return strings.HasPrefix(model, "tjcoder/") || strings.Contains(model, "ergo-")
}

func (a *App) onBargeIn() {
	if !a.isVoiceEligible() || !a.voice.Speaking {
		return
	}
	a.voice.BargeIn = true
	a.voice.Speaking = false
	// Force speech engine to briefly say "Go ahead".
	a.speakBrief("Go ahead")
	// Then resume listening with ample silence timeout.
	a.startListening(2 * time.Second)
}

func (a *App) speakBrief(text string) {
	// Local TTS call (piper/espeak/say) — implement with exec or cgo.
	// Keep under 1 second.
}

func (a *App) optimizeForVoice(completion string) string {
	// Post-process LLM completion: split to 1-2 sentences, natural speech.
	// Strip markdown, tool call noise; weave commentary as spoken asides.
	sentences := strings.Split(completion, ".")
	if len(sentences) > 2 {
		return strings.Join(sentences[:2], ".") + "."
	}
	return completion
}

// TODO: integrate VAD/STT (local whisper), continuous loop, gateway LLM call with voice system prompt.
