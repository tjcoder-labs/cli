package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/providers"
)

func TestValidateProviderIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want ******", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer srv.Close()

	count, err := validateProviderIntegration(providers.Config{
		Name: "test-gateway", BaseURL: srv.URL, APIKey: "test-key",
	})
	if err != nil {
		t.Fatalf("validateProviderIntegration: %v", err)
	}
	if count != 2 {
		t.Fatalf("model count = %d, want 2", count)
	}
}

func TestValidateProviderIntegrationRejectsEmptyCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	if _, err := validateProviderIntegration(providers.Config{Name: "test-gateway", BaseURL: srv.URL}); err == nil {
		t.Fatal("expected empty model catalog to fail integration validation")
	}
}

func TestModelFromProviderModels(t *testing.T) {
	models := []client.ModelInfo{{Name: "model-a"}, {Name: "model-b"}}
	if got, changed := modelFromProviderModels("", models); got != "model-a" || changed {
		t.Fatalf("unset model: got %q, changed=%v", got, changed)
	}
	if got, changed := modelFromProviderModels("model-b", models); got != "model-b" || changed {
		t.Fatalf("available model: got %q, changed=%v", got, changed)
	}
	if got, changed := modelFromProviderModels("stale", models); got != "model-a" || !changed {
		t.Fatalf("stale model: got %q, changed=%v", got, changed)
	}
	if got, changed := modelFromProviderModels("stale", nil); got != "stale" || changed {
		t.Fatalf("empty list: got %q, changed=%v", got, changed)
	}
}
