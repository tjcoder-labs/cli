package tui

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// voiceEnumerateTimeout bounds how long a test waits for the background voice
// enumeration; the native pass compiles a Swift helper, so allow generously.
const voiceEnumerateTimeout = 60 * time.Second

// The inventory must never be produced synchronously: warming only starts a
// goroutine, and reading before it lands returns nil rather than blocking.
func TestWarmVoiceCacheIsNonBlocking(t *testing.T) {
	start := time.Now()
	warmVoiceCache()
	warmVoiceCache() // idempotent; must not start a second enumeration
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("warmVoiceCache blocked for %s", elapsed)
	}
	_ = installedVoices() // must return immediately, possibly nil
	_ = voicesReady()
}

func TestSayVoicesParsesLocales(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("say voices are macOS-only")
	}
	voices := sayVoices()
	if len(voices) == 0 {
		t.Fatal("expected installed say voices")
	}
	byName := map[string]sayVoice{}
	for _, v := range voices {
		byName[v.Name] = v
	}
	if alice, ok := byName["Alice"]; ok && !strings.HasPrefix(alice.Language, "it") {
		t.Errorf("Alice should be an Italian voice, got %q", alice.Language)
	}
	if _, ok := byName["Eddy (English (US))"]; !ok {
		t.Error("multi-word voice name with parentheses was not parsed")
	}
}

func TestInstalledVoicesIncludeSiriVoices(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("voice inventory is macOS-only")
	}
	// Enumeration is background-cached now: warming is non-blocking, so the
	// test must wait for it rather than expect a synchronous result.
	warmVoiceCache()
	select {
	case <-voicesReadyChan():
	case <-time.After(voiceEnumerateTimeout):
		t.Fatal("voice enumeration did not finish")
	}
	voices := installedVoices()
	if len(voices) == 0 {
		t.Fatal("expected a voice inventory")
	}
	// Siri voices are invisible to `say`; if the native enumerator works,
	// at least one Siri voice should be present and carry a real language.
	siri := false
	for _, v := range voices {
		if strings.EqualFold(v.Name, "Nicky") || strings.EqualFold(v.Name, "Nora") {
			siri = true
			if v.Language == "" {
				t.Errorf("%s has no language", v.Name)
			}
		}
		if v.Name == "" {
			t.Error("empty voice name in inventory")
		}
	}
	if !siri {
		t.Skip("no Siri voices installed on this machine")
	}
}
