package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjcoder-labs/cli/internal/mcp"
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
	// CompactModel is the model used for /compact (transcript
	// summarization). When empty, the current model is used. This
	// lets the user pick a cheaper / faster model for compaction
	// while keeping a more capable model for the main conversation.
	CompactModel string `json:"compactModel,omitempty"`
	// AutoCompactPercent is the context-window fill percentage at which
	// the app automatically compacts history when a turn settles. 0
	// (the default) disables automatic compaction; a positive value
	// (e.g. 80) triggers /compact automatically once the conversation
	// passes that threshold.
	AutoCompactPercent int `json:"autoCompactPercent,omitempty"`
	// Timezone is the IANA timezone name (e.g. "America/Denver") used for
	// rendering timestamps in the TUI. If empty, the system's local zone
	// is used.
	Timezone string `json:"timezone,omitempty"`
	// MCPServers is the persisted set of Model Context Protocol
	// integrations. Each entry maps a stable name (used by /mcp
	// add|list|status|remove|tunnel and by the mcp_{server}_{tool} tool
	// namespace) onto the connection details. The /mcp slash
	// command mutates this map; the /config JSON editor round-trips
	// it verbatim.
	MCPServers map[string]MCPServerConfig `json:"mcp_servers,omitempty"`
	// CaptureDir is the directory where /screenshot and /capture save
	// their media. An empty string means the default (~/Downloads).
	CaptureDir string `json:"captureDir,omitempty"`
}

// MCPServerConfig persists either an HTTP endpoint or a local argv and optional
// explicitly supplied environment. Enabled entries reconnect at startup.
type MCPServerConfig = mcp.ServerConfig

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
