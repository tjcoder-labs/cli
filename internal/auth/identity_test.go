package auth

import (
	"os"
	"testing"
)

func TestErgoIdentityRoundTripAndClear(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("ERGO_NO_KEYRING", "1")
	if _, ok := LoadErgoIdentity(); ok {
		t.Fatal("no identity expected before sign-in")
	}
	if err := SaveErgoIdentity(Identity{Email: " tj@tjcoder.com ", Name: "TJ"}); err != nil {
		t.Fatal(err)
	}
	id, ok := LoadErgoIdentity()
	if !ok || id.Email != "tj@tjcoder.com" || id.Name != "TJ" {
		t.Fatalf("loaded %+v ok=%v", id, ok)
	}
	if info, err := os.Stat(ErgoIdentityPath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity file mode: %v %v", info, err)
	}
	// Signing out (ClearErgoToken) must drop the identity too.
	if err := SaveErgoToken("tok"); err != nil {
		t.Fatal(err)
	}
	if err := ClearErgoToken(); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadErgoIdentity(); ok {
		t.Fatal("identity should be cleared on logout")
	}
}
