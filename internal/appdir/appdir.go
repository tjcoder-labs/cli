// Package appdir owns the user-level (not per-workspace) state that
// lives next to prefs.json: the anonymous install id, the update-check
// cache, and the telemetry / update-check / auto-update switches.
package appdir

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Dir returns $XDG_CONFIG_HOME/tjcoder/coder-cli (falling back to
// ~/.config/tjcoder/coder-cli), the same directory as session prefs.
func Dir() (string, error) {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate config dir: %w", err)
		}
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, "tjcoder", "coder-cli"), nil
}

// Settings is the on-disk shape of settings.json.
type Settings struct {
	// InstallID is an anonymous random UUID shared by telemetry and the
	// ergo heartbeat. It is not derived from anything about the machine.
	InstallID string `json:"install_id,omitempty"`
	// Telemetry is the user's explicit choice (`coder telemetry on|off`);
	// nil means "never chosen" (enabled, subject to env gates).
	Telemetry *bool `json:"telemetry,omitempty"`
	// TelemetryNoticeShown records that the one-line first-run notice
	// was displayed.
	TelemetryNoticeShown bool `json:"telemetry_notice_shown,omitempty"`
	// InstallReported records that the one-time install events were sent.
	InstallReported bool `json:"install_reported,omitempty"`
	// UpdateCheck disables the background update check when false.
	UpdateCheck *bool `json:"update_check,omitempty"`
	// AutoUpdate controls whether the TUI installs a newer version in
	// the background (npm and release-binary installs only); nil means
	// "never chosen" (on). CODER_AUTO_UPDATE=0 forces it off.
	AutoUpdate *bool `json:"auto_update,omitempty"`
}

var mu sync.Mutex

func path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "settings.json"), nil
}

// Load reads settings.json. A missing or corrupt file yields zero Settings.
func Load() Settings {
	mu.Lock()
	defer mu.Unlock()
	return loadLocked()
}

func loadLocked() Settings {
	p, err := path()
	if err != nil {
		return Settings{}
	}
	var s Settings
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// Update applies fn to the current settings and writes them atomically.
func Update(fn func(*Settings)) (Settings, error) {
	mu.Lock()
	defer mu.Unlock()
	s := loadLocked()
	fn(&s)
	return s, WriteJSON("settings.json", s)
}

// InstallID returns the anonymous install id, creating it on first use.
// created reports whether this call minted it (i.e. first run).
func InstallID() (id string, created bool) {
	if s := Load(); s.InstallID != "" {
		return s.InstallID, false
	}
	s, err := Update(func(s *Settings) {
		if s.InstallID == "" {
			s.InstallID = NewUUID()
			created = true
		}
	})
	if err != nil {
		return s.InstallID, false
	}
	return s.InstallID, created
}

// NewUUID returns a random RFC 4122 version 4 UUID.
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// ReadJSON decodes name (relative to Dir) into v.
func ReadJSON(name string, v any) error {
	d, err := Dir()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(d, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// WriteJSON atomically writes v as name (relative to Dir), mode 0600.
func WriteJSON(name string, v any) error {
	d, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d, "."+name+"-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(d, name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// InCI reports whether a common CI environment variable is set.
func InCI() bool {
	for _, k := range []string{"CI", "CONTINUOUS_INTEGRATION", "BUILD_NUMBER", "RUN_ID", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TRAVIS", "JENKINS_URL", "TEAMCITY_VERSION", "TF_BUILD", "CODEBUILD_BUILD_ID"} {
		if v := os.Getenv(k); v != "" && v != "0" && v != "false" {
			return true
		}
	}
	return false
}
