package auth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

// Tests default to the file store so they never touch a real OS keychain;
// keychain behaviour is covered with go-keyring's in-memory mock below.
func TestMain(m *testing.M) {
	os.Setenv("ERGO_NO_KEYRING", "1")
	os.Exit(m.Run())
}

func TestKeychainPreferredAndFileFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("ERGO_NO_KEYRING", "")
	os.Unsetenv("ERGO_API_KEY")
	keyring.MockInit()

	if err := SaveErgoToken("ek_ergo-cli_kc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "coder", "ergo-token")); !os.IsNotExist(err) {
		t.Fatalf("token file must not exist when keychain works (err=%v)", err)
	}
	if got := ResolveErgoToken(); got != "ek_ergo-cli_kc" || TokenStore() != "keychain" {
		t.Fatalf("keychain read failed: %q store=%q", got, TokenStore())
	}
	if err := ClearErgoToken(); err != nil {
		t.Fatal(err)
	}
	if got := ResolveErgoToken(); got != "" || TokenStore() != "" {
		t.Fatalf("expected signed out, got %q", got)
	}

	// Broken keychain → 0600 file fallback.
	keyring.MockInitWithError(os.ErrPermission)
	if err := SaveErgoToken("ek_ergo-cli_file"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "coder", "ergo-token"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("fallback file wrong: %v %v", err, info)
	}
	if got := ResolveErgoToken(); got != "ek_ergo-cli_file" || TokenStore() != "file" {
		t.Fatalf("file fallback read failed: %q %q", got, TokenStore())
	}
	if err := ClearErgoToken(); err != nil || ResolveErgoToken() != "" {
		t.Fatalf("clear failed: %v", err)
	}
}

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
