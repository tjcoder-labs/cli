package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestMCPStdioHelper is re-executed as a real child, without a shell.
func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	if os.Getenv("MCP_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM, os.Interrupt)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 16*1024*1024)
	initialized, notified := false, false
	version := ""
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(2)
		}
		var result any
		switch req.Method {
		case "initialize":
			if initialized {
				os.Exit(3)
			}
			initialized = true
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
				ClientInfo      struct {
					Version string `json:"version"`
				} `json:"clientInfo"`
			}
			_ = json.Unmarshal(req.Params, &params)
			version = params.ClientInfo.Version
			if params.ProtocolVersion != protocolVersion {
				os.Exit(4)
			}
			result = map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}}
		case "notifications/initialized":
			if !initialized || len(req.ID) != 0 {
				os.Exit(5)
			}
			notified = true
			continue
		case "tools/list":
			if !notified {
				os.Exit(6)
			}
			// Logging on stderr and notifications on stdout must not corrupt replies.
			fmt.Fprintln(os.Stderr, "a log, not JSON-RPC")
			fmt.Println(`{"jsonrpc":"2.0","method":"notifications/message","params":{"data":"hi"}}`)
			result = ListToolsResponse{Tools: []ToolDefinition{{Name: "echo", Description: "Echo a value", InputSchema: map[string]any{"type": "object"}}}}
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			switch params.Name {
			case "block":
				continue
			case "exit":
				os.Exit(0)
			case "error":
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": JSONRPCError{Code: -32602, Message: "bad args"}})
				continue
			}
			details, _ := json.Marshal(map[string]any{"version": version, "secret": os.Getenv("MCP_TEST_SECRET"), "configured": os.Getenv("MCP_CONFIGURED"), "args": os.Args, "input": params.Arguments})
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(details)}}}
		default:
			os.Exit(7)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	if os.Getenv("MCP_IGNORE_TERM") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
}

func helperConfig() ServerConfig {
	return ServerConfig{Type: "stdio", Command: os.Args[0], Args: []string{"-test.run=^TestMCPStdioHelper$", "--", "argument with spaces", "; not a shell"}, Env: map[string]string{"GO_WANT_MCP_HELPER": "1", "MCP_CONFIGURED": "yes"}, Enabled: true}
}

func TestStdioClientHandshakeCallAndRemove(t *testing.T) {
	t.Setenv("MCP_TEST_SECRET", "must-not-leak")
	c := NewClient("0.9.257")
	defer c.Close()
	sc := helperConfig()
	if err := c.AddServerConfig("local", sc); err != nil {
		t.Fatal(err)
	}
	if len(c.GetToolsForServer("local")) != 1 {
		t.Fatal("tool discovery failed")
	}
	status := c.Statuses()["local"]
	if !status.Connected || status.Type != "stdio" || status.ToolCount != 1 {
		t.Fatalf("bad status: %+v", status)
	}
	result, err := c.CallTool(context.Background(), "local", "echo", json.RawMessage(`{"text":"line1\nline2"}`))
	if err != nil {
		t.Fatal(err)
	}
	var details struct {
		Version, Secret, Configured string
		Args                        []string
		Input                       map[string]string
	}
	if err := json.Unmarshal([]byte(result), &details); err != nil {
		t.Fatal(err)
	}
	if details.Secret != "" || details.Configured != "yes" || details.Version != "0.9.257" || details.Input["text"] != "line1\nline2" {
		t.Fatalf("unexpected echo: %+v", details)
	}
	if !reflect.DeepEqual(details.Args[1:], sc.Args) {
		t.Fatalf("argv changed: %q", details.Args)
	}
	tr := c.GetServers()["local"].Transport.(*StdioTransport)
	if err := c.RemoveServer("local"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tr.exited:
	default:
		t.Fatal("child was not reaped")
	}
	if len(c.GetServers()) != 0 || len(c.Statuses()) != 0 {
		t.Fatal("remove retained server")
	}
}

func TestStdioConcurrentCallsAndErrors(t *testing.T) {
	c := NewClient()
	defer c.Close()
	if err := c.AddServerConfig("local", helperConfig()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.CallTool(context.Background(), "local", "echo", json.RawMessage(`{}`)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := c.CallTool(context.Background(), "local", "error", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "bad args") {
		t.Fatalf("expected RPC error: %v", err)
	}
	if !c.Statuses()["local"].Connected {
		t.Fatal("RPC error incorrectly disconnected a live server")
	}
	if c.Statuses()["local"].LastError == "" {
		t.Fatal("last call error missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.CallTool(ctx, "local", "block", json.RawMessage(`{}`)); err != context.DeadlineExceeded {
		t.Fatalf("expected deadline: %v", err)
	}
	if _, err := c.CallTool(context.Background(), "local", "echo", json.RawMessage(`{}`)); err != nil {
		t.Fatal("call after cancellation:", err)
	}
	if c.Statuses()["local"].LastError != "" {
		t.Fatal("successful call did not clear error")
	}
}

func TestStdioExitAndShutdown(t *testing.T) {
	c := NewClient()
	if err := c.AddServerConfig("local", helperConfig()); err != nil {
		t.Fatal(err)
	}
	tr := c.GetServers()["local"].Transport.(*StdioTransport)
	if _, err := c.CallTool(context.Background(), "local", "exit", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected exit error")
	}
	if c.Statuses()["local"].Connected {
		t.Fatal("dead child marked connected")
	}
	_ = c.Close()
	_ = c.Close()
	select {
	case <-tr.exited:
	default:
		t.Fatal("child not reaped")
	}
}

func TestStdioFailedSpawnStatus(t *testing.T) {
	c := NewClient()
	defer c.Close()
	if err := c.AddServer("missing", "/nonexistent/mcp-command"); err == nil {
		t.Fatal("expected spawn failure")
	}
	status := c.Statuses()["missing"]
	if status.Connected || status.LastError == "" || status.Type != "stdio" {
		t.Fatalf("bad failure status: %+v", status)
	}
	if err := c.RemoveServer("missing"); err != nil {
		t.Fatal(err)
	}
}

func TestStdioEnvironmentAllowlist(t *testing.T) {
	t.Setenv("PATH", "/safe/bin")
	t.Setenv("PRIVATE_TOKEN", "not inherited")
	env := strings.Join(stdioEnvironment(map[string]string{"EXPLICIT": "value"}), "\n")
	if !strings.Contains(env, "PATH=/safe/bin") || !strings.Contains(env, "EXPLICIT=value") || strings.Contains(env, "PRIVATE_TOKEN=") {
		t.Fatal("environment policy violated")
	}
}

func TestStdioForcedCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal escalation")
	}
	c := NewClient()
	defer c.Close()
	sc := helperConfig()
	sc.Env["MCP_IGNORE_TERM"] = "1"
	if err := c.AddServerConfig("stubborn", sc); err != nil {
		t.Fatal(err)
	}
	tr := c.GetServers()["stubborn"].Transport.(*StdioTransport)
	// This helper ignores TERM and remains alive after stdin EOF.
	if err := c.RemoveServer("stubborn"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tr.exited:
	default:
		t.Fatal("stubborn child survived remove")
	}
}
