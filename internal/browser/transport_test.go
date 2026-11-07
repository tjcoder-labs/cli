package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/tjcoder-labs/cli/internal/nativehost"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRawCDPWireCompatibility(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no native relay/extension exists for CDP
	var server *httptest.Server
	upgrader := websocket.Upgrader{}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"})
			return
		}
		ws, e := upgrader.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer ws.Close()
		for {
			var req map[string]any
			if ws.ReadJSON(&req) != nil {
				return
			}
			if _, ok := req["transport"]; ok {
				t.Error("raw CDP wire gained extension transport field")
			}
			var result any = map[string]any{}
			switch req["method"] {
			case "Target.attachToTarget":
				result = map[string]string{"sessionId": "session-1"}
			case "Runtime.evaluate":
				if req["sessionId"] != "session-1" {
					t.Error("lost session ID")
				}
				result = map[string]any{"result": map[string]any{"value": 2}}
			case "Target.createTarget":
				result = map[string]string{"targetId": "tab-1"}
			}
			ws.WriteJSON(map[string]any{"id": req["id"], "result": result})
		}
	}))
	defer server.Close()
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	port, _ := strconv.Atoi(p)
	c, e := Connect(context.Background(), port)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	sid, e := c.Attach(context.Background(), "tab-1")
	if e != nil || sid != "session-1" {
		t.Fatal(sid, e)
	}
	v, e := Evaluate(context.Background(), c, sid, "1+1", true)
	if e != nil || string(v) != "2" {
		t.Fatal(string(v), e)
	}
	id, e := c.NewTab(context.Background(), "about:blank")
	if e != nil || id != "tab-1" {
		t.Fatal(id, e)
	}
}
func TestNativeClientDispatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := nativehost.SocketPath()
	os.MkdirAll(filepath.Dir(path), 0700)
	l, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	go func() {
		conn, e := l.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		scan := bufio.NewScanner(conn)
		for scan.Scan() {
			var req map[string]any
			json.Unmarshal(scan.Bytes(), &req)
			if req["transport"] != "stealth" {
				t.Error("native tier missing")
			}
			b, _ := json.Marshal(map[string]any{"id": req["id"], "result": map[string]any{"connected": true}})
			conn.Write(append(b, '\n'))
		}
	}()
	c, e := ConnectNative(context.Background(), "stealth")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	b, e := c.Call(context.Background(), "Bridge.ping", nil)
	if e != nil || !strings.Contains(string(b), "true") {
		t.Fatal(string(b), e)
	}
}

func TestStealthUsesInteractiveSession(t *testing.T) {
	home, e := os.MkdirTemp("", "coder-browser-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	s, e := nativehost.StartSession(nativehost.SocketPath())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c, e := ConnectNative(context.Background(), "stealth")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e = c.Call(context.Background(), "Bridge.ping", nil); e == nil || !strings.Contains(e.Error(), "extension not connected") {
		t.Fatalf("expected extension unavailable, got %v", e)
	}
	hostIn, extOut := io.Pipe()
	extIn, hostOut := io.Pipe()
	defer hostIn.Close()
	defer extOut.Close()
	defer extIn.Close()
	defer hostOut.Close()
	go nativehost.Serve(nativehost.Origin, hostIn, hostOut, "")
	nativehost.WriteFrame(extOut, []byte(`{"method":"Bridge.hello"}`))
	go func() {
		for {
			b, e := nativehost.ReadFrame(extIn)
			if e != nil {
				return
			}
			var req map[string]any
			json.Unmarshal(b, &req)
			b, _ = json.Marshal(map[string]any{"id": req["id"], "result": map[string]bool{"connected": true}})
			if nativehost.WriteFrame(extOut, b) != nil {
				return
			}
		}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		b, e := c.Call(context.Background(), "Bridge.ping", nil)
		if e == nil && strings.Contains(string(b), "true") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(string(b), e)
		}
		time.Sleep(time.Millisecond)
	}
}
