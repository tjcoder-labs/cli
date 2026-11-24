package tui

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVoiceRecognizerHelperHasSignedPrivacyIdentity(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native speech helper is macOS-only")
	}
	for _, tool := range []string{"swiftc", "codesign"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	exe, err := voiceRecognizerExecutable(ctx)
	if err != nil {
		t.Fatalf("voiceRecognizerExecutable: %v", err)
	}
	out, err := exec.CommandContext(ctx, "codesign", "-dvv", exe).CombinedOutput()
	if err != nil {
		t.Fatalf("signed helper: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "Identifier=com.tjcoder.coder.voice-recognizer") {
		t.Errorf("helper has no stable bundle identity: %s", out)
	}
	for _, permission := range []string{"NSMicrophoneUsageDescription", "NSSpeechRecognitionUsageDescription"} {
		if !strings.Contains(voiceRecognizerInfoPlist, permission) {
			t.Errorf("embedded Info.plist lacks %s", permission)
		}
	}
}

func TestVoiceRecognizerRequestsMicrophoneBeforeCapture(t *testing.T) {
	mic := strings.Index(voiceRecognizerSource, "AVCaptureDevice.requestAccess")
	capture := strings.Index(voiceRecognizerSource, "audioEngine.start()")
	ready := strings.Index(voiceRecognizerSource, "READY: microphone active")
	if mic < 0 || capture < 0 || ready < 0 {
		t.Fatal("recognizer is missing permission or capture steps")
	}
	if mic > capture {
		t.Error("microphone permission must be requested before audio capture")
	}
	if ready < capture {
		t.Error("READY must be reported after the audio engine actually starts")
	}
}
