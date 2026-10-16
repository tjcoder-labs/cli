package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseBody returns a canned SSE stream: one content chunk, one tool-call chunk
// across two fragments, then a final usage chunk (what the gateway appends
// when stream_options.include_usage is set).
const sseBody = "" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"Hello, \"},\"finish_reason\":null}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"world.\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"fn1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]},\"finish_reason\":null}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"type\":\"function\",\"function\":{\"arguments\":\"\\\"x.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
	"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":42,\"completion_tokens\":17,\"total_tokens\":59}}\n\n" +
	"data: [DONE]\n\n"

func TestErgoChatStreamsContentToolsAndUsage(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		b, _ := json.Marshal(req["stream_options"])
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	e := NewErgo(srv.URL, "ergo_test_key", 0)
	var streamed strings.Builder
	msg, err := e.Chat(context.Background(), ChatRequest{
		Model:    "gpt-4o-mini",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}, func(ev StreamEvent) error {
		streamed.WriteString(ev.Commentary)
		return nil
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if gotAuth != "Bearer ergo_test_key" {
		t.Fatalf("expected bearer header, got %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"include_usage":true`) {
		t.Fatalf("expected include_usage in stream_options, got %s", gotBody)
	}
	if msg.Content != "Hello, world." || streamed.String() != "Hello, world." {
		t.Fatalf("content mismatch: msg=%q streamed=%q", msg.Content, streamed.String())
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Function.Name != "read_file" {
		t.Fatalf("tool name mismatch: %q", tc.Function.Name)
	}
	if string(tc.Function.Arguments) != `{"path":"x.txt"}` {
		t.Fatalf("tool args mismatch: %s", string(tc.Function.Arguments))
	}
	if msg.PromptEvalCount != 42 || msg.EvalCount != 17 {
		t.Fatalf("usage mismatch: prompt=%d out=%d", msg.PromptEvalCount, msg.EvalCount)
	}
}

func TestErgoChatMaps402(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits"}}`))
	}))
	defer srv.Close()

	e := NewErgo(srv.URL, "k", 0)
	_, err := e.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsInsufficientCredits(err) {
		t.Fatalf("expected *InsufficientCreditsError, got %T: %v", err, err)
	}
}

func TestErgoChatMaps403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API key"}}`))
	}))
	defer srv.Close()

	e := NewErgo(srv.URL, "k", 0)
	_, err := e.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err == nil || !IsErgoAuthError(err) {
		t.Fatalf("expected *ErgoAuthError, got %T: %v", err, err)
	}
}

func TestErgoChatNoKeyFailsFast(t *testing.T) {
	e := NewErgo("http://127.0.0.1:0", "", 0)
	_, err := e.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err == nil || !IsErgoAuthError(err) {
		t.Fatalf("expected ErgoAuthError, got %v", err)
	}
}

func TestErgoToolMessageWireShape(t *testing.T) {
	msg := toErgoMessage(Message{Role: "tool", Content: `{"ok":true}`, ToolName: "read_file"})
	if msg.Role != "tool" || msg.ToolCallID != "read_file" {
		t.Fatalf("tool message mismatch: %+v", msg)
	}

	assistant := toErgoMessage(Message{
		Role:      "assistant",
		ToolCalls: []ToolCall{{Function: ToolFunctionCall{Name: "shell", Arguments: json.RawMessage(`{"cmd":"ls"}`)}}},
	})
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Function.Arguments != `{"cmd":"ls"}` {
		t.Fatalf("assistant tool calls mismatch: %+v", assistant.ToolCalls)
	}
}

func TestErgoListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"},{"id":"claude-sonnet-4"}]}`))
	}))
	defer srv.Close()

	e := NewErgo(srv.URL, "k", 0)
	models, err := e.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].Name != "gpt-4o-mini" {
		t.Fatalf("models mismatch: %+v", models)
	}
}

func TestErgoContextWindow(t *testing.T) {
	e := NewErgo("http://example.com", "k", 0)
	n, err := e.ContextWindow(context.Background(), "gpt-4o-mini")
	if err != nil || n != 128000 {
		t.Fatalf("got %d, err %v", n, err)
	}
	n, _ = e.ContextWindow(context.Background(), "totally-unknown-model")
	if n != 0 {
		t.Fatalf("expected 0 for unknown model, got %d", n)
	}
}
