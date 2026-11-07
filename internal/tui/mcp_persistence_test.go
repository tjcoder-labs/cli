package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rivo/tview"

	"github.com/tjcoder-labs/cli/internal/mcp"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// newTestApp builds a minimal App sufficient to exercise the
// /mcp add|list|remove paths and the config persistence helpers.
// It does not start tv.Run() so it is safe to call from tests.
func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	app := &App{
		tv:            tview.NewApplication(),
		mcpClient:     mcp.NewClient(),
		workspaceRoot: dir,
		palette:       darkPalette(),
		activity:      tview.NewTextView(),
		config:        AppConfig{},
	}
	// build a tiny registry so RegisterMCPClient has something to
	// hang tools on (the bridge only needs a non-nil map).
	app.registry = tools.NewRegistry(nil)
	return app
}

// capturedActivity drains everything written to the activity log so
// tests can assert on messages.
func (a *App) capturedActivity() string {
	if a.activity == nil {
		return ""
	}
	return a.activity.GetText(true)
}

func TestMCPAddPersistsToConfig(t *testing.T) {
	app := newTestApp(t)
	// Avoid hitting a real network in the test by pre-seeding the
	// in-memory client (mirrors what /mcp add would do once a real
	// server's tools/list responds).
	srv := &mcp.Server{Name: "stub", URL: "http://127.0.0.1:1/mcp", IsEnabled: true, Transport: mcp.NewTransport("http://127.0.0.1:1/mcp")}
	app.mcpClient.GetServers()["stub"] = srv

	// Simulate the post-connect portion of /mcp add by hand: in the
	// real handler this is reached only after AddServer succeeds,
	// but the contract under test is the persistence path, so we
	// exercise it directly.
	if app.config.MCPServers == nil {
		app.config.MCPServers = make(map[string]MCPServerConfig)
	}
	app.config.MCPServers["stub"] = MCPServerConfig{Type: "sse", URL: "http://127.0.0.1:1/mcp", Enabled: true}
	if err := app.saveConfig(app.config); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	cfgPath := filepath.Join(app.workspaceRoot, ".ergo-cli-go", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected config.json to be written: %v", err)
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config.json not valid JSON: %v", err)
	}
	sc, ok := cfg.MCPServers["stub"]
	if !ok {
		t.Fatalf("expected MCPServers[stub] in persisted config, got %+v", cfg.MCPServers)
	}
	if sc.URL != "http://127.0.0.1:1/mcp" || sc.Type != "sse" || !sc.Enabled {
		t.Fatalf("unexpected MCPServerConfig: %+v", sc)
	}
}

func TestMCPListRendersAfterAdd(t *testing.T) {
	app := newTestApp(t)
	// We don't actually open a real MCP server in tests; inject a
	// synthetic entry into the in-memory client so the list path
	// has something to render.
	srv := &mcp.Server{Name: "demo", URL: "http://example/mcp", IsEnabled: true, Transport: mcp.NewTransport("http://example/mcp")}
	app.mcpClient.GetServers()["demo"] = srv

	app.handleMCPCommand([]string{"mcp", "list"})
	out := app.capturedActivity()
	if !strings.Contains(out, "demo") || !strings.Contains(out, "http://example/mcp") {
		t.Fatalf("expected list to mention demo + url, got %q", out)
	}
}

func TestMCPRemoveDropsFromConfigAndRegistry(t *testing.T) {
	app := newTestApp(t)
	// Seed config and a synthetic in-memory server + tool so we can
	// verify both map deletion and registry unregistration.
	app.config.MCPServers = map[string]MCPServerConfig{
		"seed": {Type: "sse", URL: "http://example/mcp", Enabled: true},
	}
	if err := app.saveConfig(app.config); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	srv := &mcp.Server{Name: "seed", URL: "http://example/mcp", IsEnabled: true, Transport: mcp.NewTransport("http://example/mcp")}
	app.mcpClient.GetServers()["seed"] = srv
	app.mcpClient.SetToolsForTest("seed", map[string]mcp.ToolDefinition{"ping": {Name: "ping", Description: "ping"}})
	tools.RegisterMCPClient(app.registry, app.mcpClient)

	// Sanity: mcp_seed_ping should be registered.
	if !app.registry.Has("mcp_seed_ping") {
		t.Fatalf("expected mcp_seed_ping to be registered before remove")
	}

	app.handleMCPCommand([]string{"mcp", "remove", "seed"})

	if _, stillThere := app.mcpClient.GetServers()["seed"]; stillThere {
		t.Fatalf("expected server %q to be removed from client", "seed")
	}
	if app.registry.Has("mcp_seed_ping") {
		t.Fatalf("expected mcp_seed_* tools to be unregistered, but mcp_seed_ping is still present")
	}
	data, err := os.ReadFile(filepath.Join(app.workspaceRoot, ".ergo-cli-go", "config.json"))
	if err != nil {
		t.Fatalf("config not persisted: %v", err)
	}
	var cfg AppConfig
	_ = json.Unmarshal(data, &cfg)
	if _, ok := cfg.MCPServers["seed"]; ok {
		t.Fatalf("expected seed to be removed from persisted config, got %+v", cfg.MCPServers)
	}
}

func TestMCPLoadFromConfigSkipsDisabledAndMalformed(t *testing.T) {
	dir := t.TempDir()
	cfg := AppConfig{
		MCPServers: map[string]MCPServerConfig{
			"disabled": {Type: "sse", URL: "http://nope/mcp", Enabled: false},
			"missing":  {Type: "sse", URL: "", Enabled: true},
			"stdio":    {Type: "stdio", Command: filepath.Join(dir, "missing-mcp-executable"), Args: []string{"x"}, Enabled: true},
			// "good" points at a URL that won't respond; we just want
			// to see that the loader attempted it without panicking.
			"good": {Type: "sse", URL: "http://127.0.0.1:1/mcp", Enabled: true},
		},
	}
	app := &App{
		tv:            tview.NewApplication(),
		mcpClient:     mcp.NewClient(),
		workspaceRoot: dir,
		palette:       darkPalette(),
		activity:      tview.NewTextView(),
		config:        cfg,
	}
	app.loadMCPServers()

	// Disabled, malformed and unavailable servers never register tools.
	// Failed attempts remain visible in /mcp status.
	for _, name := range []string{"disabled", "missing", "stdio"} {
		if _, ok := app.mcpClient.GetServers()[name]; ok {
			t.Fatalf("did not expect %q to be registered, but it was", name)
		}
	}
}

// TestMCPStdioTUIHelper is a child process used by the real slash-command path.
func TestMCPStdioTUIHelper(t *testing.T) {
	if os.Getenv("GO_WANT_TUI_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch req.Method {
		case "notifications/initialized":
			continue
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "ping", "description": "Ping the local helper", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "pong"}}}
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	os.Exit(0)
}

func tuiStdioConfig() MCPServerConfig {
	return MCPServerConfig{Type: "stdio", Command: os.Args[0], Args: []string{"-test.run=^TestMCPStdioTUIHelper$", "--", "one argument"}, Env: map[string]string{"GO_WANT_TUI_MCP_HELPER": "1"}, Enabled: true}
}

func TestMCPStdioRestoreListStatusRemove(t *testing.T) {
	app := newTestApp(t)
	defer app.mcpClient.Close()
	app.config.MCPServers = map[string]MCPServerConfig{"local": tuiStdioConfig()}
	if err := app.saveConfig(app.config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(app.workspaceRoot, ".ergo-cli-go", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var restored AppConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.MCPServers, app.config.MCPServers) {
		t.Fatal("stdio config did not round-trip")
	}
	app.config = restored
	app.loadMCPServers()
	if !app.registry.Has("mcp_local_ping") {
		t.Fatal("restored stdio tool was not registered")
	}
	app.handleMCPCommand([]string{"mcp", "list"})
	app.handleMCPCommand([]string{"mcp", "status"})
	output := app.capturedActivity()
	for _, want := range []string{"local", "stdio", "connected", "1 tools", "ping", "Ping the local helper", os.Args[0]} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q in %q", want, output)
		}
	}
	result, err := app.mcpClient.CallTool(context.Background(), "local", "ping", json.RawMessage(`{}`))
	if err != nil || strings.TrimSpace(result) != "pong" {
		t.Fatalf("call=%q err=%v", result, err)
	}
	app.handleMCPCommand([]string{"mcp", "remove", "local"})
	if app.registry.Has("mcp_local_ping") || len(app.mcpClient.GetServers()) != 0 || len(app.config.MCPServers) != 0 {
		t.Fatal("remove retained stdio integration")
	}
}

func TestMCPStdioAddCommandPersistsArgv(t *testing.T) {
	app := newTestApp(t)
	defer app.mcpClient.Close()
	// The CLI doesn't inherit this control flag; supply it via argv to a tiny
	// wrapper test helper instead (Go's -test.run is enough for the add path).
	app.handleMCPCommand([]string{"mcp", "add", "local", os.Args[0], "-test.run=^TestMCPAddStdioHelper$", "--", "argument with spaces"})
	sc, ok := app.config.MCPServers["local"]
	if !ok || sc.Type != "stdio" || sc.Command != os.Args[0] || len(sc.Args) != 3 || sc.Args[2] != "argument with spaces" || !sc.Enabled {
		t.Fatalf("unexpected config: %+v; output=%q", sc, app.capturedActivity())
	}
	data, err := os.ReadFile(filepath.Join(app.workspaceRoot, ".ergo-cli-go", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var restored AppConfig
	_ = json.Unmarshal(data, &restored)
	if !reflect.DeepEqual(restored.MCPServers["local"], sc) {
		t.Fatal("add did not persist stdio argv")
	}
}

func TestMCPAddStdioHelper(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "argument with spaces" {
		return
	}
	_ = os.Setenv("GO_WANT_TUI_MCP_HELPER", "1")
	TestMCPStdioTUIHelper(t)
}

func TestMCPStatusFailedDisabledAndUsage(t *testing.T) {
	app := newTestApp(t)
	app.config.MCPServers = map[string]MCPServerConfig{
		"failed":   {Type: "stdio", Command: filepath.Join(t.TempDir(), "not-an-executable"), Enabled: true},
		"disabled": {Type: "sse", URL: "https://example/mcp", Enabled: false},
	}
	app.loadMCPServers()
	app.handleMCPCommand([]string{"mcp", "status"})
	app.handleMCPCommand([]string{"mcp"})
	output := app.capturedActivity()
	for _, want := range []string{"failed", "stdio", "disconnected", "Last error:", "disabled", "add|list|status|remove"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q in %q", want, output)
		}
	}
	app.handleMCPCommand([]string{"mcp", "remove", "failed"})
	app.handleMCPCommand([]string{"mcp", "remove", "disabled"})
	if len(app.config.MCPServers) != 0 {
		t.Fatal("could not remove failed/disabled config")
	}
}

func TestMCPQuotedArgv(t *testing.T) {
	got, err := splitMCPArgs(`mcp add local "/path with spaces/server" --label 'one argument' "" '$TOKEN' '; echo unsafe'`)
	want := []string{"mcp", "add", "local", "/path with spaces/server", "--label", "one argument", "", "$TOKEN", "; echo unsafe"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("argv=%q err=%v", got, err)
	}
	if _, err := splitMCPArgs(`mcp add local "unclosed`); err == nil {
		t.Fatal("expected quote error")
	}
}

func TestMCPHTTPAddPersistsAndLists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}}
		if req.Method == "tools/list" {
			result = map[string]any{"tools": []any{map[string]any{"name": "remote_ping", "description": "Ping remote", "inputSchema": map[string]any{"type": "object"}}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()
	app := newTestApp(t)
	defer app.mcpClient.Close()
	app.handleMCPCommand([]string{"mcp", "add", "remote", server.URL})
	if sc := app.config.MCPServers["remote"]; sc.Type != "sse" || sc.URL != server.URL || !sc.Enabled {
		t.Fatalf("HTTP config regressed: %+v", sc)
	}
	if !app.registry.Has("mcp_remote_remote_ping") {
		t.Fatal("remote tool not registered")
	}
	app.handleMCPCommand([]string{"mcp", "list"})
	if !strings.Contains(app.capturedActivity(), "remote_ping: Ping remote") {
		t.Fatal("remote tool not listed")
	}
}
