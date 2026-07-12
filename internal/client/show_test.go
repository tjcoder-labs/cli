package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOllama_ShowModel verifies that the ShowModel helper
// decodes the /api/show response into a ModelDetails struct
// and extracts any ADAPTER entries from the modelfile. This
// is the test the user (and reviewers) will run to confirm
// the new `coder models info <name>` subcommand surfaces
// provenance information correctly.
func TestOllama_ShowModel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req["model"] == "" {
			http.Error(w, "missing model", http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"modelfile": "# Modelfile\nFROM qwen2.5-coder:7b\nADAPTER ./tjcoder-coding-adapter.gguf\nPARAMETER num_ctx 32768\nLICENSE MIT",
			"parameters": "num_ctx 32768\nstop \"<|im_end|>\"\n",
			"template":   "{{ .System }}\n{{ .Prompt }}",
			"license":    "MIT",
			"details": map[string]string{
				"family":             "qwen2",
				"parameter_size":     "7.6B",
				"quantization_level": "Q4_K_M",
				"parent_model":       "qwen2.5-coder:7b",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	o := NewOllamaWithTimeout(ts.URL, 5*time.Second)
	details, err := o.ShowModel(context.Background(), "tjcoder-coding")
	if err != nil {
		t.Fatalf("ShowModel failed: %v", err)
	}
	if details.Name != "tjcoder-coding" {
		t.Errorf("expected name=tjcoder-coding, got %q", details.Name)
	}
	if details.License != "MIT" {
		t.Errorf("expected license=MIT, got %q", details.License)
	}
	if !strings.Contains(details.Modelfile, "FROM qwen2.5-coder:7b") {
		t.Errorf("modelfile missing FROM line: %q", details.Modelfile)
	}
	if len(details.Adapters) != 1 || details.Adapters[0] != "./tjcoder-coding-adapter.gguf" {
		t.Errorf("expected one adapter, got %v", details.Adapters)
	}
	if details.Details["family"] != "qwen2" {
		t.Errorf("expected family=qwen2, got %q", details.Details["family"])
	}
	if details.Details["parent_model"] != "qwen2.5-coder:7b" {
		t.Errorf("expected parent_model=qwen2.5-coder:7b, got %q", details.Details["parent_model"])
	}
	if !strings.Contains(details.Template, "{{ .Prompt }}") {
		t.Errorf("expected template to contain prompt directive, got %q", details.Template)
	}
	if !strings.Contains(details.Parameters, "num_ctx 32768") {
		t.Errorf("expected parameters to contain num_ctx, got %q", details.Parameters)
	}
}

// TestOllama_ShowModel_NotFound verifies the error path when the
// upstream model doesn't exist. This is important because
// `coder models info <name>` should fail loudly (not silently
// return empty fields) when the user mistypes a model name.
func TestOllama_ShowModel_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model 'nope' not found"}`, http.StatusNotFound)
	}))
	defer ts.Close()

	o := NewOllamaWithTimeout(ts.URL, 5*time.Second)
	_, err := o.ShowModel(context.Background(), "nope")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 in error, got %v", err)
	}
}
