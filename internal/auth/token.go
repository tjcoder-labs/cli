// Package auth manages the ergo platform credential used by the "ergo"
// chat provider. The token is a scoped ergo API key (scope "ergo-chat")
// minted from the ergo dashboard; the CLI stores it locally at
// ~/.local/share/coder/ergo-token (mode 0600) or reads it from the
// ERGO_API_KEY environment variable.
package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ergoTokenEnv = "ERGO_API_KEY"

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
//  2. the persisted token file (see ErgoTokenPath).
//
// An empty string means "not authenticated".
func ResolveErgoToken() string {
	if t := strings.TrimSpace(os.Getenv(ergoTokenEnv)); t != "" {
		return t
	}
	data, err := os.ReadFile(ErgoTokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveErgoToken persists the API key with 0600 permissions inside a
// 0700 directory. The raw key is only ever held in memory here; it is
// never printed or logged.
func SaveErgoToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("token must not be empty")
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

// ClearErgoToken removes the persisted token file (a no-op when absent).
func ClearErgoToken() error {
	path := ErgoTokenPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove token: %w", err)
	}
	return nil
}
