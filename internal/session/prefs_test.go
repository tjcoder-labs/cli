package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// withXDGConfigHome points $XDG_CONFIG_HOME at a fresh temp dir for
// the duration of the test, then restores the previous value (or
// unsets it) on cleanup. All prefs file I/O is sandboxed under the
// temp dir, so tests can run in parallel and never touch the real
// user's config.
func withXDGConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, had := os.LookupEnv("XDG_CONFIG_HOME")
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		t.Fatalf("set XDG_CONFIG_HOME: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("XDG_CONFIG_HOME", prev)
		} else {
			_ = os.Unsetenv("XDG_CONFIG_HOME")
		}
	})
	return dir
}

func TestGetLastModelMissingReturnsFalse(t *testing.T) {
	withXDGConfigHome(t)
	if m, ok := GetLastModel(true); ok || m != "" {
		t.Fatalf("expected ('', false) on missing file, got (%q, %v)", m, ok)
	}
	if m, ok := GetLastModel(false); ok || m != "" {
		t.Fatalf("expected ('', false) on missing file (headless), got (%q, %v)", m, ok)
	}
}

func TestSetAndGetRoundTrip(t *testing.T) {
	withXDGConfigHome(t)
	if err := SetLastModel(true, "minimax-m3:cloud"); err != nil {
		t.Fatalf("SetLastModel(tui): %v", err)
	}
	if m, ok := GetLastModel(true); !ok || m != "minimax-m3:cloud" {
		t.Fatalf("GetLastModel(tui) = (%q, %v), want minimax-m3:cloud/true", m, ok)
	}
	// The model choice is shared across modes, so a headless read sees
	// the value written from the TUI (and vice versa).
	if m, ok := GetLastModel(false); !ok || m != "minimax-m3:cloud" {
		t.Fatalf("GetLastModel(headless) = (%q, %v), want minimax-m3:cloud/true", m, ok)
	}
}

func TestSlotIsSharedAcrossModes(t *testing.T) {
	withXDGConfigHome(t)
	if err := SetLastModel(true, "shared-model"); err != nil {
		t.Fatalf("set tui: %v", err)
	}
	// Both modes share a single remembered model, so setting it from
	// the TUI is immediately visible to headless runs.
	if m, ok := GetLastModel(false); !ok || m != "shared-model" {
		t.Fatalf("expected shared slot, got (%q, %v)", m, ok)
	}
	// Writing from headless overwrites the same shared slot.
	if err := SetLastModel(false, "shared-model-2"); err != nil {
		t.Fatalf("set headless: %v", err)
	}
	if m, ok := GetLastModel(true); !ok || m != "shared-model-2" {
		t.Fatalf("expected TUI to see shared update, got (%q, %v)", m, ok)
	}
}

func TestEmptySetIsNoop(t *testing.T) {
	withXDGConfigHome(t)
	if err := SetLastModel(true, "keep-this"); err != nil {
		t.Fatalf("initial set: %v", err)
	}
	if err := SetLastModel(true, ""); err != nil {
		t.Fatalf("empty set: %v", err)
	}
	if m, _ := GetLastModel(true); m != "keep-this" {
		t.Fatalf("empty SetLastModel wiped stored value: got %q", m)
	}
}

func TestLastModelOrDefaultFallbackChain(t *testing.T) {
	withXDGConfigHome(t)
	// No recorded value yet: explicit fallback wins.
	if got := LastModelOrDefault(true, "fallback-x"); got != "fallback-x" {
		t.Fatalf("expected fallback-x, got %q", got)
	}
	// No recorded value and no explicit fallback: hard default.
	if got := LastModelOrDefault(false, ""); got != DefaultModel {
		t.Fatalf("expected %q, got %q", DefaultModel, got)
	}
	// Recorded value wins over both fallbacks.
	if err := SetLastModel(true, "minimax-m3:cloud"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := LastModelOrDefault(true, "fallback-x"); got != "minimax-m3:cloud" {
		t.Fatalf("expected stored value to win, got %q", got)
	}
}

func TestPrefsFileIsCreatedInExpectedLocation(t *testing.T) {
	dir := withXDGConfigHome(t)
	if err := SetLastModel(true, "x"); err != nil {
		t.Fatalf("set: %v", err)
	}
	want := filepath.Join(dir, "tjcoder", "coder-cli", "prefs.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected prefs file at %s: %v", want, err)
	}
	// File should be JSON-decodable and contain the stored value.
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read prefs: %v", err)
	}
	var p Prefs
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.LastModel != "x" {
		t.Fatalf("expected LastModel=x, got %q", p.LastModel)
	}
}

func TestCorruptFileFallsBackCleanly(t *testing.T) {
	dir := withXDGConfigHome(t)
	// Write a deliberately broken prefs file at the expected path.
	prefsDir := filepath.Join(dir, "tjcoder", "coder-cli")
	if err := os.MkdirAll(prefsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prefsDir, "prefs.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if m, ok := GetLastModel(true); ok || m != "" {
		t.Fatalf("expected ('', false) on corrupt file, got (%q, %v)", m, ok)
	}
	// And a subsequent Set should overwrite the corrupt file rather
	// than append to it.
	if err := SetLastModel(true, "recovered"); err != nil {
		t.Fatalf("set after corrupt: %v", err)
	}
	if m, _ := GetLastModel(true); m != "recovered" {
		t.Fatalf("expected recovered value, got %q", m)
	}
}

func TestMergePrefsOnlyOverridesNonEmpty(t *testing.T) {
	base := Prefs{LastTUIModel: "a", LastHeadlessModel: "b", Theme: "dark"}
	over := Prefs{LastTUIModel: "x"} // headless/theme empty
	merged := mergePrefs(base, over)
	if !reflect.DeepEqual(merged, Prefs{LastTUIModel: "x", LastHeadlessModel: "b", Theme: "dark"}) {
		t.Fatalf("merge wiped untouched fields: %+v", merged)
	}
}
