package tui

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// sayVoice is one installed speech voice with its language and quality.
// macOS exposes two disjoint inventories: `say -v ?` lists the classic
// voices, while AVFoundation additionally knows the Siri voices (Nicky,
// Nora, …) that `say` cannot reach at all. We merge both so the picker
// reflects what the user actually has, and so a Siri voice saved in
// System Settings is visible instead of silently replaced.
type sayVoice struct {
	Name     string
	Language string
	Quality  string
}

// The voice inventory is expensive to build: nativeVoices() shells out to
// `swift`, which may have to compile the AVFoundation helper script first
// (seconds, not milliseconds). Enumerating it inline on the event loop is
// what froze /config, so the work always happens on a background goroutine
// and the UI only ever reads whatever has been cached so far.
var (
	voiceWarmOnce sync.Once             // guards starting the enumeration
	voiceReady    = make(chan struct{}) // closed once the inventory is cached
	voiceListMu   sync.RWMutex          // guards voiceList
	voiceList     []sayVoice
)

// warmVoiceCache starts the background voice enumeration and returns
// immediately. It is safe (and cheap) to call on every /config open: only the
// first call does anything.
func warmVoiceCache() {
	voiceWarmOnce.Do(func() {
		go func() {
			voices := enumerateVoices()
			voiceListMu.Lock()
			voiceList = voices
			voiceListMu.Unlock()
			close(voiceReady)
		}()
	})
}

// installedVoices returns the merged, de-duplicated voice inventory without
// blocking. It is nil until warmVoiceCache's enumeration has finished; use
// voicesReady to tell those two states apart. Callers must treat the slice as
// read-only (it is shared with every other caller).
func installedVoices() []sayVoice {
	voiceListMu.RLock()
	defer voiceListMu.RUnlock()
	return voiceList
}

// voicesReady reports whether the background enumeration has completed, so
// the picker can swap its "(loading voices…)" placeholder for the real list.
func voicesReady() bool {
	select {
	case <-voiceReady:
		return true
	default:
		return false
	}
}

// voicesReadyChan returns a channel that is closed once the background
// enumeration has finished. It lets callers wait for the inventory from a
// goroutine instead of polling or blocking on the event loop.
func voicesReadyChan() <-chan struct{} { return voiceReady }

func enumerateVoices() []sayVoice {
	if runtime.GOOS != "darwin" {
		return nil
	}
	byKey := map[string]sayVoice{}
	add := func(v sayVoice) {
		if v.Name == "" {
			return
		}
		key := strings.ToLower(v.Name) + "|" + strings.ToLower(v.Language)
		if prev, ok := byKey[key]; ok && qualityRank(prev.Quality) >= qualityRank(v.Quality) {
			return
		}
		byKey[key] = v
	}
	for _, v := range sayVoices() {
		add(v)
	}
	for _, v := range nativeVoices() {
		add(v)
	}
	out := make([]sayVoice, 0, len(byKey))
	for _, v := range byKey {
		out = append(out, v)
	}
	return out
}

func qualityRank(q string) int {
	switch q {
	case "premium":
		return 2
	case "enhanced":
		return 1
	}
	return 0
}

// sayVoices parses `say -v ?`, whose lines look like:
//
//	Samantha            en_US    # Hello! My name is Samantha.
//	Eddy (English (US)) en_US    # Hello! My name is Eddy.
func sayVoices() []sayVoice {
	out, err := exec.Command("say", "-v", "?").Output()
	if err != nil {
		return nil
	}
	var voices []sayVoice
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
		// `say -v ?` pads columns, so trim before removing the locale.
		rest := strings.TrimSpace(line[:hash])
		name := strings.TrimSpace(strings.TrimSuffix(rest, locale))
		if name == "" {
			continue
		}
		quality := "default"
		lower := strings.ToLower(name)
		if strings.Contains(lower, "(enhanced)") {
			quality = "enhanced"
		} else if strings.Contains(lower, "(premium)") {
			quality = "premium"
		}
		voices = append(voices, sayVoice{Name: name, Language: locale, Quality: quality})
	}
	return voices
}

// nativeVoices enumerates AVFoundation's inventory, which is the only
// source that includes Siri voices. A failure (no Swift toolchain) simply
// leaves the classic `say` list in place.
func nativeVoices() []sayVoice {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	script, err := nativeSpeechScriptPath(ctx)
	if err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, "swift", script, "--list").Output()
	if err != nil {
		return nil
	}
	var voices []sayVoice
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		voices = append(voices, sayVoice{Name: fields[0], Language: fields[1], Quality: fields[2]})
	}
	return voices
}
