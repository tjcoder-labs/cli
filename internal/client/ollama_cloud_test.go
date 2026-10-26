package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/auth"
)

func TestOllamaCloudSendsBearerOnAllRequests(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("%s %s: Authorization = %q, want Bearer test-key", r.Method, r.URL.Path, got)
		}
		seen = append(seen, r.URL.Path)
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"gpt-oss:120b-cloud"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"model_info":{"llama.context_length":131072}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	o := NewOllamaCloud(srv.URL, " test-key ", 5*time.Second)
	models, err := o.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].Name != "gpt-oss:120b-cloud" {
		t.Fatalf("unexpected models: %+v", models)
	}
	if _, err := o.ContextWindow(context.Background(), "gpt-oss:120b-cloud"); err != nil {
		t.Fatalf("ContextWindow: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("no requests observed")
	}
}

func TestOllamaLocalSendsNoAuthorization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("local ollama must not send Authorization")
		}
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()
	if _, err := NewOllamaWithTimeout(srv.URL, time.Second).ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFactoryOllamaCloudDefaultsToHostedAPI(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "k")
	t.Setenv("OLLAMA_CLOUD_HOST", "")
	p, err := NewProvider("ollama-cloud", "http://localhost:11434", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseURL() != OllamaCloudHost {
		t.Fatalf("BaseURL = %q, want %q", p.BaseURL(), OllamaCloudHost)
	}
}

func TestProviderOrderErgoFirst(t *testing.T) {
	want := []string{"ergo", "ollama", "ollama-cloud"}
	for i, w := range want {
		if ProviderOrder[i] != w {
			t.Fatalf("ProviderOrder[%d]=%q want %q", i, ProviderOrder[i], w)
		}
	}
	_, err := NewProvider("nope", "", "", 0)
	if err == nil || !strings.Contains(err.Error(), "ergo, ollama, ollama-cloud") {
		t.Fatalf("unknown-provider error should list ergo first, got %v", err)
	}
}

func TestNewProviderErgoUsesSavedToken(t *testing.T) {
	t.Setenv("ERGO_API_KEY", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	p, _ := NewProvider("ergo", "", "", 0)
	if p.(*Ergo).Authenticated() {
		t.Fatal("expected unauthenticated with no token")
	}
	if err := auth.SaveErgoToken("test-token"); err != nil {
		t.Fatal(err)
	}
	p, _ = NewProvider("ergo", "", "", 0)
	if !p.(*Ergo).Authenticated() {
		t.Fatal("expected saved token to be picked up")
	}
}
