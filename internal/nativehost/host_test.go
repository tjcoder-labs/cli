package nativehost

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFraming(t *testing.T) {
	var b bytes.Buffer
	want := []byte(`{"x":1}`)
	if e := WriteFrame(&b, want); e != nil {
		t.Fatal(e)
	}
	got, e := ReadFrame(&b)
	if e != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s %v", got, e)
	}
	for _, n := range []uint32{0, MaxFrame + 1} {
		b.Reset()
		binary.Write(&b, binary.LittleEndian, n)
		if _, e = ReadFrame(&b); e == nil {
			t.Fatal("accepted bad length")
		}
	}
	b.Reset()
	binary.Write(&b, binary.LittleEndian, uint32(4))
	b.WriteString("{")
	if _, e = ReadFrame(&b); e == nil {
		t.Fatal("accepted truncated frame")
	}
}
func TestRejectOrigin(t *testing.T) {
	if Serve("chrome-extension://evil/", bytes.NewReader(nil), io.Discard, filepath.Join(t.TempDir(), "sock")) == nil {
		t.Fatal("accepted bad origin")
	}
}
func startFake(t *testing.T, path string, incognito bool) (func(), <-chan error) {
	t.Helper()
	hostIn, extOut := io.Pipe()
	extIn, hostOut := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve("chrome-extension://"+ExtensionID+"/", hostIn, hostOut, path) }()
	hello, _ := json.Marshal(map[string]any{"method": "Bridge.hello", "params": map[string]bool{"incognito": incognito}})
	if e := WriteFrame(extOut, hello); e != nil {
		t.Fatal(e)
	}
	go func() {
		for {
			b, e := ReadFrame(extIn)
			if e != nil {
				return
			}
			var r request
			json.Unmarshal(b, &r)
			result := map[string]any{"echo": string(r.Params)}
			if r.Method == "Target.getTargets" {
				key := "normal"
				if incognito {
					key = "incognito:1"
				}
				result = map[string]any{"targetInfos": []any{map[string]any{"targetId": key}}}
			}
			b, _ = json.Marshal(map[string]any{"id": r.ID, "result": result})
			if WriteFrame(extOut, b) != nil {
				return
			}
		}
	}()
	socket := path
	if incognito {
		socket += ".incognito"
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e := os.Stat(socket); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() { extOut.Close(); extIn.Close(); hostOut.Close(); hostIn.Close() }, done
}
func rpc(t *testing.T, path string, msg string) map[string]json.RawMessage {
	t.Helper()
	c, e := net.Dial("unix", path)
	if e != nil {
		t.Error(e)
		return nil
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	io.WriteString(c, msg+"\n")
	var m map[string]json.RawMessage
	if e = json.NewDecoder(c).Decode(&m); e != nil {
		t.Error(e)
	}
	return m
}
func TestCorrelationAndPrivateRouting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser.sock")
	close1, d1 := startFake(t, path, false)
	defer func() { close1(); <-d1 }()
	close2, d2 := startFake(t, path, true)
	defer func() { close2(); <-d2 }()
	if info, e := os.Stat(path); e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("socket not private")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := rpc(t, path, `{"id":1,"method":"echo","params":{"test":true}}`)
			if string(m["id"]) != "1" || m["error"] != nil {
				t.Errorf("bad correlation: %s", m)
			}
		}()
	}
	wg.Wait()
	m := rpc(t, path, `{"id":9,"method":"Runtime.evaluate","sessionId":"incognito:1","params":{"expression":"1+1"}}`)
	if m["error"] != nil {
		t.Fatal(string(m["error"]))
	}
	m = rpc(t, path, `{"id":10,"method":"Target.getTargets"}`)
	if !bytes.Contains(m["result"], []byte("incognito:1")) || !bytes.Contains(m["result"], []byte("normal")) {
		t.Fatal("list did not merge contexts")
	}
	// A second same-profile host must not replace the existing socket.
	var hello bytes.Buffer
	WriteFrame(&hello, []byte(`{"method":"Bridge.hello"}`))
	if e := Serve("chrome-extension://"+ExtensionID+"/", bufio.NewReader(&hello), io.Discard, path); e == nil {
		t.Fatal("replaced live host")
	}
}
func TestRegisterExactOrigin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	paths, e := Register("/tmp/coder-browser-host")
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		var m struct {
			Allowed []string `json:"allowed_origins"`
		}
		json.Unmarshal(b, &m)
		if len(m.Allowed) != 1 || m.Allowed[0] != "chrome-extension://"+ExtensionID+"/" {
			t.Fatal("origin mismatch")
		}
	}
}
