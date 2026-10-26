package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestResolveErgoModel(t *testing.T) {
	avail := ErgoFallbackModels()
	cases := []struct{ name, explicit, saved, want string }{
		{"default when nothing saved", "", "", "ergo"},
		{"ollama tag ignored", "", "deepseek-v4-pro:cloud", "ergo"},
		{"saved gateway model kept", "", "ergo-genius", "ergo-genius"},
		{"explicit wins even if unknown", "my-model", "ergo-lite", "my-model"},
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
}
