package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// stubHandler returns an http.Handler that:
//   - responds to `initialize` with a minimal capabilities payload
//   - responds to `notifications/initialized` with 202 Accepted
//   - delegates every other method to `dispatch`, which the test
//     supplies so it can assert on the request body and shape a
//     response.
// All SSE responses are flushed so the client doesn't block waiting
// for buffered bytes.
func stubHandler(dispatch func(body string) (contentType string, payload string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		w.Header().Set("Cache-Control", "no-cache")
		switch {
		case strings.Contains(s, `"method":"initialize"`):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"stub","version":"0.0.1"}}}`)
		case strings.Contains(s, `"method":"notifications/initialized"`):
			w.WriteHeader(http.StatusAccepted)
		default:
			ct, payload := dispatch(s)
			w.Header().Set("Content-Type", ct)
			_, _ = io.WriteString(w, payload)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
}

// TestTransportHeaders verifies the transport sends the headers
// that MCP Streamable HTTP servers require to accept the request.
func TestTransportHeaders(t *testing.T) {
	var captured http.Header
	var captureOnce sync.Once
	inner := stubHandler(func(_ string) (string, string) {
		return "text/event-stream", "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n"
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captureOnce.Do(func() { captured = r.Header.Clone() })
		inner.ServeHTTP(w, r)
	}))
	defer ts.Close()

	tr := NewTransport(ts.URL)
	var out json.RawMessage
	if err := tr.SendRequest(context.Background(), "tools/list", nil, &out); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if captured == nil {
		t.Fatal("never captured request headers")
	}

	if got := captured.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", got)
	}
	if got := captured.Get("Accept"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("Accept: got %q, want it to include text/event-stream", got)
	}
	if got := captured.Get("MCP-Protocol-Version"); got == "" {
		t.Errorf("MCP-Protocol-Version header missing")
	}
	if got := captured.Get("User-Agent"); got == "" {
		t.Errorf("User-Agent header missing")
	}
}

// TestTransportSSEResponse verifies the transport parses
// `text/event-stream` framing correctly.
func TestTransportSSEResponse(t *testing.T) {
	ts := httptest.NewServer(stubHandler(func(_ string) (string, string) {
		return "text/event-stream", "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[{\"name\":\"hi\"}]}}\n\n"
	}))
	defer ts.Close()

	tr := NewTransport(ts.URL)
	var resp ListToolsResponse
	if err := tr.SendRequest(context.Background(), "tools/list", nil, &resp); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if len(resp.Tools) != 1 || resp.Tools[0].Name != "hi" {
		t.Fatalf("expected one tool named hi, got %+v", resp.Tools)
	}
}

// TestTransportJSONFallback verifies the transport still works for
// servers (or test fixtures) that return plain JSON instead of SSE.
func TestTransportJSONFallback(t *testing.T) {
	ts := httptest.NewServer(stubHandler(func(_ string) (string, string) {
		return "application/json", `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`
	}))
	defer ts.Close()

	tr := NewTransport(ts.URL)
	var resp ListToolsResponse
	if err := tr.SendRequest(context.Background(), "tools/list", nil, &resp); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if len(resp.Tools) != 0 {
		t.Fatalf("expected no tools, got %+v", resp.Tools)
	}
}

// TestTransportInitializeHandshake verifies the first SendRequest
// triggers an `initialize` round-trip followed by a
// `notifications/initialized` notification, and that subsequent
// calls skip the handshake.
func TestTransportInitializeHandshake(t *testing.T) {
	var initCount, notifCount, toolsListCount int32
	inner := stubHandler(func(_ string) (string, string) {
		atomic.AddInt32(&toolsListCount, 1)
		return "application/json", `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Re-inject the body for the wrapped handler.
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		s := string(body)
		switch {
		case strings.Contains(s, `"method":"initialize"`):
			atomic.AddInt32(&initCount, 1)
		case strings.Contains(s, `"method":"notifications/initialized"`):
			atomic.AddInt32(&notifCount, 1)
		}
		inner.ServeHTTP(w, r)
	}))
	defer ts.Close()

	tr := NewTransport(ts.URL)
	var resp ListToolsResponse
	if err := tr.SendRequest(context.Background(), "tools/list", nil, &resp); err != nil {
		t.Fatalf("first SendRequest: %v", err)
	}
	// Second call should not re-handshake.
	if err := tr.SendRequest(context.Background(), "tools/list", nil, &resp); err != nil {
		t.Fatalf("second SendRequest: %v", err)
	}

	if got := atomic.LoadInt32(&initCount); got != 1 {
		t.Errorf("initialize: got %d calls, want 1", got)
	}
	if got := atomic.LoadInt32(&notifCount); got != 1 {
		t.Errorf("notifications/initialized: got %d calls, want 1", got)
	}
	if got := atomic.LoadInt32(&toolsListCount); got != 2 {
		t.Errorf("tools/list: got %d calls, want 2", got)
	}
}

// TestTransportNotFound verifies the transport surfaces a clear
// error when the endpoint doesn't exist.
func TestTransportNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "not found")
	}))
	defer ts.Close()

	tr := NewTransport(ts.URL)
	var resp ListToolsResponse
	err := tr.SendRequest(context.Background(), "tools/list", nil, &resp)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}
