package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
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
			"stdio":    {Type: "stdio", Command: "npx", Args: []string{"-y", "x"}, Enabled: true},
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

	// disabled and missing should not be registered; stdio is
	// skipped (transport not implemented); "good" will fail to
	// connect but should not panic.
	for _, name := range []string{"disabled", "missing", "stdio"} {
		if _, ok := app.mcpClient.GetServers()[name]; ok {
			t.Fatalf("did not expect %q to be registered, but it was", name)
		}
	}
}
