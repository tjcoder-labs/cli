package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/mcpserver"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// All tunnel UI state is owned by the UI goroutine. Relay credentials and the
// URL are never appended to activity/history (both are persisted).
type interactiveMCPTunnel struct {
	cancel   context.CancelFunc
	done     chan struct{}
	ready    bool
	stopping bool
	url      string
	expires  string
	revealed bool
	notice   string
}

func (a *App) handleMCPTunnel(args []string) {
	if len(args) > 1 || (len(args) == 1 && args[0] != "stop") {
		a.appendActivity("Usage: /mcp tunnel [stop]. Opens a secret capability URL; /login required. Sudo is gated by you in the TUI.")
		return
	}
	if len(args) == 1 {
		if a.mcpTunnel == nil {
			a.appendActivity("No MCP tunnel is running.")
			return
		}
		a.mcpTunnel.stopping = true
		a.mcpTunnel.cancel()
		a.clearMCPTunnelBar()
		a.appendActivity("Stopping MCP tunnel and revoking its link.")
		a.refreshContextBar()
		return
	}
	if a.mcpTunnel != nil {
		a.appendActivity("An MCP tunnel is already running or stopping. Use /mcp tunnel stop to revoke it.")
		return
	}
	key := auth.ResolveErgoToken()
	if key == "" {
		a.appendActivity("Run /login first, then /mcp tunnel. Standalone: coder mcp tunnel (sudo denied).")
		return
	}
	if a.runner == nil || a.registry == nil {
		a.appendActivity("MCP tunnel unavailable: interactive tool registry is not initialized.")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	tunnel := &interactiveMCPTunnel{cancel: cancel, done: make(chan struct{})}
	a.mcpTunnel = tunnel
	registry := &interactiveMCPRegistry{app: a, ctx: ctx}
	server := &mcpserver.Server{Registry: registry, Version: a.appVersion}
	relay := &mcpserver.Relay{BaseURL: client.ErgoDefaultBaseURL, Key: key, Server: server}
	a.appendActivity("Opening MCP tunnel for this interactive workspace. Anyone holding its secret link can call tools; sudo uses your existing TUI approval gate, not unattended root.")
	a.runMCPTunnel(ctx, tunnel, relay.Run)
}

// The runner is injected here so lifecycle tests need not create a real public
// capability or contact the production relay.
func (a *App) runMCPTunnel(ctx context.Context, tunnel *interactiveMCPTunnel, run func(context.Context, func(mcpserver.Publication)) error) {
	go func() {
		defer close(tunnel.done)
		err := run(ctx, func(p mcpserver.Publication) {
			a.postUI(func() {
				if ctx.Err() != nil || a.mcpTunnel != tunnel {
					return
				}
				tunnel.ready = true
				a.appendActivity("MCP tunnel ready. Its masked link is pinned above the input; F7 shows/hides the token, F8 copies the revealed link. /mcp tunnel stop revokes it.")
				a.setMCPTunnelLink(p)
				a.refreshContextBar()
			})
		})
		wasCanceled := ctx.Err() != nil
		tunnel.cancel()
		a.postUI(func() {
			if a.mcpTunnel != tunnel {
				return
			}
			a.clearMCPTunnelBar()
			a.mcpTunnel = nil
			if err != nil && !wasCanceled {
				a.appendActivity("MCP tunnel failed: " + tview.Escape(err.Error()))
			} else {
				a.appendActivity("MCP tunnel stopped; its link is revoked (or expires with the relay lease if unreachable).")
			}
			a.refreshContextBar()
		})
	}()
}

// Shutdown must not wait for UI callbacks: the event loop may already be gone.
// Relay.Run cancels outstanding tools/prompts and attempts DELETE before done.
func (a *App) shutdownMCPTunnel() {
	if a.mcpTunnel == nil {
		return
	}
	a.mcpTunnel.cancel()
	// The event loop has exited, so scrub UI buffers without rebuilding it.
	a.scrubMCPTunnelLink()
	// Bound exit if a third-party tool fails to honor cancellation. The relay
	// stops heartbeats on cancellation; its lease still expires if DELETE fails.
	select {
	case <-a.mcpTunnel.done:
	case <-time.After(5 * time.Second):
	}
}

// Live registry access is marshalled onto the event loop so /mcp add/remove
// cannot race discovery. Tool execution reserves the same busy slot as an agent
// turn, and uses precisely the runner's ExecEnv (including Prompter + Sudo).
// Busy sessions fail clearly rather than silently interleaving tool mutations.
type interactiveMCPRegistry struct {
	app *App
	ctx context.Context
}

func (r *interactiveMCPRegistry) onUI(fn func()) bool {
	done := make(chan struct{})
	r.app.postUI(func() {
		if r.ctx.Err() == nil {
			fn()
		}
		close(done)
	})
	select {
	case <-done:
		return r.ctx.Err() == nil
	case <-r.ctx.Done():
		return false
	}
}
func (r *interactiveMCPRegistry) Names() []string {
	// Buffered replies avoid races if cancellation wins while a UI closure runs.
	out := make(chan []string, 1)
	r.onUI(func() { out <- r.app.registry.Names() })
	select {
	case names := <-out:
		return names
	default:
		return nil
	}
}
func (r *interactiveMCPRegistry) Definitions(names []string) []client.ToolDefinition {
	out := make(chan []client.ToolDefinition, 1)
	r.onUI(func() { out <- r.app.registry.Definitions(names) })
	select {
	case defs := <-out:
		return defs
	default:
		return nil
	}
}
func (r *interactiveMCPRegistry) Execute(ctx context.Context, name string, args json.RawMessage, _ tools.ExecEnv) (tools.Result, error) {
	type reservation struct {
		env tools.ExecEnv
		err error
	}
	out := make(chan reservation, 1)
	r.app.postUI(func() {
		if ctx.Err() != nil || r.ctx.Err() != nil {
			out <- reservation{err: context.Canceled}
			return
		}
		if r.app.busy {
			out <- reservation{err: fmt.Errorf("interactive session is busy; retry when its current turn finishes")}
			return
		}
		r.app.busy = true
		r.app.mcpExecuting = true
		r.app.appendActivity("MCP consumer is calling " + tview.Escape(name) + "; any sudo approval is yours to grant or deny.")
		out <- reservation{env: r.app.runner.ExecEnv()}
	})
	// Always observe the reservation result, even if cancellation races it; if it
	// acquired the slot we must release it before allowing another agent turn.
	var v reservation
	select {
	case v = <-out:
	case <-r.ctx.Done():
		go func() {
			select {
			case v := <-out:
				if v.err == nil {
					r.release()
				}
			case <-r.app.uiq.stopped:
			}
		}()
		return tools.Result{}, context.Canceled
	}
	if v.err != nil {
		return tools.Result{}, v.err
	}
	defer r.release()
	if err := ctx.Err(); err != nil {
		return tools.Result{}, err
	}
	result, err := r.app.registry.Execute(ctx, name, args, v.env)
	// Local sudo records stay in the TUI. Consumers receive command output or
	// a denial, not whether approval was once-only or session-wide.
	if name == "run_command" {
		result.Content = stripMCPSudoDecision(result.Content)
		result.Preview = stripMCPSudoDecision(result.Preview)
	}
	return result, err
}
func (r *interactiveMCPRegistry) release() {
	r.app.postUI(func() {
		r.app.mcpExecuting = false
		r.app.busy = false
		if err := r.app.saveSession(); err != nil {
			r.app.appendActivity("MCP session save failed: " + tview.Escape(err.Error()))
		}
		r.app.runIdleQueue()
		r.app.refreshFooter()
	})
}

func isMCPHint(text string) bool {
	fields := strings.Fields(text)
	return len(fields) > 0 && strings.EqualFold(fields[0], "/mcp")
}

func stripMCPSudoDecision(text string) string {
	line, rest, ok := strings.Cut(text, "\n")
	if ok && (strings.HasPrefix(line, "[sudo:") || strings.HasPrefix(line, "[sudo on ")) && strings.HasSuffix(line, " by the user]") {
		return rest
	}
	return text
}
