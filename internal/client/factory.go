package client

import (
	"fmt"
	"os"
	"time"

	"github.com/tjcoder-labs/cli/internal/auth"
)

// ProviderOrder is the canonical priority order for listing providers and for
// auto-selecting a default: ergo first, then local ollama, then ollama-cloud.
// Anything that shows or iterates providers (help text, errors, pickers)
// should use this slice so the ordering stays consistent.
var ProviderOrder = []string{"ergo", "ollama", "ollama-cloud", "gemini"}

// NewProvider creates a Provider by name. Supported names (in priority
// order): "ergo", "ollama", "ollama-cloud", "gemini".
func NewProvider(name, host, geminiAPIKey string, timeout time.Duration) (Provider, error) {
	switch name {
	case "", "ollama":
		return NewOllamaWithTimeout(host, timeout), nil
	case "ollama-cloud", "ollama_cloud":
		// Hosted Ollama (ollama.com). Key comes from OLLAMA_API_KEY. A missing
		// key is not fatal here: the first request surfaces the server's 401
		// and the TUI can tell the user to set OLLAMA_API_KEY.
		cloudHost := host
		if cloudHost == "" || cloudHost == "http://localhost:11434" {
			cloudHost = os.Getenv("OLLAMA_CLOUD_HOST")
		}
		return NewOllamaCloud(cloudHost, os.Getenv("OLLAMA_API_KEY"), timeout), nil
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
		return nil, fmt.Errorf("unknown provider %q: supported values are ergo, ollama, ollama-cloud, gemini", name)
	}
}
