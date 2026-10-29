package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/providers"
)

func TestResolveErgoModel(t *testing.T) {
	avail := ErgoFallbackModels()
	cases := []struct{ name, explicit, saved, want string }{
		{"default when nothing saved", "", "", "ergo/pro"},
		{"ollama tag ignored", "", "deepseek-v4-pro:cloud", "ergo/pro"},
		{"saved gateway model kept", "", "ergo/genius", "ergo/genius"},
		{"saved legacy id migrated", "", "ergo-genius", "ergo/genius"},
		{"saved legacy lite", "", "ergo-lite", "ergo/flash"},
		{"explicit wins even if unknown", "my-model", "ergo-lite", "my-model"},
		{"explicit legacy alias mapped", "ergo", "", "ergo/pro"},
	}
	for _, c := range cases {
		if got := ResolveErgoModel(c.explicit, c.saved, avail); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	// default missing from the live list -> first served model
	if got := ResolveErgoModel("", "x", []string{"alpha", "beta"}); got != "alpha" {
		t.Errorf("got %q want alpha", got)
	}
}

func TestErgoAvailableModelsFetchesAndFallsBack(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"ergo"},{"id":" ergo-lite "},{"id":""}]}`))
	}))
	defer srv.Close()
	got := ErgoAvailableModels(context.Background(), NewErgo(srv.URL, "k", 0))
	if !reflect.DeepEqual(got, []string{"ergo", "ergo-lite"}) {
		t.Fatalf("got %v", got)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("auth header %q", gotAuth)
	}

	// server error -> fallback
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	if got := ErgoAvailableModels(context.Background(), NewErgo(bad.URL, "k", 0)); !reflect.DeepEqual(got, ErgoFallbackModels()) {
		t.Fatalf("fallback: got %v", got)
	}
	// no key / non-ergo provider -> fallback without a request
	if got := ErgoAvailableModels(context.Background(), NewErgo(srv.URL, "", 0)); !reflect.DeepEqual(got, ErgoFallbackModels()) {
		t.Fatalf("no key: got %v", got)
	}
	// empty list -> fallback
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) }))
	defer empty.Close()
	if got := ErgoAvailableModels(context.Background(), NewErgo(empty.URL, "k", 0)); !reflect.DeepEqual(got, ErgoFallbackModels()) {
		t.Fatalf("empty: got %v", got)
	}
}

func TestErgoChatModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"Unknown model \"deepseek-v4-pro:cloud\". Available: ergo, ergo-lite, ergo-genius."}}`))
	}))
	defer srv.Close()
	_, err := NewErgo(srv.URL, "k", 0).Chat(context.Background(), ChatRequest{
		Model: "deepseek-v4-pro:cloud", Messages: []Message{{Role: "user", Content: "hi"}},
	}, nil)
	var mnf *ErgoModelNotFoundError
	if !errors.As(err, &mnf) || !IsModelNotFound(err) {
		t.Fatalf("want ErgoModelNotFoundError, got %T %v", err, err)
	}
	if mnf.Model != "deepseek-v4-pro:cloud" {
		t.Errorf("model %q", mnf.Model)
	}
	if !reflect.DeepEqual(mnf.Available, []string{"ergo", "ergo-lite", "ergo-genius"}) {
		t.Errorf("available %v", mnf.Available)
	}
}

func TestErgoChatOther404IsNotModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`not here`))
	}))
	defer srv.Close()
	_, err := NewErgo(srv.URL, "k", 0).Chat(context.Background(), ChatRequest{Model: "ergo", Messages: []Message{{Role: "user", Content: "hi"}}}, nil)
	if err == nil || IsModelNotFound(err) || !strings.Contains(err.Error(), "404") {
		t.Fatalf("got %v", err)
	}
}

func TestProviderKey(t *testing.T) {
	if got := ProviderKey(NewErgo("http://x", "k", 0)); got != "ergo" {
		t.Fatalf("got %q", got)
	}
	p, err := NewProviderFromConfig(providers.Config{Name: "my-gateway", BaseURL: "http://localhost"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ProviderKey(p); got != "my-gateway" {
		t.Fatalf("custom provider key = %q, want my-gateway", got)
	}
}

func TestErgoListAllModelsFlattensProviders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/v1/providers" {
			_, _ = w.Write([]byte(`{"object":"list","providers":[
				{"id":"ergo","models":[{"id":"ergo"},{"id":"ergo-lite"}]},
				{"id":"xai","models":[{"id":"xai/grok-4.5"}]},
				{"id":"ollama","models":[{"id":"ollama/phi3"}]}
			]}`))
			return
		}
		// /v1/models fallback path
		_, _ = w.Write([]byte(`{"data":[{"id":"ergo"},{"id":"ergo-lite"},{"id":"ergo-genius"}]}`))
	}))
	defer srv.Close()

	e := NewErgo(srv.URL, "k", 0)
	models, err := e.ListAllModels(context.Background())
	if err != nil {
		t.Fatalf("ListAllModels: %v", err)
	}
	var names []string
	for _, m := range models {
		names = append(names, m.Name)
	}
	want := []string{"ergo", "ergo-lite", "xai/grok-4.5", "ollama/phi3"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v want %v", names, want)
	}
	// family is the provider id
	if models[2].Family != "xai" {
		t.Fatalf("family for xai model = %q", models[2].Family)
	}
}

func TestErgoListAllModelsFallsBackOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/providers" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"ergo"},{"id":"ergo-lite"}]}`))
	}))
	defer srv.Close()

	e := NewErgo(srv.URL, "k", 0)
	models, err := e.ListAllModels(context.Background())
	if err != nil {
		t.Fatalf("ListAllModels: %v", err)
	}
	if len(models) != 2 || models[0].Name != "ergo" {
		t.Fatalf("fallback mismatch: %+v", models)
	}
}

func TestNormalizeErgoModel(t *testing.T) {
	for in, want := range map[string]string{
		"ergo": "ergo/pro", "ergo-lite": "ergo/flash", "ergo-genius": "ergo/genius",
		"ergo-standard": "ergo/flash", "ergo/lite": "ergo/flash", "ERGO/PRO": "ergo/pro",
		"ergo-turbo": "ergo/pro", "ergo/unknown": "ergo/pro",
		"xai/grok-4.3": "xai/grok-4.3", "qwen3:8b": "qwen3:8b", "": "",
	} {
		if got := NormalizeErgoModel(in); got != want {
			t.Errorf("NormalizeErgoModel(%q) = %q, want %q", in, got, want)
		}
	}
	if !IsErgoTier("ergo-lite") || IsErgoTier("xai/grok-4.3") {
		t.Fatal("IsErgoTier")
	}
}

func TestErgoListModelsUnifiedCatalogue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"ergo/pro","owned_by":"ergo"},{"id":"ergo","owned_by":"ergo","alias_for":"ergo/pro"},{"id":"xai/grok-4.3","owned_by":"xai"},{"id":"ollama/qwen3:8b"}]}`))
	}))
	defer srv.Close()
	got := ErgoAvailableModels(context.Background(), NewErgo(srv.URL, "k", 0))
	if !reflect.DeepEqual(got, []string{"ergo/pro", "xai/grok-4.3", "ollama/qwen3:8b"}) {
		t.Fatalf("got %v (aliases must be hidden, /v1/providers 404 falls back)", got)
	}
	infos, _ := NewErgo(srv.URL, "k", 0).ListModels(context.Background())
	if infos[1].Family != "xai" || infos[2].Family != "ollama" {
		t.Fatalf("families: %+v", infos)
	}
}

func TestErgoOllamaProtocol(t *testing.T) {
	var paths []string
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		auth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/ollama/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"ergo/pro","details":{"family":"ergo"}}]}`))
		case "/ollama/api/chat":
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hi"},"done":true}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("ERGO_PROTOCOL", "ollama")
	t.Setenv("ERGO_OLLAMA_PATH", "/ollama/api")
	e := newErgo(srv.URL, "k", 0, "", "")
	if e.Protocol() != "ollama" {
		t.Fatalf("protocol %q", e.Protocol())
	}
	models, err := e.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].Name != "ergo/pro" {
		t.Fatalf("models %v err %v", models, err)
	}
	msg, err := e.Chat(context.Background(), ChatRequest{Model: "ergo/pro", Messages: []Message{{Role: "user", Content: "hey"}}}, nil)
	if err != nil || msg.Content != "hi" {
		t.Fatalf("chat %+v err %v", msg, err)
	}
	if auth != "Bearer k" || paths[len(paths)-1] != "/ollama/api/chat" {
		t.Fatalf("auth %q paths %v", auth, paths)
	}

	// default: OpenAI protocol; registry "protocol":"ollama" with default /api.
	t.Setenv("ERGO_PROTOCOL", "")
	t.Setenv("ERGO_OLLAMA_PATH", "")
	if p := newErgo(srv.URL, "k", 0, "", "").Protocol(); p != "openai" {
		t.Fatalf("default protocol %q", p)
	}
	r := newErgo(srv.URL, "k", 0, "ollama", "")
	if r.ollama == nil || r.ollama.apiBase() != srv.URL+"/api" {
		t.Fatalf("registry ollama base %v", r.ollama)
	}
}
