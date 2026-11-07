package mcpserver

import (
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
	"github.com/tjcoder-labs/cli/internal/tools"
	"github.com/tjcoder-labs/cli/internal/tracking"
	"github.com/tjcoder-labs/cli/internal/workspace"
	"time"
)

// NewLocalServer builds the default interactive toolset with independent session
// state for standalone stdio/tunnel commands. Live TUI tunnels instead attach
// through the interactive registry adapter, without a second saved session.
func NewLocalServer(abs, key, version string) (*Server, error) {
	provider := client.NewErgo(client.ErgoDefaultBaseURL, key, 2*time.Minute)
	registry := tools.NewRegistry(provider)
	state, _, err := session.Load(abs)
	if err != nil {
		return nil, err
	}
	track := tracking.NewRegistry()
	track.Register(tracking.NewTaskTracker())
	track.Register(tracking.NewMemoryTracker())
	track.Register(tracking.NewReminderTracker(abs))
	registry.RegisterTool(tools.ManageItemsBridge{Impl: tools.NewManageItems(track, &state)})
	env := tools.ExecEnv{WorkspaceRoot: abs, Provider: provider, SessionState: &state, PersistSession: func() error { return session.Save(abs, state) }, Workspaces: workspace.NewSet(abs)}
	server := &Server{Registry: registry, Env: env, Version: version}
	return server, nil
}
