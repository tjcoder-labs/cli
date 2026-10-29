package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/providers"
)

// TestNewProviderFromConfigRoutesOllama ensures a registry entry named
// "ollama" builds an Ollama client (hitting /api/tags) rather than the
// OpenAICompatible client (/v1/models). This is the regression guard for the
// unified-provider bug where every non-ergo entry was routed to the generic
// OpenAI client.
func TestNewProviderFromConfigRoutesOllama(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3"}]}`))
	}))
	defer srv.Close()

	p, err := NewProviderFromConfig(providers.Config{Name: "ollama", BaseURL: srv.URL}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*Ollama); !ok {
		t.Fatalf("expected *Ollama, got %T", p)
	}
	if _, err := p.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(paths) == 0 || paths[0] != "/api/tags" {
		t.Fatalf("expected /api/tags request, got %v", paths)
	}
}

// TestNewProviderFromConfigRoutesOllamaCloud ensures "ollama-cloud" also
// builds an Ollama client.
func TestNewProviderFromConfigRoutesOllamaCloud(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	p, err := NewProviderFromConfig(providers.Config{Name: "ollama-cloud", BaseURL: srv.URL}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*Ollama); !ok {
		t.Fatalf("expected *Ollama, got %T", p)
	}
}

// TestNewProviderFromConfigProtocolOverride verifies an explicit protocol
// override can force an Ollama-named endpoint to speak OpenAI, and vice
// versa, so a custom gateway can pick its wire protocol independent of name.
func TestNewProviderFromConfigProtocolOverride(t *testing.T) {
	// A provider named "ollama" but forced to openai should build an
	// OpenAICompatible client.
	p, err := NewProviderFromConfig(providers.Config{Name: "ollama", BaseURL: "http://x", Protocol: providers.ProtocolOpenAI}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*OpenAICompatible); !ok {
		t.Fatalf("expected *OpenAICompatible, got %T", p)
	}

	// A generic-named provider forced to ollama should build an Ollama
	// client.
	p2, err := NewProviderFromConfig(providers.Config{Name: "my-ollama", BaseURL: "http://x", Protocol: providers.ProtocolOllama}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p2.(*Ollama); !ok {
		t.Fatalf("expected *Ollama, got %T", p2)
	}
}
