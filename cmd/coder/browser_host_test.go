package main

import (
	"errors"
	"os"
	"testing"

	"github.com/tjcoder-labs/cli/internal/nativehost"
)

func TestInteractiveBrowserLifecycle(t *testing.T) {
	home, e := os.MkdirTemp("", "coder-tui-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	want := errors.New("test TUI return")
	e = runInteractiveBrowser(func() error {
		for _, p := range []string{nativehost.SocketPath(), nativehost.SocketPath() + ".native"} {
			info, e := os.Stat(p)
			if e != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
				t.Errorf("missing private relay %s: %v", p, e)
			}
		}
		return want
	})
	if !errors.Is(e, want) {
		t.Fatal(e)
	}
	if _, e = os.Stat(nativehost.SocketPath()); !os.IsNotExist(e) {
		t.Fatal("relay outlived interactive run")
	}
}
