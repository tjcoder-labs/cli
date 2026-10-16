package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveErgoTokenPrefersEnv(t *testing.T) {
	t.Setenv("ERGO_API_KEY", "env-key")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if got := ResolveErgoToken(); got != "env-key" {
		t.Fatalf("expected env key, got %q", got)
	}
}

func TestSaveAndResolveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("ERGO_API_KEY", "")
	os.Unsetenv("ERGO_API_KEY")

	if got := ResolveErgoToken(); got != "" {
		t.Fatalf("expected empty before save, got %q", got)
	}
	if err := SaveErgoToken("ergo_abc123"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := ResolveErgoToken(); got != "ergo_abc123" {
		t.Fatalf("expected persisted key, got %q", got)
	}

	info, err := os.Stat(filepath.Join(dir, "coder", "ergo-token"))
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}
}

func TestSaveEmptyTokenRejected(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := SaveErgoToken("  "); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestClearErgoToken(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("ERGO_API_KEY", "")
	os.Unsetenv("ERGO_API_KEY")

	if err := SaveErgoToken("ergo_abc123"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := ClearErgoToken(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := ResolveErgoToken(); got != "" {
		t.Fatalf("expected empty after clear, got %q", got)
	}
	// Clearing again is a no-op.
	if err := ClearErgoToken(); err != nil {
		t.Fatalf("second clear: %v", err)
	}
}
