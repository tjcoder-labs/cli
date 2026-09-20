package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCloudflareRequiresAction(t *testing.T) {
	raw := json.RawMessage(`{}`)
	if _, err := (cloudflareTool{}).Execute(context.Background(), raw, ExecEnv{}); err == nil {
		t.Fatal("expected error when action is missing")
	}
}

func TestCloudflareUnknownAction(t *testing.T) {
	raw := json.RawMessage(`{"action":"frobnicate"}`)
	if _, err := (cloudflareTool{}).Execute(context.Background(), raw, ExecEnv{}); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestCloudflareAuthRequiresToken(t *testing.T) {
	raw := json.RawMessage(`{"action":"auth"}`)
	if _, err := (cloudflareTool{}).Execute(context.Background(), raw, ExecEnv{}); err == nil {
		t.Fatal("expected error when token is empty")
	}
}

func TestCloudflareNoTokenConfigured(t *testing.T) {
	// Force no token by clearing env (persisted file may exist on dev
	// machines; the tool reads it, so this test only asserts the error
	// path when neither env nor file yields a token). We only check
	// behavior when the token resolution returns empty by using an
	// action that requires a token and pointing resolution away from
	// any real file is not possible without env isolation. Skip if a
	// token happens to be present.
	token := cloudflareResolveToken()
	if token != "" {
		t.Skip("token configured; skipping no-token error-path assertion")
	}
	raw := json.RawMessage(`{"action":"zones"}`)
	if _, err := (cloudflareTool{}).Execute(context.Background(), raw, ExecEnv{}); err == nil {
		t.Fatal("expected error when no token is configured")
	}
}

func TestIsHex(t *testing.T) {
	cases := map[string]bool{
		"d26a5c55f27b3226a8de9671d5f14d9e": true,
		"not-hex":                          false,
		"":                                 false,
	}
	for in, want := range cases {
		if got := isHex(in); got != want {
			t.Fatalf("isHex(%q) = %v, want %v", in, got, want)
		}
	}
}
