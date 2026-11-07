package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
		fmt.Fprint(w, `{"id":"session-id","url":"https://api.tjcoder.com/mcp/hosts/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","expiresAt":"2026-11-07T08:00:00Z","relayToken":"relay"}`)
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
			if !strings.HasSuffix(p.URL, strings.Repeat("A", 43)) {
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

func fastRelay(t *testing.T) {
	hb, idle, base := relayHeartbeatEvery, relayStreamIdle, relayBackoffBase
	relayHeartbeatEvery, relayStreamIdle, relayBackoffBase = 20*time.Millisecond, 300*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { relayHeartbeatEvery, relayStreamIdle, relayBackoffBase = hb, idle, base })
}

func TestRelayReconnectsSameSessionWithoutNewURL(t *testing.T) {
	fastRelay(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	creates, connects, deletes := 0, 0, 0
	mux := http.NewServeMux()
	mux.HandleFunc("/cli/mcp/sessions", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		creates++
		mu.Unlock()
		fmt.Fprint(w, `{"id":"sid","url":"https://api.tjcoder.com/mcp/hosts/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","expiresAt":"2999-01-01T00:00:00Z","relayToken":"relay"}`)
	})
	mux.HandleFunc("/cli/mcp/sessions/sid/connect", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connects++
		n := connects
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer relay" {
			t.Error("wrong relay auth")
		}
		if n == 2 {
			w.WriteHeader(502) // transient failure while offline
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": connected\n\ndata: {\"requestId\":\"r1\",\"consumerId\":\"c0ffee00-0000-4000-8000-000000000001\",\"body\":{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}}\n\n")
		w.(http.Flusher).Flush()
		if n == 1 {
			return // dropout
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("/cli/mcp/sessions/sid/heartbeat", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/cli/mcp/sessions/sid/responses/r1", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/cli/mcp/sessions/sid", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		deletes++
		mu.Unlock()
		w.WriteHeader(204)
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	events := make(chan RelayEvent, 32)
	announced := 0
	relay := &Relay{BaseURL: ts.URL, Key: "k", Server: &Server{Registry: &stub{}}, Client: ts.Client(), Events: func(e RelayEvent) { events <- e }}
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx, func(Publication) { announced++ }) }()
	var sawReconnecting, sawConsumer bool
	deadline := time.After(5 * time.Second)
	for wait := true; wait; {
		select {
		case e := <-events:
			switch e.Kind {
			case RelayReconnecting:
				sawReconnecting = true
			case RelayConsumer:
				sawConsumer = e.ConsumerID != ""
			case RelayReconnected:
				wait = false
			}
		case <-deadline:
			t.Fatal("never reconnected")
		}
	}
	mu.Lock()
	if creates != 1 || deletes != 0 || connects < 3 {
		t.Fatalf("creates=%d deletes=%d connects=%d", creates, deletes, connects)
	}
	mu.Unlock()
	if !sawReconnecting || !sawConsumer {
		t.Fatal("missing reconnecting/consumer events")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if announced != 1 || deletes != 1 {
		t.Fatalf("announced=%d deletes=%d", announced, deletes)
	}
}

func TestRelayStopsOnSessionGone(t *testing.T) {
	fastRelay(t)
	mux := http.NewServeMux()
	creates := 0
	mux.HandleFunc("/cli/mcp/sessions", func(w http.ResponseWriter, r *http.Request) {
		creates++
		fmt.Fprint(w, `{"id":"sid","url":"https://api.tjcoder.com/mcp/hosts/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","expiresAt":"2999-01-01T00:00:00Z","relayToken":"relay"}`)
	})
	n := 0
	mux.HandleFunc("/cli/mcp/sessions/sid/connect", func(w http.ResponseWriter, r *http.Request) {
		n++
		if n > 1 {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": connected\n\n")
	})
	mux.HandleFunc("/cli/mcp/sessions/sid/heartbeat", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	relay := &Relay{BaseURL: ts.URL, Key: "k", Server: &Server{Registry: &stub{}}, Client: ts.Client()}
	err := relay.Run(context.Background(), func(Publication) {})
	if !errors.Is(err, errSessionGone) || creates != 1 {
		t.Fatalf("err=%v creates=%d", err, creates)
	}
}
