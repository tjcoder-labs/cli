package tui

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// VoiceState holds telephone-style speech mode for tjcoder models only.
// Barge-in (Esc while speaking) aborts TTS and speaks a brief "Go ahead".
// Commentary is optimized to 1-2 short sentences.
//
// All exported fields are guarded by mu. The TTS goroutine writes state
// only while holding mu; the UI goroutine does the same. Never read
// Speaking/Listening/ttsCmd without the lock.
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
	// Voice works for any provider; only a TTS binary is required.
	return ttsBinary() != ""
}

// ttsBinary returns the system TTS binary name ("say" on Darwin,
// "espeak" elsewhere) or "" if none is installed.
func ttsBinary() string {
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("say"); err == nil {
			return "say"
		}
		return ""
	}
	if _, err := exec.LookPath("espeak"); err == nil {
		return "espeak"
	}
	if _, err := exec.LookPath("say"); err == nil {
		return "say"
	}
	return ""
}

func (a *App) toggleVoice() {
	if !a.isVoiceEligible() {
		a.appendActivity("Voice unavailable: no TTS binary (say/espeak) found on PATH")
		return
	}
	a.voice.mu.Lock()
	a.voice.Enabled = !a.voice.Enabled
	enabled := a.voice.Enabled
	a.voice.mu.Unlock()

	if enabled {
		a.appendActivity("Voice mode on - short spoken replies, Esc barge-in says \"Go ahead\"")
		a.speakBrief("Voice mode on. Say something after I finish.")
	} else {
		a.stopTTS()
		a.appendActivity("Voice mode off")
	}
}

func (a *App) onCommentary(text string) {
	if !a.isVoiceEligible() {
		return
	}
	a.voice.mu.Lock()
	defer a.voice.mu.Unlock()
	if !a.voice.Enabled {
		return
	}
	a.voice.buf.WriteString(text)
}

func (a *App) flushVoiceSpeech() {
	if !a.isVoiceEligible() {
		return
	}
	a.voice.mu.Lock()
	if !a.voice.Enabled {
		a.voice.mu.Unlock()
		return
	}
	raw := a.voice.buf.String()
	a.voice.buf.Reset()
	a.voice.mu.Unlock()
	if strings.TrimSpace(raw) == "" {
		return
	}
	a.speak(a.optimizeForVoice(raw))
}

func (a *App) optimizeForVoice(completion string) string {
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
	bin := ttsBinary()
	if bin == "" || strings.TrimSpace(text) == "" {
		return
	}
	a.stopTTS()

	cmd := exec.Command(bin, text)
	a.voice.mu.Lock()
	a.voice.ttsCmd = cmd
	a.voice.Speaking = true
	a.voice.mu.Unlock()

	go func() {
		_ = cmd.Run()
		// Brief debounce so rapid flush cycles don't cut each other off.
		time.Sleep(50 * time.Millisecond)
		a.voice.mu.Lock()
		if a.voice.ttsCmd == cmd {
			a.voice.ttsCmd = nil
			a.voice.Speaking = false
		}
		a.voice.mu.Unlock()
	}()
}

func (a *App) speakBrief(text string) { a.speak(text) }

func (a *App) stopTTS() {
	a.voice.mu.Lock()
	cmd := a.voice.ttsCmd
	a.voice.ttsCmd = nil
	a.voice.Speaking = false
	a.voice.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (a *App) onBargeIn() {
	a.voice.mu.Lock()
	enabled := a.voice.Enabled
	speaking := a.voice.Speaking
	a.voice.mu.Unlock()
	if !enabled || !speaking {
		return
	}
	a.stopTTS()
	a.speakBrief("Go ahead")
	a.appendActivity("Barge-in: Go ahead")
}
