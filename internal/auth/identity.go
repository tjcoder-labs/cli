package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Identity is the non-secret profile of the signed-in ergo account,
// captured from the device-login result. It is used only for local UI and
// per-turn metadata; it never contains the API key.
type Identity struct {
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
}

// ErgoIdentityPath sits next to the token file (see ErgoTokenPath).
func ErgoIdentityPath() string {
	p := ErgoTokenPath()
	if p == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "ergo-identity.json")
}

// SaveErgoIdentity persists id (mode 0600). An empty identity removes the
// file so a stale email is never reported.
func SaveErgoIdentity(id Identity) error {
	path := ErgoIdentityPath()
	if path == "" {
		return nil
	}
	id.Email = strings.TrimSpace(id.Email)
	id.Name = strings.TrimSpace(id.Name)
	if id.Email == "" && id.Name == "" {
		return ClearErgoIdentity()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(id)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadErgoIdentity returns the saved identity; ok is false when none is
// stored (signed out, or signed in before identities were recorded).
func LoadErgoIdentity() (Identity, bool) {
	path := ErgoIdentityPath()
	if path == "" {
		return Identity{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, false
	}
	var id Identity
	if json.Unmarshal(data, &id) != nil || (id.Email == "" && id.Name == "") {
		return Identity{}, false
	}
	return id, true
}

// ClearErgoIdentity removes the saved identity (no-op when absent).
func ClearErgoIdentity() error {
	path := ErgoIdentityPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
