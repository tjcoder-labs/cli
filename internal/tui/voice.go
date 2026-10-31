package tui

import (
	"os/exec"
	"strings"
	"sync"
	"time"
)

// VoiceState holds telephone-style speech mode for tjcoder models only.
// Barge-in aborts TTS and speaks a brief "Go ahead".
// Commentary is optimized to 1-2 short sentences.
type VoiceState struct {
	Enabled   bool
	Listening bool
	Speaking  bool
	BargeIn   bool
	buf       strings.Builder
	mu        sync.Mutex
	ttsCmd    *exec.Cmd
}

func (a *App) isVoiceEligible() bool {
	model := a.currentModel
	return strings.HasPrefix(model, "tjcoder/") || strings.Contains(model, "ergo-")
}

func (a *App) toggleVoice() {
	if !a.isVoiceEligible() {
		a.appendActivity("Voice mode only available for tjcoder models")
		a.speakBrief("Voice mode on. Say something after I finish.")
		return
	}
	a.voice.Enabled = !a.voice.Enabled
	state := "off"
	if a.voice.Enabled {
		state = "on"
		a.appendActivity("Voice mode on — short spoken replies, barge-in says \"Go ahead\"")
		a.speakBrief("Voice mode on. Say something after I finish.")
	} else {
		a.stopTTS()
		a.appendActivity("Voice mode off")
	}
	a.appendActivity("Voice " + state)
}

func (a *App) onCommentary(text string) {
	if !a.voice.Enabled || !a.isVoiceEligible() {
		return
	}
	a.voice.mu.Lock()
	a.voice.buf.WriteString(text)
	a.voice.mu.Unlock()
}

func (a *App) flushVoiceSpeech() {
	if !a.voice.Enabled || !a.isVoiceEligible() {
		return
	}
	a.voice.mu.Lock()
	raw := a.voice.buf.String()
	a.voice.buf.Reset()
	a.voice.mu.Unlock()
	if strings.TrimSpace(raw) == "" {
		return
	}
	spoken := a.optimizeForVoice(raw)
	a.speak(spoken)
}

func (a *App) optimizeForVoice(completion string) string {
	// Strip markdown-ish, take first 1-2 sentences.
	s := strings.ReplaceAll(completion, "\n", " ")
	s = strings.TrimSpace(s)
	sentences := strings.Split(s, ".")
	var out []string
	for _, sent := range sentences {
		sent = strings.TrimSpace(sent)
		if sent != "" && len(out) < 2 {
			out = append(out, sent)
		}
	}
	if len(out) == 0 {
		return s
	}
	return strings.Join(out, ". ") + "."
}

func (a *App) speak(text string) {
	a.stopTTS()
	a.voice.Speaking = true
	go func() {
		// Prefer macOS say, fall back to espeak.
		cmd := exec.Command("say", text)
		if _, err := exec.LookPath("say"); err != nil {
			cmd = exec.Command("espeak", text)
		}
		a.voice.ttsCmd = cmd
		_ = cmd.Run()
		a.voice.Speaking = false
		a.voice.ttsCmd = nil
	}()
}

func (a *App) speakBrief(text string) {
	a.speak(text)
}

func (a *App) stopTTS() {
	if a.voice.ttsCmd != nil && a.voice.ttsCmd.Process != nil {
		_ = a.voice.ttsCmd.Process.Kill()
	}
	a.voice.Speaking = false
}

func (a *App) onBargeIn() {
	if !a.voice.Enabled || !a.voice.Speaking {
		return
	}
	a.voice.BargeIn = true
	a.stopTTS()
	a.speakBrief("Go ahead")
	a.appendActivity("Barge-in: Go ahead")
	// Resume listening (VAD hook goes here).
}
