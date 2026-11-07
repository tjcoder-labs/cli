package nativehost

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionLifecycleAndShim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "browser.sock")
	s, err := StartSession(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, p := range []string{path, path + ".native"} {
		info, e := os.Lstat(p)
		if e != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || !ownedByUser(info) {
			t.Fatalf("not owner-only unix socket %s: %v", p, e)
		}
	}
	if _, err = StartSession(path); err == nil {
		t.Fatal("replaced existing relay")
	}
	m := rpc(t, path, `{"id":8,"method":"Bridge.ping"}`)
	if !bytes.Contains(m["error"], []byte("extension not connected")) {
		t.Fatalf("missing clear disconnected error: %s", m)
	}
	if Serve("chrome-extension://evil/", bytes.NewReader(nil), io.Discard, path) == nil {
		t.Fatal("accepted foreign origin")
	}
	// A command socket greeting cannot attach an extension.
	m = rpc(t, path, `{"id":9,"method":"Bridge.hello"}`)
	if !bytes.Contains(m["error"], []byte("not accepted")) {
		t.Fatal("command client became extension")
	}
	c, e := net.Dial("unix", path+".native")
	if e != nil {
		t.Fatal(e)
	}
	c.SetDeadline(time.Now().Add(time.Second))
	WriteFrame(c, []byte(`{"origin":"chrome-extension://evil/"}`))
	var ack [1]byte
	if _, e = io.ReadFull(c, ack[:]); e == nil {
		t.Fatal("attachment accepted bad origin")
	}
	c.Close()
	// Real shim framing round-trip through the in-session relay.
	hostIn, extOut := io.Pipe()
	extIn, hostOut := io.Pipe()
	defer hostIn.Close()
	defer extOut.Close()
	defer extIn.Close()
	defer hostOut.Close()
	done := make(chan error, 1)
	go func() { done <- Serve(Origin, hostIn, hostOut, path) }()
	if e = WriteFrame(extOut, []byte(`{"method":"Bridge.hello"}`)); e != nil {
		t.Fatal(e)
	}
	go func() {
		for {
			b, e := ReadFrame(extIn)
			if e != nil {
				return
			}
			var req request
			json.Unmarshal(b, &req)
			b, _ = json.Marshal(map[string]any{"id": req.ID, "result": map[string]bool{"connected": true}})
			if WriteFrame(extOut, b) != nil {
				return
			}
		}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		if _, e = os.Stat(s.backend); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend not connected")
		}
		time.Sleep(time.Millisecond)
	}
	m = rpc(t, path, `{"id":10,"method":"Bridge.ping"}`)
	if m["error"] != nil || !bytes.Contains(m["result"], []byte("true")) {
		t.Fatalf("failed shim round trip %s", m)
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("relay cleanup hung")
	}
	for _, p := range []string{path, path + ".native", filepath.Dir(s.backend)} {
		if _, e = os.Stat(p); !os.IsNotExist(e) {
			t.Fatalf("left relay artifact %s", p)
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shim did not stop with session")
	}
}

func TestUnsafeDirectoryRejected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unsafe")
	os.Mkdir(dir, 0755)
	if s, e := StartSession(filepath.Join(dir, "socket")); e == nil {
		s.Close()
		t.Fatal("accepted non-private directory")
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(t.TempDir(), link)
	if s, e := StartSession(filepath.Join(link, "socket")); e == nil {
		s.Close()
		t.Fatal("accepted symlink directory")
	}
}

func TestInteractiveRegistration(t *testing.T) {
	// Short HOME avoids Unix sockaddr path length limits in deeply named tests.
	home, e := os.MkdirTemp("", "coder-home-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	exe := "/usr/local/bin/coder"
	s, e := StartInteractive(exe)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	paths, e := Register(exe)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		var m struct {
			Path    string   `json:"path"`
			Allowed []string `json:"allowed_origins"`
		}
		json.Unmarshal(b, &m)
		if m.Path != exe || len(m.Allowed) != 1 || m.Allowed[0] != Origin {
			t.Fatalf("bad registration %s", b)
		}
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0600 {
			t.Fatal("manifest permissions")
		}
	}
	// Uninstalling another binary cannot remove this installation's manifests.
	if e = Unregister("/other/coder"); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(paths[0]); e != nil {
		t.Fatal(e)
	}
	if e = Unregister(exe); e != nil {
		t.Fatal(e)
	}
	for _, p := range paths {
		if _, e = os.Stat(p); !os.IsNotExist(e) {
			t.Fatal("manifest not removed")
		}
	}
	// Foreign origin fails without consuming input or needing a live relay.
	if e = Serve(strings.TrimSuffix(Origin, "/"), nil, io.Discard, ""); e == nil {
		t.Fatal("origin comparison not exact")
	}
}
