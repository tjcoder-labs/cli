package client

import (
	"fmt"
	"os"
	"time"

	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/providers"
)

// ProviderOrder is the canonical priority order for listing providers and for
// auto-selecting a default: ergo first, then local ollama, then ollama-cloud.
// Anything that shows or iterates providers (help text, errors, pickers)
// should use this slice so the ordering stays consistent.
var ProviderOrder = []string{"ergo", "ollama", "ollama-cloud", "gemini"}

// NewProvider creates a Provider by name. Supported names (in priority
// order): "ergo", "ollama", "ollama-cloud", "gemini". Any other name is
// resolved against the global provider registry (see session.Providers):
// a registered OpenAI-compatible gateway builds an OpenAICompatible client
// (or an Ergo client for the ergo-gateway default).
func NewProvider(name, host, geminiAPIKey string, timeout time.Duration) (Provider, error) {
	// defaultOllamaHost is the well-known local Ollama address. It is used as
	// a sentinel: if the caller passes this (or empty) we check the registry
	// for a user-configured override before falling back to the literal value.
	const defaultOllamaHost = "http://localhost:11434"
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
	case "ergo":
		// The ergo provider authenticates with a scoped ergo API key. We read
		// it from ERGO_API_KEY or the token saved by /login. When neither is set we still return a configured
		// provider so the error surfaces at first chat with a clean 401-typed
		// ErgoAuthError (rather than a startup failure).
		base := os.Getenv("ERGO_BASE_URL")
		if base == "" {
			base = ErgoDefaultBaseURL
		}
		return NewErgo(base, auth.ResolveErgoToken(), timeout), nil
	default:
		// A registered provider name (e.g. "ergo-gateway", a custom gateway).
		if cfg, ok := providers.Get(name); ok {
			return NewProviderFromConfig(cfg, timeout)
		}
		return nil, fmt.Errorf("unknown provider %q: supported values are ergo, ollama, ollama-cloud, gemini (or run `coder providers add`)", name)
	}
}

// NewProviderFromConfig builds a live Provider from a registry entry.
// The ergo-gateway default maps to the full Ergo client (device-auth, wallet
// billing, 401/402 handling); everything else maps to the generic
// OpenAICompatible client. A provider with AuthAPIKey uses its stored APIKey;
// one with AuthDeviceCode reads the keychain token.
func NewProviderFromConfig(cfg providers.Config, timeout time.Duration) (Provider, error) {
	if !cfg.Valid() {
		return nil, fmt.Errorf("invalid provider config %q (missing name or base_url)", cfg.Name)
	}
	switch cfg.Name {
	case "ergo-gateway", "ergo":
		key := cfg.APIKey
		if key == "" {
			key = auth.ResolveErgoToken()
		}
		return NewErgo(cfg.BaseURL, key, timeout), nil
	default:
		key := cfg.APIKey
		return NewOpenAICompatible(cfg.BaseURL, key, timeout), nil
	}
}

// ActiveProvider builds the client for the registry's active provider.
// It is the default construction path when no explicit --provider is given.
func ActiveProvider(host, geminiAPIKey string, timeout time.Duration) (Provider, error) {
	active := providers.Active()
	return NewProviderFromConfig(active, timeout)
}
