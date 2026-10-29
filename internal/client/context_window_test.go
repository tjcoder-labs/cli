package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaContextWindowPrefersNumCtx(t *testing.T) {
	cases := map[string]int{
		`{"parameters":"stop \"<eos>\"\nnum_ctx 8192","model_info":{"gemma3.context_length":131072}}`: 8192,
		`{"model_info":{"llama.context_length":131072,"llama.embedding_length":4096}}`:                131072,
		`{"model_info":{},"context_length":262144}`:                                                   262144,
		`{"model_info":{}}`: 0,
	}
	for body, want := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/show" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		}))
		n, err := NewOllama(srv.URL).ContextWindow(context.Background(), "m")
		srv.Close()
		if err != nil || n != want {
			t.Errorf("%s -> %d (%v), want %d", body, n, err, want)
		}
	}
}

func TestOllamaContextWindowUsesRunningContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:latest","model":"qwen3:latest","context_length":4096}]}`))
		default:
			_, _ = w.Write([]byte(`{"model_info":{"qwen3.context_length":40960}}`))
		}
	}))
	defer srv.Close()
	if n, _ := NewOllama(srv.URL).ContextWindow(context.Background(), "qwen3"); n != 4096 {
		t.Fatalf("loaded model must report its runtime window, got %d", n)
	}
}

func TestErgoContextWindowFromGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" && r.Header.Get("Authorization") == "Bearer k" {
			_, _ = w.Write([]byte(`{"model_info":{"ollama.context_length":262144},"context_length":262144}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	if n, _ := NewErgo(srv.URL, "k", 0).ContextWindow(context.Background(), "ergo/flash"); n != 262144 {
		t.Fatalf("gateway window must win over the fallback table, got %d", n)
	}
	// Gateway can't say: fallback table, then explicit unknown.
	if n, _ := NewErgo(srv.URL, "bad", 0).ContextWindow(context.Background(), "ergo/flash"); n != 131072 {
		t.Fatalf("fallback ergo/flash = %d", n)
	}
	if n, _ := NewErgo(srv.URL, "bad", 0).ContextWindow(context.Background(), "mystery"); n != 0 {
		t.Fatalf("unknown = %d", n)
	}
}
