package tooling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

const testBlock = "<tui_context>\nuser: tj\ntranscript_width_cols: 100\n</tui_context>"

func TestApplyTurnContext_OnlyLastUserMessage(t *testing.T) {
	in := []client.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "", ToolCalls: []client.ToolCall{{Type: "function"}}},
		{Role: "tool", Content: "tool out"},
	}
	out := ApplyTurnContext(in, testBlock)
	if out[3].Content != "second\n\n"+testBlock {
		t.Fatalf("last user message not annotated: %q", out[3].Content)
	}
	for i, m := range out {
		if i != 3 && strings.Contains(m.Content, "<tui_context>") {
			t.Fatalf("message %d (%s) unexpectedly annotated: %q", i, m.Role, m.Content)
		}
	}
	if in[3].Content != "second" {
		t.Fatalf("input slice mutated: %q", in[3].Content)
	}
	if got := ApplyTurnContext(in, "  "); got[3].Content != "second" {
		t.Fatalf("empty block should be a no-op, got %q", got[3].Content)
	}
	if got := ApplyTurnContext([]client.Message{{Role: "user"}}, testBlock); got[0].Content != testBlock {
		t.Fatalf("empty user content should become the block, got %q", got[0].Content)
	}
}

func TestWithTurnContext_EmptyIsNoop(t *testing.T) {
	ctx := context.Background()
	if WithTurnContext(ctx, " \n") != ctx {
		t.Fatal("empty block should return the same context")
	}
	if TurnContextFrom(WithTurnContext(ctx, testBlock)) != testBlock {
		t.Fatal("block not round-tripped through context")
	}
	if TurnContextFrom(ctx) != "" {
		t.Fatal("unset context should yield empty block")
	}
}

func countBlocks(msgs []client.Message) (total int, lastUserHas bool) {
	last := -1
	for i, m := range msgs {
		total += strings.Count(m.Content, "<tui_context>")
		if m.Role == "user" {
			last = i
		}
	}
	return total, last >= 0 && strings.Contains(msgs[last].Content, "<tui_context>")
}

// A multi-step turn (tool call → final answer) must carry the block exactly
// once per request, on the latest user message, while older user turns and
// the returned (persisted) history stay clean.
func TestRunner_TurnContextEphemeralAcrossSteps(t *testing.T) {
	provider := &scriptedProvider{responses: []client.Message{
		{Role: "assistant", ToolCalls: []client.ToolCall{{Type: "function", Function: client.ToolFunctionCall{
			Name: "list_directory", Arguments: json.RawMessage(`{"path":".","depth":1}`),
		}}}},
		{Role: "assistant", Content: "done"},
	}}
	runner := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: t.TempDir(), MaxSteps: 4}
	prior := []client.Message{
		{Role: "user", Content: "older question"},
		{Role: "assistant", Content: "older answer"},
	}
	ctx := WithTurnContext(context.Background(), testBlock)
	history, err := runner.Run(ctx, prior, "new question", agent.Config{Name: "t", Prompt: "p"}, "m", []string{"list_directory"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected 2 requests (tool step + final), got %d", len(provider.requests))
	}
	for i, req := range provider.requests {
		total, lastHas := countBlocks(req.Messages)
		if total != 1 || !lastHas {
			t.Fatalf("request %d: want exactly one block on the last user message, got total=%d lastHas=%v", i, total, lastHas)
		}
	}
	for _, m := range history {
		if strings.Contains(m.Content, "<tui_context>") {
			t.Fatalf("returned history leaked metadata into %s message: %q", m.Role, m.Content)
		}
	}
	if prior[0].Content != "older question" {
		t.Fatal("caller history mutated")
	}

	// Next turn without a block (e.g. scheduled/recap turn): nothing attached.
	provider.responses = []client.Message{{Role: "assistant", Content: "ok"}}
	provider.requests = nil
	if _, err := runner.Run(context.Background(), history, "follow up", agent.Config{Name: "t"}, "m", nil, nil); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if total, _ := countBlocks(provider.requests[0].Messages); total != 0 {
		t.Fatalf("turn without context carried %d blocks", total)
	}
}

type capturedBodies struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (c *capturedBodies) add(b []byte) {
	c.mu.Lock()
	c.bodies = append(c.bodies, b)
	c.mu.Unlock()
}

func runWireTurn(t *testing.T, provider client.Provider) {
	t.Helper()
	runner := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: t.TempDir(), MaxSteps: 1}
	prior := []client.Message{{Role: "user", Content: "older question"}, {Role: "assistant", Content: "older answer"}}
	ctx := WithTurnContext(context.Background(), testBlock)
	history, err := runner.Run(ctx, prior, "new question", agent.Config{Name: "t", Prompt: "p"}, "m", nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, m := range history {
		if strings.Contains(m.Content, "<tui_context>") {
			t.Fatalf("history leaked metadata: %q", m.Content)
		}
	}
}

type wireMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func assertWireMessages(t *testing.T, raw []byte) {
	t.Helper()
	var body struct {
		Messages []wireMsg `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode request: %v\n%s", err, raw)
	}
	last := -1
	for i, m := range body.Messages {
		if m.Role == "user" {
			last = i
		}
	}
	if last < 0 {
		t.Fatalf("no user message on the wire: %s", raw)
	}
	if got := body.Messages[last].Content; got != "new question\n\n"+testBlock {
		t.Fatalf("latest user message on the wire = %q", got)
	}
	for i, m := range body.Messages {
		if i != last && strings.Contains(m.Content, "<tui_context>") {
			t.Fatalf("message %d (%s) carried metadata on the wire: %q", i, m.Role, m.Content)
		}
	}
}

func TestTurnContext_OpenAIWireShape(t *testing.T) {
	var got capturedBodies
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got.add(b)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	runWireTurn(t, client.NewOpenAICompatible(srv.URL, "", 10*time.Second))
	if len(got.bodies) != 1 {
		t.Fatalf("expected 1 chat request, got %d", len(got.bodies))
	}
	assertWireMessages(t, got.bodies[0])
}

func TestTurnContext_OllamaWireShape(t *testing.T) {
	var got capturedBodies
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got.add(b)
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, "{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"done\":true}\n")
	}))
	defer srv.Close()
	runWireTurn(t, client.NewOllamaWithTimeout(srv.URL, 10*time.Second))
	if len(got.bodies) != 1 {
		t.Fatalf("expected 1 chat request, got %d", len(got.bodies))
	}
	assertWireMessages(t, got.bodies[0])
}
