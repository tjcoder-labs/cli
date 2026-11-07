// Package mcpserver publishes a session's existing tools, without an LLM or a
// second execution policy. Possession of the transport capability is consent
// to use the advertised tools; existing workspace and sudo gates still apply.
package mcpserver

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

const MaxMessage = 1024 * 1024

type Registry interface {
	Names() []string
	Definitions([]string) []client.ToolDefinition
	Execute(context.Context, string, json.RawMessage, tools.ExecEnv) (tools.Result, error)
}
type Server struct {
	Registry Registry
	Env      tools.ExecEnv
	Version  string
	// OnToolCall observes accepted tool calls without changing their response.
	// It is opt-in: stdio and interactive hosts leave it nil. The returned
	// callback sees the final result (including persistence errors).
	OnToolCall func(string, json.RawMessage) func(tools.Result, error)
	mu         sync.Mutex // session state and tools have the same sequential semantics as TUI
}
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func reply(id json.RawMessage, result any, code int, message string) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	v := map[string]any{"jsonrpc": "2.0", "id": id}
	if code != 0 {
		v["error"] = map[string]any{"code": code, "message": message}
	} else {
		v["result"] = result
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) > MaxMessage {
		return []byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"error":{"code":-32603,"message":"result exceeds transport limit"}}`)
	}
	return b
}

// Handle is shared by stdio, HTTP and the outbound relay. nil means notification.
func (s *Server) Handle(ctx context.Context, data []byte) []byte {
	var r request
	if len(data) > MaxMessage || json.Unmarshal(data, &r) != nil {
		return reply(nil, nil, -32700, "invalid JSON")
	}
	if r.JSONRPC != "2.0" || r.Method == "" {
		return reply(r.ID, nil, -32600, "invalid request")
	}
	if len(r.ID) == 0 {
		return nil
	}
	var id any
	if json.Unmarshal(r.ID, &id) != nil {
		return reply(nil, nil, -32600, "invalid id")
	}
	switch id.(type) {
	case string, float64, nil:
	default:
		return reply(nil, nil, -32600, "invalid id")
	}
	if err := ctx.Err(); err != nil {
		return reply(r.ID, nil, -32603, "session ended")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return reply(r.ID, nil, -32603, "session ended")
	}
	switch r.Method {
	case "initialize":
		return reply(r.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "coder", "version": s.Version}}, 0, "")
	case "ping":
		return reply(r.ID, map[string]any{}, 0, "")
	case "tools/list":
		defs := s.Registry.Definitions(s.Registry.Names())
		list := make([]any, 0, len(defs))
		for _, d := range defs {
			f := d.Function
			list = append(list, map[string]any{"name": f.Name, "description": f.Description, "inputSchema": f.Parameters})
		}
		return reply(r.ID, map[string]any{"tools": list}, 0, "")
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(r.Params, &p) != nil || p.Name == "" {
			return reply(r.ID, nil, -32602, "name and object arguments required")
		}
		if len(p.Arguments) == 0 {
			p.Arguments = json.RawMessage(`{}`)
		}
		var obj map[string]any
		if json.Unmarshal(p.Arguments, &obj) != nil || obj == nil {
			return reply(r.ID, nil, -32602, "arguments must be an object")
		}
		found := false
		for _, n := range s.Registry.Names() {
			if n == p.Name {
				found = true
				break
			}
		}
		if !found {
			return reply(r.ID, nil, -32602, "unknown tool")
		}
		var finished func(tools.Result, error)
		if s.OnToolCall != nil {
			finished = s.OnToolCall(p.Name, p.Arguments)
		}
		result, err := s.Registry.Execute(ctx, p.Name, p.Arguments, s.Env)
		// The model runner persists after a turn; an MCP call is one tool turn.
		if s.Env.PersistSession != nil {
			if saveErr := s.Env.PersistSession(); saveErr != nil && err == nil {
				err = fmt.Errorf("persist session: %w", saveErr)
			}
		}
		if finished != nil {
			finished(result, err)
		}
		text := result.Content
		if err != nil {
			text = fmt.Sprintf("tool %s failed: %v", p.Name, err)
		}
		return reply(r.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": err != nil}, 0, "")
	default:
		return reply(r.ID, nil, -32601, "method not found")
	}
}
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), MaxMessage)
	for scan.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b := s.Handle(ctx, scan.Bytes())
		if b != nil {
			if _, err := fmt.Fprintln(out, string(b)); err != nil {
				return err
			}
		}
	}
	return scan.Err()
}

// Handler is safe only with a capability and an owner-only/loopback listener.
// An empty capability fails closed. Browser-origin requests are not permitted.
func (s *Server) Handler(token string) http.Handler {
	expected := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if token == "" || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origin forbidden", 403)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxMessage)
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "request too large", 413)
			return
		}
		result := s.Handle(r.Context(), b)
		if result == nil {
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})
}
