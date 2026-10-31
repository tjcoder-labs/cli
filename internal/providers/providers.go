// Package providers owns the cross-workspace provider registry: the
// OpenAI-compatible gateways (host + credentials + auth flow) the CLI can
// route chat through. Unlike the per-workspace session state, this is global
// — it follows the user, not the repo — so a provider configured in one
// workspace is available everywhere.
//
// Persisted at $XDG_CONFIG_HOME/tjcoder/coder-cli/providers.json
// (falling back to ~/.config/tjcoder/coder-cli/providers.json).
//
// This package is deliberately a leaf (stdlib-only) so the client package can
// import it without creating an import cycle (client → providers, but never
// providers → client).
package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// prefsDirName mirrors session.prefsDirName so the registry lives next to
// prefs.json in the same user-level config directory.
const prefsDirName = "tjcoder/coder-cli"

// FileName is the registry file name.
const FileName = "providers.json"

// AuthFlow describes how /login <provider> begins authentication for a
// provider. Empty ("") means the provider needs no credentials.
type AuthFlow string

const (
	// AuthNone means the provider is unauthenticated (e.g. a local endpoint).
	AuthNone AuthFlow = ""
	// AuthDeviceCode uses the ergo gateway's device-code flow
	// (POST /cli/auth/start → /cli/auth/poll).
	AuthDeviceCode AuthFlow = "device_code"
	// AuthAPIKey prompts for a plain API key (sent as a Bearer token).
	AuthAPIKey AuthFlow = "api_key"
)

// Config describes one OpenAI-compatible gateway. Every field maps onto what
// the client package needs to build a live client: a base URL (scheme + host;
// the client appends /v1), an optional Bearer API key, and an auth flow for
// /login.
type Config struct {
	// Name is the stable identifier ("ergo-gateway", "ollama", "my-vllm").
	// It is the key in the registry and the value passed to --provider.
	Name string `json:"name"`
	// BaseURL is the scheme + host (no trailing "/v1"). The client appends
	// /v1/models and /v1/chat/completions.
	BaseURL string `json:"base_url"`
	// APIKey is the Bearer token. It is persisted only when Auth is
	// AuthAPIKey; for device-code providers the key lives in the keychain
	// and this field stays empty.
	APIKey string `json:"api_key,omitempty"`
	// Auth is how /login <provider> begins authentication.
	Auth AuthFlow `json:"auth,omitempty"`
	// Protocol selects the wire protocol the client speaks. "" (auto)
	// infers it from Name (ergo*/ollama* vs. everything else); "openai"
	// forces the OpenAI-compatible client (/v1/...); "ollama" forces the
	// Ollama client (/api/...). This lets a non-Ollama-named endpoint that
	// happens to serve the Ollama API still route to the right client.
	Protocol string `json:"protocol,omitempty"`
	// APIPath is the Ollama API prefix when Protocol is "ollama" and the
	// endpoint does not serve it at /api (e.g. "/ollama/api" on the ergo
	// gateway). "" = /api.
	APIPath string `json:"api_path,omitempty"`
	// Default marks the provider selected on launch when no explicit
	// --provider is given. Exactly one provider should have this set.
	Default bool `json:"default,omitempty"`
}

// ProtocolOpenAI and ProtocolOllama are the two concrete wire protocols a
// gateway can speak. "" (auto) is resolved by ResolveProtocol.
const (
	ProtocolOpenAI = "openai"
	ProtocolOllama = "ollama"
)

// ResolveProtocol returns the effective wire protocol for a config. An
// explicit Protocol wins; otherwise the name is inspected: "ollama" and
// "ollama-cloud" (and their aliases) are the Ollama API, everything else is
// assumed OpenAI-compatible. The ergo gateway is handled separately by the
// client factory: it defaults to OpenAI and honors an explicit "ollama".
func (p Config) ResolveProtocol() string {
	switch strings.ToLower(strings.TrimSpace(p.Protocol)) {
	case ProtocolOllama:
		return ProtocolOllama
	case ProtocolOpenAI:
		return ProtocolOpenAI
	}
	name := strings.ToLower(strings.TrimSpace(p.Name))
	switch name {
	case "ollama", "ollama-cloud", "ollama_cloud", "ollama-cloud-api":
		return ProtocolOllama
	default:
		return ProtocolOpenAI
	}
}

// Valid reports whether the config has the minimum required fields.
func (p Config) Valid() bool {
	return p.Name != "" && p.BaseURL != ""
}

// Registry is the ordered on-disk list of providers.
type Registry struct {
	Providers []Config `json:"providers"`
	// Active is the name of the currently selected provider.
	Active string `json:"active,omitempty"`
}

// Defaults returns the built-in registry: ergo-gateway (default), a local
// ollama, and the hosted Ollama cloud API. They are removable — the user
// may delete any of them — but they seed a first-run install.
func Defaults() Registry {
	return Registry{
		Active: "tjcoder",
		Providers: []Config{
			{
				Name:    "tjcoder",
				BaseURL: "https://api.tjcoder.com",
				Auth:    AuthDeviceCode,
				Default: true,
			},
			{
				Name:    "ollama",
				BaseURL: "http://localhost:11434",
				Auth:    AuthNone,
			},
			{
				// Hosted Ollama cloud API (https://ollama.com).
				// Requires an OLLAMA_API_KEY; use Login (API key) to store it.
				Name:    "ollama-cloud",
				BaseURL: "https://ollama.com",
				Auth:    AuthAPIKey,
			},
		},
	}
}

// mu serializes access to the registry file.
var mu sync.Mutex

// path returns the absolute path of the registry file, honoring
// $XDG_CONFIG_HOME and falling back to ~/.config.
func path() (string, error) {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate config dir: %w", err)
		}
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, prefsDirName, FileName), nil
}

// Load reads the registry, seeding defaults when the file is missing or
// corrupt. It never returns an error: a first-run or unreadable file yields
// the built-in defaults so the CLI still works.
func Load() Registry {
	mu.Lock()
	defer mu.Unlock()
	p, err := path()
	if err != nil {
		return Defaults()
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Defaults()
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return Defaults()
	}
	if len(reg.Providers) == 0 {
		return Defaults()
	}
	return reg
}

// Save writes the registry atomically (tmp + rename). It enforces the
// invariant that Active names an existing provider (preferring a Default
// flag, else the first provider).
func Save(reg Registry) error {
	mu.Lock()
	defer mu.Unlock()

	if len(reg.Providers) == 0 {
		return errors.New("cannot save an empty provider registry")
	}

	found := false
	for i := range reg.Providers {
		if reg.Providers[i].Name == reg.Active {
			found = true
			reg.Providers[i].Default = true
		} else {
			reg.Providers[i].Default = false
		}
	}
	if !found {
		reg.Active = reg.Providers[0].Name
		reg.Providers[0].Default = true
	}

	sort.SliceStable(reg.Providers, func(i, j int) bool {
		return reg.Providers[i].Name < reg.Providers[j].Name
	})

	p, err := path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".providers-*.json.tmp")
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
	return os.Rename(tmpName, p)
}

// Get returns the provider named name, or ok=false.
func Get(name string) (Config, bool) {
	reg := Load()
	for _, p := range reg.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Config{}, false
}

// Active returns the currently selected provider config, defaulting to
// "ergo-gateway" when the registry has no Active marker.
// ConfiguredActive returns the user's chosen active provider name, or ""
// when no registry file has been written (first run uses the built-in
// defaults, which should not override automatic selection).
func ConfiguredActive() string {
	p, err := path()
	if err != nil {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	reg := Load()
	if reg.Active != "" {
		if _, ok := Get(reg.Active); ok {
			return reg.Active
		}
	}
	for _, c := range reg.Providers {
		if c.Default {
			return c.Name
		}
	}
	return ""
}

func Active() Config {
	reg := Load()
	if reg.Active != "" {
		if p, ok := Get(reg.Active); ok {
			return p
		}
	}
	if p, ok := Get("ergo-gateway"); ok {
		return p
	}
	if len(reg.Providers) > 0 {
		return reg.Providers[0]
	}
	return Config{Name: "ergo-gateway", BaseURL: "https://api.tjcoder.com", Auth: AuthDeviceCode}
}
