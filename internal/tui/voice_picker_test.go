package tui

import (
	"runtime"
	"strings"
	"testing"
)

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
