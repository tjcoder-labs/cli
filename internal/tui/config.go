package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AppConfig is the persisted user-tunable configuration for the
// coder-cli TUI. It is intentionally small and JSON-friendly so the
// /config in-app editor can round-trip it without a schema layer.
//
// Only the fields the user can tweak from inside the TUI live here;
// transient session state (history, references, etc.) is stored in
// session.go alongside the transcript.
type AppConfig struct {
	// ToolMax is the upper bound on the number of tool invocations
	// the agent runner is allowed to make in a single turn. It maps
	// onto tooling.Runner.MaxSteps.
	ToolMax int `json:"toolMax"`
	// UserMessageMaxWidth caps how wide a user message bubble may
	// grow in cells before it is word-wrapped. Zero means "use the
	// layout-derived default" (the conversation body's current
	// width), which is what most users want. Set it explicitly to
	// force a fixed cap regardless of terminal size — useful for
	// projecting / screencasts where the bubble width should stay
	// predictable across machines.
	UserMessageMaxWidth int `json:"userMessageMaxWidth,omitempty"`
	// Model is the default model to select on launch. An empty string
	// means "use the active agent's DefaultModel".
	Model string `json:"model,omitempty"`
	// Agent is the default agent to select on launch. An empty
	// string means "use the first agent".
	Agent string `json:"agent,omitempty"`
	// MCPServers is the persisted set of Model Context Protocol
	// integrations. Each entry maps a stable name (used by /mcp
	// add|list|remove and by the mcp_{server}_{tool} tool
	// namespace) onto the connection details. The /mcp slash
	// command mutates this map; the /config JSON editor round-trips
	// it verbatim.
	MCPServers map[string]MCPServerConfig `json:"mcp_servers,omitempty"`
}

// MCPServerConfig is the on-disk shape of a single MCP integration.
// `Type` is "sse" (HTTP) or "stdio" (local process). For SSE, URL is
// required; for stdio, Command + Args describe how to launch the
// process. The current transport in internal/mcp only implements
// HTTP/SSE — stdio is parsed but not yet wired up; see MCP_SPEC.md.
type MCPServerConfig struct {
	Type    string   `json:"type"`              // "sse" or "stdio"
	URL     string   `json:"url,omitempty"`     // for SSE
	Command string   `json:"command,omitempty"` // for stdio
	Args    []string `json:"args,omitempty"`    // for stdio
	Enabled bool     `json:"enabled"`
}

// DefaultConfig returns a sane, zero-value config used when no
// config file exists yet.
func DefaultConfig() AppConfig {
	return AppConfig{
		ToolMax: 8,
	}
}

// configPath returns the on-disk location of the user's config file.
// It lives next to the session file under .ergo-cli-go/ so users have
// a single place to look for both kinds of state.
func (a *App) configPath() string {
	return filepath.Join(a.workspaceRoot, ".ergo-cli-go", "config.json")
}

// loadConfigFromDisk is a free-function wrapper used during App
// construction (before *App exists) that reads the on-disk config or
// returns the defaults if the file is missing/corrupt.
func loadConfigFromDisk(workspaceRoot string) AppConfig {
	cfg := DefaultConfig()
	if workspaceRoot == "" {
		return cfg
	}
	path := filepath.Join(workspaceRoot, ".ergo-cli-go", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig()
	}
	if cfg.ToolMax <= 0 {
		cfg.ToolMax = DefaultConfig().ToolMax
	}
	return cfg
}

// saveConfig writes the config atomically (tmp + rename) so a crash
// mid-write cannot leave a half-written file behind.
func (a *App) saveConfig(cfg AppConfig) error {
	if a.workspaceRoot == "" {
		return fmt.Errorf("no workspace root configured")
	}
	dir := filepath.Dir(a.configPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.configPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.configPath())
}

// formatConfigJSON renders the config as pretty-printed JSON for the
// in-app editor. It surfaces parse errors as a fenced comment block
// inside the editor so the user can still see and fix the bad file.
func formatConfigJSON(cfg AppConfig) string {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

// parseConfigJSON validates a JSON string from the editor and returns
// the decoded config. Empty input returns the defaults rather than an
// error so the user can wipe the file and start fresh.
func parseConfigJSON(raw string) (AppConfig, error) {
	trimmed := trimAll(raw)
	if trimmed == "" {
		return DefaultConfig(), nil
	}
	var cfg AppConfig
	if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
		return cfg, err
	}
	if cfg.ToolMax <= 0 {
		return cfg, fmt.Errorf("toolMax must be > 0 (got %d)", cfg.ToolMax)
	}
	return cfg, nil
}

// trimAll strips leading/trailing whitespace including newlines so
// pasted JSON in the editor is forgiving.
func trimAll(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
