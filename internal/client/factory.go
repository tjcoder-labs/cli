package client

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/providers"
)

// ProviderOrder is the canonical priority order for listing providers and for
// auto-selecting a default: ergo first, then local ollama, then ollama-cloud.
// Anything that shows or iterates providers (help text, errors, pickers)
// should use this slice so the ordering stays consistent.
var ProviderOrder = []string{"tjcoder", "ollama", "ollama-cloud", "gemini"}

// defaultOllamaHost is the well-known local Ollama address. It is used as
// a sentinel: if the caller passes this (or empty) we check the registry
// for a user-configured override before falling back to the literal value.
const defaultOllamaHost = "http://localhost:11434"

// NewProvider creates a Provider by name. Supported names (in priority
// order): "ergo", "ollama", "ollama-cloud", "gemini". Any other name is
// resolved against the global provider registry (see session.Providers):
// a registered OpenAI-compatible gateway builds an OpenAICompatible client
// (or an Ergo client for the ergo-gateway default).
func NewProvider(name, host, geminiAPIKey string, timeout time.Duration) (Provider, error) {
	switch name {
	case "", "ollama":
		// Prefer a custom base_url from the registry over the hardcoded default,
		// but only when the caller has not already supplied an explicit host.
		// This lets callers (tests, --host flag) pin a specific address while
		// still respecting a user-configured non-default Ollama port/address.
		if host == "" || host == defaultOllamaHost {
			if cfg, ok := providers.Get("ollama"); ok && cfg.BaseURL != "" {
				host = cfg.BaseURL
			}
		}
		return NewOllamaWithTimeout(host, timeout), nil
	case "ollama-cloud", "ollama_cloud":
		// Hosted Ollama (ollama.com). Prefer registry credentials over env vars
		// so that `coder providers add` / `/login ollama-cloud` are the
		// canonical way to store the key. Fall back to OLLAMA_API_KEY so that
		// existing setups continue to work without re-running /login.
		cloudHost := host
		cloudKey := os.Getenv("OLLAMA_API_KEY")
		if cfg, ok := providers.Get("ollama-cloud"); ok {
			if cfg.BaseURL != "" && (cloudHost == "" || cloudHost == defaultOllamaHost) {
				cloudHost = cfg.BaseURL
			}
			if cfg.APIKey != "" {
				cloudKey = cfg.APIKey
			}
		}
		if cloudHost == "" || cloudHost == defaultOllamaHost {
			cloudHost = os.Getenv("OLLAMA_CLOUD_HOST")
		}
		return NewOllamaCloud(cloudHost, cloudKey, timeout), nil
	case "gemini":
		if geminiAPIKey == "" {
			return nil, fmt.Errorf("--gemini-api-key or GEMINI_API_KEY required for gemini provider")
		}
		return NewGemini(geminiAPIKey, timeout), nil
	case "tjcoder", "ergo":
		// The ergo provider authenticates with a scoped ergo API key. We read
		// it from ERGO_API_KEY or the token saved by /login. When neither is set we still return a configured
		// provider so the error surfaces at first chat with a clean 401-typed
		// ErgoAuthError (rather than a startup failure).
		base := os.Getenv("ERGO_BASE_URL")
		if base == "" {
			base = ErgoDefaultBaseURL
		}
		return newErgo(base, auth.ResolveErgoToken(), timeout, "", ""), nil
	default:
		// A registered provider name (e.g. "ergo-gateway", a custom gateway).
		if cfg, ok := providers.Get(name); ok {
			return NewProviderFromConfig(cfg, timeout)
		}
		return nil, fmt.Errorf("unknown provider %q: supported values are ergo, ollama, ollama-cloud, gemini (or run `coder providers add`)", name)
	}
}

// NewProviderFromConfig builds a live Provider from a registry entry.
// Routing is driven by the config's resolved wire protocol: ergo/ergo-gateway
// maps to the full Ergo client (device-auth, wallet billing, 401/402
// handling); anything resolving to "ollama" builds the Ollama client
// (/api/...); everything else maps to the generic OpenAICompatible client
// (/v1/...). A provider with AuthAPIKey uses its stored APIKey; one with
// AuthDeviceCode reads the keychain token.
func NewProviderFromConfig(cfg providers.Config, timeout time.Duration) (Provider, error) {
	if !cfg.Valid() {
		return nil, fmt.Errorf("invalid provider config %q (missing name or base_url)", cfg.Name)
	}
	switch cfg.Name {
	case "tjcoder", "ergo-gateway", "ergo":
		key := cfg.APIKey
		if key == "" {
			key = auth.ResolveErgoToken()
		}
		return newErgo(cfg.BaseURL, key, timeout, cfg.Protocol, cfg.APIPath), nil
	}

	switch cfg.ResolveProtocol() {
	case providers.ProtocolOllama:
		key := cfg.APIKey
		if key == "" {
			key = os.Getenv("OLLAMA_API_KEY")
		}
		base := cfg.BaseURL
		if base == "" || base == defaultOllamaHost {
			if env := os.Getenv("OLLAMA_CLOUD_HOST"); env != "" {
				base = env
			}
		}
		return NewOllamaCloud(base, key, timeout), nil
	default:
		key := cfg.APIKey
		p := NewOpenAICompatible(cfg.BaseURL, key, timeout)
		p.providerName = cfg.Name
		return p, nil
	}
}

// ActiveProvider builds the client for the registry's active provider.
// It is the default construction path when no explicit --provider is given.
func ActiveProvider(host, geminiAPIKey string, timeout time.Duration) (Provider, error) {
	active := providers.Active()
	return NewProviderFromConfig(active, timeout)
}

// newErgo builds the ergo client. The wire protocol is OpenAI unless
// ERGO_PROTOCOL=ollama (env wins) or the registry entry sets
// "protocol":"ollama"; the Ollama API prefix comes from ERGO_OLLAMA_PATH
// or "api_path" (default /api; the gateway also serves /ollama/api).
func newErgo(base, key string, timeout time.Duration, protocol, apiPath string) *Ergo {
	e := NewErgo(base, key, timeout)
	if v := strings.TrimSpace(os.Getenv("ERGO_PROTOCOL")); v != "" {
		protocol = v
	}
	if v := strings.TrimSpace(os.Getenv("ERGO_OLLAMA_PATH")); v != "" {
		apiPath = v
	}
	if strings.EqualFold(strings.TrimSpace(protocol), providers.ProtocolOllama) {
		e.UseOllamaProtocol(apiPath)
	}
	return e
}
