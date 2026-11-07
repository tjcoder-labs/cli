package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRelayRoundtripAndRevoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deleted := make(chan struct{}, 1)
	answered := make(chan []byte, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/cli/mcp/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer login-key" || r.Method != "POST" {
			t.Error("wrong creation auth")
		}
		fmt.Fprint(w, `{"id":"session-id","url":"https://api.tjcoder.com/mcp/hosts/session-id","expiresAt":"2026-11-07T08:00:00Z","relayToken":"relay","invocationToken":"consumer"}`)
	})
	mux.HandleFunc("/cli/mcp/sessions/session-id/connect", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer relay" {
			t.Error("wrong relay auth")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"requestId\":\"request-id\",\"body\":{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"echo\"}}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/cli/mcp/sessions/session-id/responses/request-id", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer relay" {
			t.Error("wrong response auth")
		}
		var b json.RawMessage
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			t.Error(e)
		}
		answered <- b
		w.WriteHeader(204)
	})
	mux.HandleFunc("/cli/mcp/sessions/session-id", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.Header.Get("Authorization") != "Bearer relay" {
			t.Error("wrong delete auth")
		}
		deleted <- struct{}{}
		w.WriteHeader(204)
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	relay := &Relay{BaseURL: ts.URL, Key: "login-key", Server: &Server{Registry: &stub{}}, Client: ts.Client()}
	done := make(chan error, 1)
	go func() {
		done <- relay.Run(ctx, func(p Publication) {
			if p.InvocationToken != "consumer" {
				t.Error("bad publication")
			}
		})
	}()
	select {
	case b := <-answered:
		if !strings.Contains(string(b), `"isError":false`) {
			t.Fatalf("%s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing tool result")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("didn't stop")
	}
	select {
	case <-deleted:
	case <-time.After(time.Second):
		t.Fatal("didn't revoke")
	}
}
func TestRelayRequiresTLS(t *testing.T) {
	r := Relay{BaseURL: "http://example.com", Key: "key"}
	if r.Run(context.Background(), func(Publication) {}) == nil {
		t.Fatal("allowed plaintext credential transport")
	}
}
