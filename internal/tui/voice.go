package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// VoiceState holds spoken replies. A separate synthesis call produces a
// meeting-style summary; the full response remains in the transcript.
// Barge-in (Esc while speaking) aborts TTS and speaks a brief "Go ahead".
// Commentary is optimized to 1-2 short sentences.
//
// All exported fields are guarded by mu. The TTS goroutine writes state
// only while holding mu; the UI goroutine does the same. Never read
// Speaking/Listening/ttsCmd without the lock.
type VoiceState struct {
	Enabled      bool
	Listening    bool
	Speaking     bool
	BargeIn      bool
	buf          strings.Builder
	mu           sync.Mutex
	ttsCmd       *exec.Cmd
	listenCancel context.CancelFunc
}

func (a *App) isVoiceEligible() bool {
	// Voice works for any provider; only a TTS backend is required.
	if runtime.GOOS == "darwin" {
		return true // native AVFoundation synthesizer is always available
	}
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
		a.appendActivity("Voice output on; /voice listen starts microphone transcription")
		a.speakBrief("Voice mode on. Say voice listen to dictate your next message.")
	} else {
		a.stopVoiceInput(false)
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
	// Keep speech asynchronous and never delay transcript rendering.
	go a.speakSynthesized(raw)
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

// nativeSpeechScript speaks one utterance, or lists installed voices when
// invoked with "--list", through AVFoundation. Siri voices (Nicky, Nora, …)
// are invisible to `say`, so this is the only way to use the voice the user
// actually configured in System Settings. An empty voice name means the
// system default.
const nativeSpeechScript = `import AVFoundation
setvbuf(stdout, nil, _IONBF, 0)
let args = CommandLine.arguments
let wanted = args.count > 1 ? args[1] : ""
let text = args.count > 2 ? args[2] : ""
if wanted == "--list" {
    for v in AVSpeechSynthesisVoice.speechVoices() {
        let q = v.quality == .premium ? "premium" : (v.quality == .enhanced ? "enhanced" : "default")
        print("\(v.name)\t\(v.language)\t\(q)")
    }
    exit(0)
}
if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { exit(0) }
let voices = AVSpeechSynthesisVoice.speechVoices()
var chosen: AVSpeechSynthesisVoice?
if !wanted.isEmpty {
    let needle = wanted.lowercased().replacingOccurrences(of: " (enhanced)", with: "").replacingOccurrences(of: " (premium)", with: "")
    chosen = voices.first { $0.name.lowercased() == needle }
    if chosen == nil { chosen = voices.first { $0.name.lowercased() == wanted.lowercased() } }
    if chosen == nil { fputs("ERROR: voice \(wanted) not found\n", stderr); exit(3) }
}
let synth = AVSpeechSynthesizer()
let utterance = AVSpeechUtterance(string: text)
if let voice = chosen { utterance.voice = voice }
synth.speak(utterance)
while synth.isSpeaking { RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05)) }
`

// nativeSpeechScriptPath materializes the native helper and returns its path.
func nativeSpeechScriptPath(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "tjcoder", "voice")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "speaker.swift")
	if current, err := os.ReadFile(path); err != nil || string(current) != nativeSpeechScript {
		if err := os.WriteFile(path, []byte(nativeSpeechScript), 0o600); err != nil {
			return "", err
		}
	}
	return path, nil
}

// sayKnowsVoice reports whether `say -v <name>` accepts the configured voice.
func sayKnowsVoice(name string) bool {
	if name == "" {
		return true
	}
	clean := strings.TrimSpace(name)
	for _, suffix := range []string{"(Enhanced)", "(Premium)"} {
		clean = strings.TrimSpace(strings.TrimSuffix(clean, suffix))
	}
	needle := strings.ToLower(clean)
	if out, err := exec.Command("say", "-v", "?").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			hash := strings.Index(line, "#")
			if hash < 0 {
				continue
			}
			fields := strings.Fields(line[:hash])
			if len(fields) < 2 {
				continue
			}
			locale := fields[len(fields)-1]
			spoken := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(line[:hash], locale)))
			if spoken == needle {
				return true
			}
		}
	}
	return false
}

func (a *App) speak(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	voiceName := strings.TrimSpace(a.config.VoiceName)
	// Siri voices (Nicky, Nora, …) exist only in AVFoundation, and an empty
	// name should use the System Settings voice. Both cases need the native
	// synthesizer; `say` would silently substitute a different speaker.
	if runtime.GOOS == "darwin" && (voiceName == "" || !sayKnowsVoice(voiceName)) {
		a.speakViaNative(text, voiceName)
		return
	}
	bin := ttsBinary()
	if bin == "" {
		return
	}
	a.stopTTS()

	args := []string{}
	if voiceName != "" {
		args = append(args, "-v", voiceName)
	}
	args = append(args, text)
	cmd := exec.Command(bin, args...)
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

// speakViaNative runs the AVFoundation speaker so the exact configured voice
// is used. It registers its process for barge-in cancellation like `say`.
func (a *App) speakViaNative(text, voiceName string) {
	a.stopTTS()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	script, err := nativeSpeechScriptPath(ctx)
	if err != nil {
		cancel()
		return
	}
	cmd := exec.CommandContext(ctx, "swift", script, voiceName, text)
	a.voice.mu.Lock()
	a.voice.ttsCmd = cmd
	a.voice.Speaking = true
	a.voice.mu.Unlock()
	go func() {
		defer cancel()
		if out, err := cmd.CombinedOutput(); err != nil {
			msg := strings.TrimSpace(string(out))
			if msg != "" {
				a.postUI(func() { a.appendActivity("Voice output failed: " + firstLine(msg)) })
			}
		}
		time.Sleep(50 * time.Millisecond)
		a.voice.mu.Lock()
		if a.voice.ttsCmd == cmd {
			a.voice.ttsCmd = nil
			a.voice.Speaking = false
		}
		a.voice.mu.Unlock()
	}()
}

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
