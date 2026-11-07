package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

type stub struct{ calls int }

func (s *stub) Names() []string { return []string{"echo"} }
func (s *stub) Definitions([]string) []client.ToolDefinition {
	return []client.ToolDefinition{{Function: client.FunctionDefinition{Name: "echo", Description: "test", Parameters: map[string]any{"type": "object"}}}}
}
func (s *stub) Execute(_ context.Context, name string, args json.RawMessage, env tools.ExecEnv) (tools.Result, error) {
	s.calls++
	if string(args) == `{"fail":true}` {
		return tools.Result{}, fmt.Errorf("denied")
	}
	return tools.Result{Content: env.WorkspaceRoot}, nil
}
func TestStdio(t *testing.T) {
	reg := &stub{}
	s := &Server{Registry: reg, Env: tools.ExecEnv{WorkspaceRoot: "workspace"}, Version: "test"}
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"fail":true}}}
`
	var out bytes.Buffer
	if err := s.ServeStdio(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || reg.calls != 2 {
		t.Fatalf("%s calls=%d", out.String(), reg.calls)
	}
	for i, want := range []string{`"protocolVersion":"2024-11-05"`, `"inputSchema"`, `"text":"workspace"`, `"isError":true`} {
		if !strings.Contains(lines[i], want) {
			t.Fatal(lines[i])
		}
	}
}
func TestHTTP(t *testing.T) {
	s := &Server{Registry: &stub{}}
	for _, tc := range []struct {
		method, token, origin, body string
		status                      int
	}{
		{"POST", "", "", `{}`, 401}, {"POST", "bad", "", `{}`, 401}, {"POST", "secret", "https://evil.test", `{}`, 403},
		{"GET", "secret", "", "", 405}, {"POST", "secret", "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, 200},
		{"POST", "secret", "", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, 202},
		{"POST", "secret", "", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, 200},
		{"POST", "secret", "", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo"}}`, 200},
		{"POST", "secret", "", strings.Repeat("x", MaxMessage+1), 413},
	} {
		r := httptest.NewRequest(tc.method, "http://localhost/mcp", strings.NewReader(tc.body))
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		s.Handler("secret").ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.Handler("").ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != 401 {
		t.Fatal("empty token accepted")
	}
}
func TestValidation(t *testing.T) {
	reg := &stub{}
	s := &Server{Registry: reg}
	for _, body := range []string{`no`, `[]`, `{"jsonrpc":"2.0","id":{},"method":"tools/list"}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"unknown"}}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":[]}}`} {
		b := s.Handle(context.Background(), []byte(body))
		if !bytes.Contains(b, []byte(`"error"`)) {
			t.Fatalf("accepted %s: %s", body, b)
		}
	}
	if reg.calls != 0 {
		t.Fatal("invalid calls executed")
	}
}

func TestCallPersistsState(t *testing.T) {
	saved := 0
	s := &Server{Registry: &stub{}, Env: tools.ExecEnv{PersistSession: func() error { saved++; return nil }}}
	s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`))
	if saved != 1 {
		t.Fatal("session state not persisted")
	}
}
