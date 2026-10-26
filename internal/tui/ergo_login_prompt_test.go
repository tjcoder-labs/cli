package tui

import (
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
)

func TestErgoLoginPrompt(t *testing.T) {
	if msg := ergoLoginPrompt(client.NewErgo("https://example.invalid", "", 0)); !strings.Contains(msg, "/login") {
		t.Fatalf("expected /login prompt for unauthenticated ergo, got %q", msg)
	}
	if msg := ergoLoginPrompt(client.NewErgo("https://example.invalid", "k", 0)); msg != "" {
		t.Fatalf("expected no prompt when authenticated, got %q", msg)
	}
	if msg := ergoLoginPrompt(client.NewOllamaWithTimeout("http://localhost:11434", 0)); msg != "" {
		t.Fatalf("expected no prompt for ollama, got %q", msg)
	}
}
