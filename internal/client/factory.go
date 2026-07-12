package client

import (
	"fmt"
	"time"
)

// NewProvider creates a Provider by name. Supported names: "ollama", "gemini".
func NewProvider(name, host, geminiAPIKey string, timeout time.Duration) (Provider, error) {
	switch name {
	case "", "ollama":
		return NewOllamaWithTimeout(host, timeout), nil
	case "gemini":
		if geminiAPIKey == "" {
			return nil, fmt.Errorf("--gemini-api-key or GEMINI_API_KEY required for gemini provider")
		}
		return NewGemini(geminiAPIKey, timeout), nil
	default:
		return nil, fmt.Errorf("unknown provider %q: supported values are ollama, gemini", name)
	}
}
