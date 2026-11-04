package update

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestIsPermissionErr(t *testing.T) {
	if !isPermissionErr(os.ErrPermission) {
		t.Fatal("EACCES-style must be permission")
	}
	if !isPermissionErr(syscall.EACCES) {
		t.Fatal("EACCES must be permission")
	}
	if !isPermissionErr(syscall.EPERM) {
		t.Fatal("EPERM must be permission")
	}
	if isPermissionErr(errors.New("nope")) {
		t.Fatal("unrelated error must not be permission")
	}
}

func TestReplaceExecutablePermissionDenied(t *testing.T) {
	if testing.Short() {
		t.Skip("requires root-owned dir simulation")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "coder")
	os.WriteFile(target, []byte("OLD"), 0o755)
	// Make the directory read-only so the temp file can't be created.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("cannot chmod dir (running as root?): %v", err)
	}
	defer os.Chmod(dir, 0o755)

	err := replaceExecutable(target, []byte("NEW"))
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
}
