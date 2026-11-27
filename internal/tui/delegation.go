package tui

import (
	"context"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
	"github.com/tjcoder-labs/cli/internal/tooling"
	"github.com/tjcoder-labs/cli/internal/tools"
	"github.com/tjcoder-labs/cli/internal/tracking"
)

// specialistDelegationRunner keeps the coordinator's history, model, tools
// and session persistence separate from the delegated specialist turn.
type specialistDelegationRunner struct {
	app  *App
	env  tools.ExecEnv
	root string
}

func (r *specialistDelegationRunner) RunDelegation(ctx context.Context, state *session.State, prompt string, specialist agent.Config) ([]client.Message, error) {
	model := specialist.DefaultModel
	if model == "" {
		model = r.app.currentModel
	}
	if r.app.isErgoProvider() {
		model = r.app.sanitizeModel(model)
		if model == "" {
			model = r.app.currentModel
		}
	}
	registry := tools.NewRegistry(r.app.provider)
	registry.RegisterTool(tools.ManageItemsBridge{Impl: r.app.manageItemsFor(state, r.root)})
	// Deliberately no delegate registration/factory: specialist sessions are
	// leaves. Coordinator and specialist transcripts never share history.
	runner := &tooling.Runner{
		Provider: r.app.provider, Registry: registry, WorkspaceRoot: r.root,
		MaxSteps: 12, HighlightSink: r.env.Sink, CLICommandSink: r.env.CommandSink,
		SessionState: state, Sudo: r.env.Sudo, Workspaces: r.env.Workspaces,
		PersistSession: func() error { return nil },
	}
	return runner.Run(ctx, nil, prompt, specialist, model, specialist.ToolNames, func(ev tooling.Event) {
		switch ev.Type {
		case tooling.EventToolStart:
			r.app.postUI(func() { r.app.appendActivity("Ergo → " + specialist.Name + ": " + ev.ToolName + " " + ev.Text) })
		case tooling.EventError:
			r.app.postUI(func() { r.app.appendActivity("Ergo → " + specialist.Name + " error: " + ev.Text) })
		}
	})
}

func newSpecialistDelegationRunner(app *App) tools.DelegationProviderFactory {
	return func(_ context.Context, env tools.ExecEnv, _ agent.Config) tools.DelegationRunner {
		return &specialistDelegationRunner{app: app, env: env, root: env.WorkspaceRoot}
	}
}

func (a *App) manageItemsFor(state *session.State, root string) *tools.ManageItems {
	trackReg := tracking.NewRegistry()
	trackReg.Register(tracking.NewTaskTracker())
	trackReg.Register(tracking.NewMemoryTracker())
	trackReg.Register(tracking.NewReminderTracker(root))
	return tools.NewManageItems(trackReg, state)
}
