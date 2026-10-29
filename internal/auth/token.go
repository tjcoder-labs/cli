// Package auth manages the ergo platform credential used by the "ergo"
// chat provider. The token is a scoped ergo API key (scope "ergo-cli")
// minted by the device-code login (/login). It is stored in the OS keychain
// when one is available and otherwise in ~/.local/share/coder/ergo-token
// (mode 0600); the ERGO_API_KEY environment variable overrides both.
// Set ERGO_NO_KEYRING=1 to force the file store.
package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	ergoTokenEnv     = "ERGO_API_KEY"
	keyringService   = "tjcoder-ergo-cli"
	keyringUser      = "ergo-token"
	keyringOpTimeout = 3 * time.Second
)

// keyringEnabled is false when ERGO_NO_KEYRING is set (or in headless
// environments where the keyring backend errors out, handled per call).
func keyringEnabled() bool { return strings.TrimSpace(os.Getenv("ERGO_NO_KEYRING")) == "" }

// withTimeout runs fn but gives up after keyringOpTimeout: some Linux
// keyring backends (dbus without a session) can block indefinitely.
func withTimeout(fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(keyringOpTimeout):
		return fmt.Errorf("keyring timed out")
	}
}

func keyringGet() (string, error) {
	var v string
	err := withTimeout(func() (e error) { v, e = keyring.Get(keyringService, keyringUser); return })
	return strings.TrimSpace(v), err
}

func keyringSet(token string) error {
	return withTimeout(func() error { return keyring.Set(keyringService, keyringUser, token) })
}

func keyringDelete() error {
	return withTimeout(func() error { return keyring.Delete(keyringService, keyringUser) })
}

// TokenStore reports where the active token lives: "env", "keychain",
// "file" or "" when signed out. Never returns the token itself.
func TokenStore() string {
	if strings.TrimSpace(os.Getenv(ergoTokenEnv)) != "" {
		return "env"
	}
	if keyringEnabled() {
		if v, err := keyringGet(); err == nil && v != "" {
			return "keychain"
		}
	}
	if data, err := os.ReadFile(ErgoTokenPath()); err == nil && strings.TrimSpace(string(data)) != "" {
		return "file"
	}
	return ""
}

// ErgoTokenPath is where the ergo API key is persisted across sessions
// (mode 0600, per-user). Written only by SaveErgoToken; never logged.
func ErgoTokenPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "coder", "ergo-token")
}

// ResolveErgoToken returns the bearer token from, in order:
//  1. the ERGO_API_KEY environment variable, then
//  2. the OS keychain, then
//  3. the persisted token file (see ErgoTokenPath).
//
// An empty string means "not authenticated".
func ResolveErgoToken() string {
	if t := strings.TrimSpace(os.Getenv(ergoTokenEnv)); t != "" {
		return t
	}
	if keyringEnabled() {
		if v, err := keyringGet(); err == nil && v != "" {
			return v
		}
	}
	data, err := os.ReadFile(ErgoTokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveErgoToken persists the API key: in the OS keychain when available
// (removing any stale file copy), otherwise in a 0600 file inside a 0700
// directory. The raw key is only ever held in memory here; it is never
// printed or logged.
func SaveErgoToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("token must not be empty")
	}
	if keyringEnabled() {
		if err := keyringSet(token); err == nil {
			if p := ErgoTokenPath(); p != "" {
				_ = os.Remove(p)
			}
			return nil
		}
		// keychain unavailable (headless, locked, no dbus…): fall through to the file.
	}
	path := ErgoTokenPath()
	if path == "" {
		return fmt.Errorf("could not resolve token path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	return nil
}

// ClearErgoToken removes the persisted token from the keychain and the
// token file (a no-op when absent).
func ClearErgoToken() error {
	_ = ClearErgoIdentity()
	if keyringEnabled() {
		_ = keyringDelete() // not-found / unavailable is fine
	}
	path := ErgoTokenPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove token: %w", err)
	}
	return nil
}
