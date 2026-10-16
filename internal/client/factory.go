package client

import (
	"fmt"
	"os"
	"time"
)

// NewProvider creates a Provider by name. Supported names: "ollama", "gemini", "ergo".
func NewProvider(name, host, geminiAPIKey string, timeout time.Duration) (Provider, error) {
	switch name {
	case "", "ollama":
		return NewOllamaWithTimeout(host, timeout), nil
	case "gemini":
		if geminiAPIKey == "" {
			return nil, fmt.Errorf("--gemini-api-key or GEMINI_API_KEY required for gemini provider")
		}
		return NewGemini(geminiAPIKey, timeout), nil
	case "ergo":
		// The ergo provider authenticates with a scoped ergo API key. We read
		// it from the environment here; the TUI's /login flow persists it and
		// re-resolves. When neither is set we still return a configured
		// provider so the error surfaces at first chat with a clean 401-typed
		// ErgoAuthError (rather than a startup failure).
		base := os.Getenv("ERGO_BASE_URL")
		if base == "" {
			base = ErgoDefaultBaseURL
		}
		return NewErgo(base, os.Getenv("ERGO_API_KEY"), timeout), nil
	default:
		return nil, fmt.Errorf("unknown provider %q: supported values are ollama, gemini, ergo", name)
	}
}
