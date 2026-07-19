// Package session also owns a small, *workspace-independent* prefs
// file that stores cross-workspace UI preferences the user has
// expressed at least once. The most important of these is the
// "last used model" per session type: the TUI and the headless
// `coder -p`/`ask` paths each keep their own remembered model so
// that switching between the two (or switching between workspaces)
// does not wipe the user's choice.
//
// Persisted at $XDG_CONFIG_HOME/tjcoder/coder-cli/prefs.json
// (falling back to ~/.config/tjcoder/coder-cli/prefs.json). The
// file is intentionally outside any workspace so it follows the
// user, not the repo.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DefaultModel is the model the TUI / headless paths fall back to
// when neither a per-session-type pref nor an explicit agent
// default is available. It is exported so tests and the README can
// reference the same constant the runtime uses.
const DefaultModel = "minimax-m3:cloud"

// prefsDirName is the directory under the user's config root where
// the prefs file lives. Kept as a constant so tests can construct
// the same path without duplicating the join.
const prefsDirName = "tjcoder/coder-cli"

// prefsFileName is the file inside prefsDirName that holds the
// serialized Prefs document.
const prefsFileName = "prefs.json"

// Prefs is the on-disk shape of the user's cross-workspace UI
// preferences. New fields must be added to Prefs (as omitempty when
// zero values should not appear) and to mergePrefs so a missing
// field in an older file does not wipe a newly-added default.
type Prefs struct {
	// LastModel is the single, shared "most recently selected model"
	// used by both the interactive TUI and the headless
	// `coder -p`/`ask` paths. Selecting a model in either mode records
	// it here so the choice persists across program sessions and
	// across workspaces, regardless of which mode set it.
	LastModel string `json:"last_model,omitempty"`
	// LastTUIModel and LastHeadlessModel are retained only for
	// backward-compatible migration: older prefs files wrote these
	// per-mode slots. They are read as a fallback when LastModel is
	// empty and are no longer written.
	LastTUIModel string `json:"last_tui_model,omitempty"`
	// LastHeadlessModel: see LastTUIModel.
	LastHeadlessModel string `json:"last_headless_model,omitempty"`
	// Theme is reserved for future cross-workspace UI prefs.
	// Not used yet; kept here so the on-disk schema does not
	// shift the first time we add a non-model pref.
	Theme string `json:"theme,omitempty"`
}

// mergePrefs overlays overrides onto base, returning the merged
// view. The two-arg form is what save callers use to make sure a
// partial update (e.g. only flipping LastModel) does not erase
// the other field that the caller did not touch.
func mergePrefs(base, overrides Prefs) Prefs {
	out := base
	if overrides.LastModel != "" {
		out.LastModel = overrides.LastModel
	}
	if overrides.LastTUIModel != "" {
		out.LastTUIModel = overrides.LastTUIModel
	}
	if overrides.LastHeadlessModel != "" {
		out.LastHeadlessModel = overrides.LastHeadlessModel
	}
	if overrides.Theme != "" {
		out.Theme = overrides.Theme
	}
	return out
}

// PrefsPath returns the absolute path of the prefs file. It honors
// $XDG_CONFIG_HOME when set and falls back to ~/.config so the
// file lives in the conventional Linux/macOS location. On Windows
// the same fallback still produces a usable path under %USERPROFILE%.
func PrefsPath() (string, error) {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate config dir: %w", err)
		}
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, prefsDirName, prefsFileName), nil
}

// prefsMu serializes concurrent reads/writes of the prefs file
// from the TUI's runner goroutine and the headless ask path. The
// critical sections are tiny (a few file ops) so contention is
// not a concern in practice.
var prefsMu sync.Mutex

// loadPrefsLocked is the internal loader. Callers must hold prefsMu.
// A missing file is not an error: it returns (zero, false, nil) so
// callers can fall back to the agent's default model without
// surfacing a confusing "no such file" message.
func loadPrefsLocked() (Prefs, bool, error) {
	path, err := PrefsPath()
	if err != nil {
		return Prefs{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Prefs{}, false, nil
		}
		return Prefs{}, false, err
	}
	var p Prefs
	if err := json.Unmarshal(data, &p); err != nil {
		// Treat a corrupt file the same as a missing one: callers
		// fall back to defaults, and the next Save overwrites it.
		return Prefs{}, false, nil
	}
	return p, true, nil
}

// savePrefsLocked is the internal saver. Callers must hold prefsMu.
// It writes atomically (tmp + rename) so a crash mid-write cannot
// leave a half-written file behind.
func savePrefsLocked(p Prefs) error {
	path, err := PrefsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".prefs-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// GetLastModel returns the persisted model. The boolean is false when
// no pref has been recorded yet, so callers know to fall through to
// their next preference source (usually the agent's DefaultModel).
//
// The isTUI argument is retained for API compatibility but no longer
// selects between separate slots: TUI and headless now share a single
// remembered model (Prefs.LastModel). When LastModel is empty, the
// legacy per-mode slots are consulted as a migration fallback.
func GetLastModel(isTUI bool) (string, bool) {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	p, _, err := loadPrefsLocked()
	if err != nil {
		return "", false
	}
	if p.LastModel != "" {
		return p.LastModel, true
	}
	// Migration fallback: honor whichever legacy slot is populated so
	// an existing prefs file keeps working before the first write.
	if p.LastTUIModel != "" {
		return p.LastTUIModel, true
	}
	if p.LastHeadlessModel != "" {
		return p.LastHeadlessModel, true
	}
	return "", false
}

// SetLastModel records model as the user's most recent pick, shared
// across both the TUI and headless modes. Empty model is treated as a
// no-op so callers can pass an uninitialized string without wiping the
// stored value. The isTUI argument is retained for API compatibility.
func SetLastModel(isTUI bool, model string) error {
	if model == "" {
		return nil
	}
	prefsMu.Lock()
	defer prefsMu.Unlock()
	p, _, err := loadPrefsLocked()
	if err != nil {
		// Read failed for some reason other than a missing file
		// (e.g. permission denied). Still try to write a fresh
		// prefs document so the caller's intent is preserved.
		p = Prefs{}
	}
	p.LastModel = model
	return savePrefsLocked(p)
}

// LastModelOrDefault returns the persisted model, falling back to
// fallback when no value has been recorded. It is the one-call helper
// both runtimes use; it guarantees the caller always gets a non-empty
// model.
func LastModelOrDefault(isTUI bool, fallback string) string {
	if m, ok := GetLastModel(isTUI); ok {
		return m
	}
	if fallback != "" {
		return fallback
	}
	return DefaultModel
}
