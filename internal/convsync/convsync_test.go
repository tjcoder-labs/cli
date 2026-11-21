package convsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func turn(user, reply string) []client.Message {
	return []client.Message{
		{Role: "user", Content: user},
		{Role: "assistant", ToolCalls: []client.ToolCall{{Function: client.ToolFunctionCall{Name: "run_command", Arguments: json.RawMessage(`{"command":"go test ./` + user + `"}`)}}}},
		{Role: "tool", ToolName: "run_command", Content: "ok"},
		{Role: "assistant", Content: reply, PromptEvalCount: 10, EvalCount: 3},
	}
}

func TestPlanFoldsToolResultsAndAdvancesCursor(t *testing.T) {
	h := append([]client.Message{{Role: "system", Content: "sys"}}, turn("fix it", "done")...)
	items, cur := Plan(h, Cursor{SessionID: "s"}, Meta{Provider: "ollama", Model: "qwen3"}, t0)
	if len(items) != 3 {
		t.Fatalf("want 3 items (user, assistant+tool, assistant), got %d: %+v", len(items), items)
	}
	if items[1].ToolCalls[0].Result != "ok" || items[1].ToolCalls[0].Arguments != `{"command":"go test ./fix it"}` {
		t.Fatalf("tool result not folded: %+v", items[1].ToolCalls)
	}
	if items[2].Provider != "ollama" || items[2].Usage == nil || items[2].Usage.InputTokens != 10 {
		t.Fatalf("assistant meta missing: %+v", items[2])
	}
	if cur.NextSeq != 3 || cur.SyncedLen != len(h) {
		t.Fatalf("cursor = %+v", cur)
	}
	if items[0].CreatedAt >= items[2].CreatedAt {
		t.Fatalf("timestamps not monotonic: %s %s", items[0].CreatedAt, items[2].CreatedAt)
	}
	// Nothing new: nothing to send.
	if again, _ := Plan(h, cur, Meta{}, t0); len(again) != 0 {
		t.Fatalf("resent %d items", len(again))
	}
	// Next turn continues the sequence.
	h = append(h, turn("more", "ok again")...)
	more, cur2 := Plan(h, cur, Meta{}, t0)
	if len(more) != 3 || more[0].Seq != 3 || cur2.NextSeq != 6 {
		t.Fatalf("incremental plan wrong: %d items first seq %d next %d", len(more), more[0].Seq, cur2.NextSeq)
	}
}

func TestPlanHoldsBackRunningToolCalls(t *testing.T) {
	h := turn("go", "x")[:2] // assistant called a tool; no result yet
	items, cur := Plan(h, Cursor{SessionID: "s"}, Meta{}, t0)
	if len(items) != 1 || items[0].Role != "user" || cur.SyncedLen != 1 {
		t.Fatalf("should hold back the pending call: %+v %+v", items, cur)
	}
	h = turn("go", "x")
	items, _ = Plan(h, cur, Meta{}, t0)
	if len(items) != 2 || items[0].Seq != 1 || items[0].ToolCalls[0].Result != "ok" {
		t.Fatalf("call + result should follow together: %+v", items)
	}
}

func TestPlanAfterCompactionAppendsSummary(t *testing.T) {
	h := turn("a", "b")
	_, cur := Plan(h, Cursor{SessionID: "s"}, Meta{}, t0)
	compacted := []client.Message{{Role: "user", Content: "[Compaction] summary follows"}, {Role: "assistant", Content: "summary"}}
	items, next := Plan(compacted, cur, Meta{}, t0)
	if len(items) != 2 || items[0].Seq != 3 || next.NextSeq != 5 {
		t.Fatalf("compaction should append with continuing seqs: %+v next=%+v", items, next)
	}
}

func TestPlanRecoversFromServerCursor(t *testing.T) {
	h := turn("a", "b")
	h = append(h, turn("c", "d")...)
	// Server says it has seq 0..1 and the last item was the tool-calling assistant.
	cur := Cursor{SessionID: "s", NextSeq: 2, LastFingerprint: Fingerprint(h[1]), Recovered: true}
	items, _ := Plan(h, cur, Meta{}, t0)
	if len(items) != 4 || items[0].Seq != 2 || items[0].Content != "b" {
		t.Fatalf("recovery should skip folded tool results: %+v", items)
	}
}

func TestRedactMasksSecrets(t *testing.T) {
	in := "OPENAI_API_KEY=supersecretvalue123 Bearer abcdefghijklmnopqrstuvwxyz123456 ghp_abcdefghijklmnopqrstuvwxyz0123 https://api.tjcoder.com/mcp/hosts/8TL7abcdefghijklmnopSHeo"
	out := Redact(in)
	for _, leak := range []string{"supersecretvalue123", "abcdefghijklmnopqrstuvwxyz123456", "ghp_abcdefghijklmnopqrstuvwxyz0123", "8TL7abcdefghijklmnopSHeo"} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q in %q", leak, out)
		}
	}
	for _, want := range []string{"OPENAI_API_KEY=supe…e123", "Bearer abcd…3456", "ghp_…0123", "/mcp/hosts/8TL7…SHeo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in %q", want, out)
		}
	}
	if got := Redact("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"); got != "[REDACTED PRIVATE KEY]" {
		t.Fatalf("private key: %q", got)
	}
	items, _ := Plan([]client.Message{{Role: "user", Content: "my key is sk-proj-ABCDEFGHIJKLMNOPQRSTUV"}}, Cursor{}, Meta{}, t0)
	if strings.Contains(items[0].Content, "ABCDEFGHIJKLMNOPQRSTUV") {
		t.Fatalf("plan did not redact: %q", items[0].Content)
	}
}

// fakeServer mimics /cli/conversations with insert-only seq dedupe.
type fakeServer struct {
	mu       sync.Mutex
	seqs     map[int]Item
	posts    int
	failNext int
	status   int
	auth     string
}

func (f *fakeServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = r.Header.Get("Authorization")
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(`{"error":{"code":"x"}}`))
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found"}}`))
			return
		}
		if f.failNext > 0 {
			f.failNext--
			w.WriteHeader(503)
			return
		}
		f.posts++
		var body struct {
			Messages []Item `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, it := range body.Messages {
			if _, ok := f.seqs[it.Seq]; !ok {
				f.seqs[it.Seq] = it
			}
		}
		_, _ = w.Write([]byte(`{"conversation_id":"cli_abc"}`))
	})
}

func newTestSyncer(t *testing.T, srv *httptest.Server, token string) *Syncer {
	t.Helper()
	t.Setenv(EnvOptOut, "")
	return New(Options{BaseURL: func() string { return srv.URL }, Token: func() string { return token }, Now: func() time.Time { return t0 }})
}

func TestSyncerSendsOnceAndSkipsWhenSignedOut(t *testing.T) {
	f := &fakeServer{seqs: map[int]Item{}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	dir := t.TempDir()
	snap := Snapshot{SessionID: "sess_12345678", CursorPath: CursorPathFor(dir + "/session.json"), History: turn("hi", "hello")}

	// Signed out: nothing sent, nothing lost.
	out := newTestSyncer(t, srv, "")
	if err := out.syncOnce(context.Background(), snap); err != errNotAuthenticated {
		t.Fatalf("want errNotAuthenticated, got %v", err)
	}
	if f.posts != 0 {
		t.Fatalf("posted while signed out")
	}

	s := newTestSyncer(t, srv, "ek_test_token_value")
	for i := 0; i < 3; i++ {
		if err := s.syncOnce(context.Background(), snap); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.seqs) != 3 || f.posts != 1 {
		t.Fatalf("want 3 items in 1 post, got %d items / %d posts", len(f.seqs), f.posts)
	}
	if c, ok := LoadCursor(CursorPathFor(dir + "/session.json")); !ok || c.ConversationID != "cli_abc" || c.NextSeq != 3 {
		t.Fatalf("cursor = %+v", c)
	}
	if f.auth != "Bearer ek_test_token_value" {
		t.Fatalf("auth header not sent")
	}
}

func TestSyncerRetriesOfflineWithoutDuplicates(t *testing.T) {
	f := &fakeServer{seqs: map[int]Item{}, failNext: 1}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	s := newTestSyncer(t, srv, "tok_123456789")
	snap := Snapshot{SessionID: "sess_12345678", CursorPath: CursorPathFor(t.TempDir() + "/session.json"), History: turn("hi", "hello")}
	if err := s.syncOnce(context.Background(), snap); err == nil || !isTransient(err) {
		t.Fatalf("503 should be transient, got %v", err)
	}
	if err := s.syncOnce(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	if len(f.seqs) != 3 {
		t.Fatalf("want 3 items, got %d", len(f.seqs))
	}
}

func TestSyncerOptOutAndAuthFailure(t *testing.T) {
	f := &fakeServer{seqs: map[int]Item{}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	s := newTestSyncer(t, srv, "tok_123456789")
	snap := Snapshot{SessionID: "sess_12345678", CursorPath: CursorPathFor(t.TempDir() + "/session.json"), History: turn("hi", "hello")}
	t.Setenv(EnvOptOut, "off")
	if err := s.syncOnce(context.Background(), snap); err != errDisabled || f.posts != 0 {
		t.Fatalf("opt-out ignored: %v posts=%d", err, f.posts)
	}
	t.Setenv(EnvOptOut, "")
	f.status = 401
	if err := s.syncOnce(context.Background(), snap); err != errNotAuthenticated {
		t.Fatalf("401 should pause sync: %v", err)
	}
	f.status = 0
	if err := s.syncOnce(context.Background(), snap); err != errNotAuthenticated {
		t.Fatalf("same token should stay paused after 401: %v", err)
	}
}

func TestSyncerNotifyRunsInBackground(t *testing.T) {
	f := &fakeServer{seqs: map[int]Item{}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	s := newTestSyncer(t, srv, "tok_123456789")
	dir := t.TempDir()
	s.Notify(Snapshot{SessionID: "sess_12345678", CursorPath: CursorPathFor(dir + "/session.json"), History: turn("hi", "hello")})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.seqs)
		f.mu.Unlock()
		if n == 3 {
			s.Close(time.Second)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background sync never delivered")
}

func TestCursorPathFor(t *testing.T) {
	if got := CursorPathFor("/w/.ergo-cli-go/session.json"); got != "/w/.ergo-cli-go/conversation-sync.json" {
		t.Fatal(got)
	}
	if got := CursorPathFor("/w/.ergo-cli-go/jobs/job1.json"); got != "/w/.ergo-cli-go/jobs/job1.conversation-sync.json" {
		t.Fatal(got)
	}
}
