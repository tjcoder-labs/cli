package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const voiceRecognizerInfoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.tjcoder.coder.voice-recognizer</string>
<key>CFBundleExecutable</key><string>recognizer</string>
<key>CFBundleName</key><string>Coder Voice Recognizer</string>
<key>CFBundlePackageType</key><string>XPC!</string>
<key>NSMicrophoneUsageDescription</key><string>Coder uses the microphone to transcribe speech when you run /voice listen.</string>
<key>NSSpeechRecognitionUsageDescription</key><string>Coder transcribes speech into the input field only when you request voice input.</string>
</dict></plist>`

const voiceRecognizerSource = `import Foundation
import AVFoundation
import Speech

func fail(_ message: String, _ code: Int32) -> Never {
    fputs("ERROR: " + message + "\n", stderr)
    exit(code)
}
let timeoutSeconds = CommandLine.arguments.count > 1 ? Double(CommandLine.arguments[1]) ?? 30 : 30
let localeID = CommandLine.arguments.count > 2 ? CommandLine.arguments[2] : "en-US"
guard let recognizer = SFSpeechRecognizer(locale: Locale(identifier: localeID)) else {
    fail("speech recognition is unavailable for \(localeID)", 2)
}
let micAuth = DispatchSemaphore(value: 0)
var micAuthorized = false
if #available(macOS 10.14, *) {
    switch AVCaptureDevice.authorizationStatus(for: .audio) {
    case .authorized:
        micAuthorized = true
    case .notDetermined:
        AVCaptureDevice.requestAccess(for: .audio) { granted in
            micAuthorized = granted
            micAuth.signal()
        }
        if micAuth.wait(timeout: .now() + 30) == .timedOut {
            fail("microphone permission prompt timed out; check System Settings > Privacy & Security > Microphone for your terminal", 6)
        }
    default:
        fail("microphone permission denied; enable your terminal in System Settings > Privacy & Security > Microphone", 6)
    }
}
guard micAuthorized else {
    fail("microphone permission denied; enable your terminal in System Settings > Privacy & Security > Microphone", 6)
}
let auth = DispatchSemaphore(value: 0)
var authorizationReceived = false
var speechAuthorized = false
SFSpeechRecognizer.requestAuthorization { status in
    speechAuthorized = status == .authorized
    authorizationReceived = true
    auth.signal()
}
_ = auth.wait(timeout: .now() + 30)
guard authorizationReceived else {
    fail("speech recognition permission prompt timed out; enable your terminal in System Settings > Privacy & Security > Speech Recognition", 3)
}
guard speechAuthorized else {
    fail("speech recognition permission denied; enable your terminal in System Settings > Privacy & Security > Speech Recognition", 3)
}
guard recognizer.isAvailable else { fail("speech recognizer is unavailable", 4) }
if recognizer.supportsOnDeviceRecognition {
    fputs("Speech service: on-device recognition available\n", stderr)
} else {
    fputs("Speech service: macOS speech recognition service\n", stderr)
}
fflush(stderr)
let audioEngine = AVAudioEngine()
let request = SFSpeechAudioBufferRecognitionRequest()
request.shouldReportPartialResults = true
// Prefer on-device recognition when its language assets are installed;
// otherwise macOS uses its authorized speech service.
let input = audioEngine.inputNode
let format = input.outputFormat(forBus: 0)
var audioBufferCount = 0
var peakAudioLevel: Float = 0
input.installTap(onBus: 0, bufferSize: 2048, format: format) { buffer, _ in
    audioBufferCount += 1
    if let data = buffer.floatChannelData {
        let frames = Int(buffer.frameLength)
        for channel in 0..<Int(buffer.format.channelCount) {
            for frame in 0..<frames { peakAudioLevel = max(peakAudioLevel, abs(data[channel][frame])) }
        }
    }
    request.append(buffer)
}
var last = ""
var finished = false
let done = DispatchSemaphore(value: 0)
var task: SFSpeechRecognitionTask?
var recognitionError: Error?
func finish() {
    if finished { return }
    finished = true
    request.endAudio()
    if audioEngine.isRunning { audioEngine.stop(); input.removeTap(onBus: 0) }
    if let error = recognitionError {
        fputs("ERROR: speech recognition failed: \(error.localizedDescription)\n", stderr)
    }
    print(last)
    done.signal()
}
DispatchQueue.main.asyncAfter(deadline: .now() + timeoutSeconds) { finish() }
task = recognizer.recognitionTask(with: request) { result, error in
    if let result = result { last = result.bestTranscription.formattedString }
    if let error = error { recognitionError = error }
    if result?.isFinal == true || error != nil { finish() }
}
do {
    audioEngine.prepare()
    try audioEngine.start()
    fputs("READY: microphone active\n", stderr)
    fflush(stderr)
} catch { fail("microphone failed: \(error.localizedDescription)", 5) }
_ = done.wait(timeout: .now() + timeoutSeconds + 5)
task?.cancel()
if !finished {
    audioEngine.stop()
    input.removeTap(onBus: 0)
    print(last)
}
fputs("Audio: \(audioBufferCount) buffers, peak level \(peakAudioLevel)\n", stderr)
if audioBufferCount == 0 || peakAudioLevel == 0 {
    fputs("ERROR: no microphone audio detected; check the input device and its level in System Settings > Sound > Input\n", stderr)
    exit(8)
}
exit(recognitionError == nil ? 0 : 7)
`

func (a *App) handleVoiceCommand(parts []string) {
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "listen", "input", "mic":
		a.startVoiceInput()
	case "stop":
		a.stopVoiceInput(true)
	default:
		a.toggleVoice()
	}
}

func (a *App) startVoiceInput() {
	if runtime.GOOS != "darwin" {
		a.appendActivity("Voice input currently requires macOS speech recognition")
		return
	}
	if _, err := exec.LookPath("swift"); err != nil {
		a.appendActivity("Voice input requires Swift (install Xcode or Command Line Tools)")
		return
	}
	a.voice.mu.Lock()
	if a.voice.Listening {
		a.voice.mu.Unlock()
		a.appendActivity("Already listening; use /voice stop to finish")
		return
	}
	a.voice.Listening = true
	a.voice.mu.Unlock()
	a.stopTTS()
	a.setVoicePlaceholder()
	a.appendActivity("Listening for up to 30 seconds; transcribed text will appear in the input field")
	go a.captureVoiceInput()
}

func (a *App) stopVoiceInput(log bool) {
	a.voice.mu.Lock()
	listening := a.voice.Listening
	cancel := a.voice.listenCancel
	a.voice.listenCancel = nil
	a.voice.Listening = false
	a.voice.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if listening {
		if log {
			a.appendActivity("Voice input stopping; final transcript will appear shortly")
		}
		a.postUI(func() { a.setVoicePlaceholder() })
	}
}

func (a *App) setVoicePlaceholder() {
	if a.input == nil {
		return
	}
	a.voice.mu.Lock()
	listening := a.voice.Listening
	a.voice.mu.Unlock()
	if listening {
		a.input.SetPlaceholder("Listening… speak now; use /voice stop to finish")
	} else {
		a.setInputPlaceholder()
	}
}

func (a *App) captureVoiceInput() {
	defer a.stopVoiceInput(false)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	a.voice.mu.Lock()
	a.voice.listenCancel = cancel
	a.voice.mu.Unlock()
	defer cancel()
	exe, err := voiceRecognizerExecutable(ctx)
	if err != nil {
		a.postUI(func() { a.appendActivity("Voice input unavailable: " + err.Error()) })
		return
	}
	cmd := exec.CommandContext(ctx, exe, "30")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	text := strings.TrimSpace(stdout.String())
	diagnostic := strings.TrimSpace(stderr.String())
	if err != nil {
		if text != "" {
			diagnostic += "\n" + text
		}
		a.postUI(func() { a.appendActivity("Voice input failed: " + firstLine(diagnostic)) })
		return
	}
	if text != "" {
		a.postUI(func() {
			a.input.SetText(text)
			a.tv.SetFocus(a.input)
			a.appendActivity("Transcribed speech into the input field; review and press Enter")
		})
	} else {
		a.postUI(func() {
			a.appendActivity("Voice input finished without a transcript; check microphone input and try again")
		})
	}
}

// voiceRecognizerExecutable compiles the small native helper on demand.
// macOS provides both the compiler and the speech frameworks, so this
// avoids shipping a platform-specific binary or adding Go dependencies.
func voiceRecognizerExecutable(ctx context.Context) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "tjcoder", "voice")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	src := filepath.Join(dir, "recognizer.swift")
	plist := filepath.Join(dir, "Info.plist")
	exe := filepath.Join(dir, "recognizer")
	current, sourceErr := os.ReadFile(src)
	currentPlist, plistErr := os.ReadFile(plist)
	if sourceErr != nil || string(current) != voiceRecognizerSource || plistErr != nil || string(currentPlist) != voiceRecognizerInfoPlist {
		if err := os.WriteFile(src, []byte(voiceRecognizerSource), 0o600); err != nil {
			return "", err
		}
		if err := os.WriteFile(plist, []byte(voiceRecognizerInfoPlist), 0o600); err != nil {
			return "", err
		}
		cmd := exec.CommandContext(ctx, "swiftc", src, "-o", exe,
			"-Xlinker", "-sectcreate", "-Xlinker", "__TEXT", "-Xlinker", "__info_plist", "-Xlinker", plist)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("native helper build failed: %s", firstLine(string(out)))
		}
		// Embed Info.plist in the executable so macOS 13 can attribute
		// microphone and speech access without an application bundle.
		// The responsible terminal may own the privacy grant, as expected
		// for a CLI, while the ad-hoc signature replaces InvalidCode.
		cmd = exec.CommandContext(ctx, "codesign", "--force", "--sign", "-", "--identifier", "com.tjcoder.coder.voice-recognizer", exe)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("native helper signing failed: %s", firstLine(string(out)))
		}
	}
	return exe, nil
}
