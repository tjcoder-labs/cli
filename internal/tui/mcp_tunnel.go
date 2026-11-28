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
	// Live status only; consumer ids are opaque server-assigned values and
	// never include caller IP/UA. Nothing here is persisted.
	reconnecting bool
	consumers    map[string]time.Time
	inFlight     []string
	publication  mcpserver.Publication // held so /mcp tunnel save can persist it
}

const mcpConsumerIdle = 5 * time.Minute

func (t *interactiveMCPTunnel) connectionCount(now time.Time) int {
	n := 0
	for id, seen := range t.consumers {
		if now.Sub(seen) >= mcpConsumerIdle {
			delete(t.consumers, id)
			continue
		}
		n++
	}
	return n
}

// handleMCPRelayEvent runs on the UI goroutine.
func (a *App) handleMCPRelayEvent(tunnel *interactiveMCPTunnel, ev mcpserver.RelayEvent) {
	if a.mcpTunnel != tunnel || tunnel.stopping {
		return
	}
	switch ev.Kind {
	case mcpserver.RelayReconnecting:
		if !tunnel.reconnecting {
			a.appendActivity("MCP tunnel connection dropped; reconnecting with the same link…")
		}
		tunnel.reconnecting = true
	case mcpserver.RelayReconnected:
		if tunnel.reconnecting {
			a.appendActivity("MCP tunnel reconnected; the same link is live again.")
		}
		tunnel.reconnecting = false
	case mcpserver.RelayConsumer:
		if tunnel.consumers == nil {
			tunnel.consumers = map[string]time.Time{}
		}
		tunnel.consumers[ev.ConsumerID] = time.Now()
	}
	a.refreshMCPTunnelBar()
}

func (a *App) handleMCPTunnel(args []string) {
	if len(args) > 1 || (len(args) == 1 && args[0] != "stop" && args[0] != "save") {
		a.appendActivity("Usage: /mcp tunnel [stop|save]. Opens a secret capability URL; /login required. Sudo is gated by you in the TUI. /mcp tunnel save persists the RelayToken for later reattach.")
		return
	}
	if len(args) == 1 && args[0] == "save" {
		if a.mcpTunnel == nil || !a.mcpTunnel.ready || a.mcpTunnel.publication.ID == "" {
			a.appendActivity("No active MCP tunnel to save. Start one with /mcp tunnel first.")
			return
		}
		if err := a.saveMCPTunnel(a.mcpTunnel.publication); err != nil {
			a.appendActivity("Failed to save MCP tunnel: " + tview.Escape(err.Error()))
			return
		}
		a.appendActivity("MCP tunnel saved. A later /mcp tunnel will try to reattach with the same RelayToken until you stop it or it expires.")
		return
	}
	if len(args) == 1 && args[0] == "stop" {
		if a.mcpTunnel == nil {
			a.appendActivity("No MCP tunnel is running.")
			return
		}
		a.mcpTunnel.stopping = true
		a.mcpTunnel.cancel()
		a.clearMCPTunnelBar()
		a.clearMCPTunnel()
		a.appendActivity("Stopping MCP tunnel and revoking its link. Saved credentials cleared.")
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
	relay := &mcpserver.Relay{BaseURL: client.ErgoDefaultBaseURL, Key: key, Server: server, Events: func(ev mcpserver.RelayEvent) {
		a.postUI(func() { a.handleMCPRelayEvent(tunnel, ev) })
	}}

	// Prefer saved publication when present and not expired.
	var run func(context.Context, func(mcpserver.Publication)) error
	if saved := a.config.MCPTunnel; saved != nil && saved.SessionID != "" && saved.RelayToken != "" {
		if exp, err := time.Parse(time.RFC3339, saved.ExpiresAt); err == nil && time.Now().Before(exp) {
			pub := mcpserver.Publication{ID: saved.SessionID, RelayToken: saved.RelayToken, URL: saved.URL, ExpiresAt: saved.ExpiresAt}
			a.appendActivity("Attempting to reattach to saved MCP tunnel…")
			run = func(ctx context.Context, announce func(mcpserver.Publication)) error {
				err := relay.RunWithPublication(ctx, pub, func(p mcpserver.Publication) {
					tunnel.publication = p
					announce(p)
				})
				if errorsIsSessionGone(err) {
					a.postUI(func() { a.clearMCPTunnel() })
				}
				return err
			}
		}
	}
	if run == nil {
		a.appendActivity("Opening MCP tunnel for this interactive workspace. Anyone holding its secret link can call tools; sudo uses your existing TUI approval gate, not unattended root.")
		run = func(ctx context.Context, announce func(mcpserver.Publication)) error {
			return relay.Run(ctx, func(p mcpserver.Publication) {
				tunnel.publication = p
				announce(p)
			})
		}
	}
	a.runMCPTunnel(ctx, tunnel, run)
}

func errorsIsSessionGone(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "tunnel session ended") || strings.Contains(err.Error(), "401"))
}

// The runner is injected here so lifecycle tests need not create a real public
// capability or contact the production relay.
func (a *App) runMCPTunnel(ctx context.Context, tunnel *interactiveMCPTunnel, run func(context.Context, func(mcpserver.Publication)) error) {
	go func() {
		// Idle consumers age out of the banner count even without new events.
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tunnel.done:
				return
			case <-tick.C:
				a.postUI(func() {
					if a.mcpTunnel == tunnel {
						a.refreshMCPTunnelBar()
					}
				})
			}
		}
	}()
	go func() {
		defer close(tunnel.done)
		err := run(ctx, func(p mcpserver.Publication) {
			a.postUI(func() {
				if ctx.Err() != nil || a.mcpTunnel != tunnel {
					return
				}
				tunnel.ready = true
				tunnel.publication = p
				a.appendActivity("MCP tunnel ready. Its masked link is pinned above the input; F7 shows/hides the token, F8 copies the revealed link. /mcp tunnel stop revokes it. /mcp tunnel save persists the token for later reattach.")
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
				if errorsIsSessionGone(err) {
					a.clearMCPTunnel()
				}
			} else {
				a.appendActivity("MCP tunnel stopped; its link is revoked (or expires with the relay lease if unreachable).")
				// Keep saved credentials so a later /mcp tunnel reattaches;
				// only explicit stop or a 401 clears them.
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
	a.clearMCPTunnel()
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
		env         tools.ExecEnv
		err         error
		interactive bool
	}
	interactive := isInteractiveMCPTool(name, args)
	out := make(chan reservation, 1)
	r.app.postUI(func() {
		if ctx.Err() != nil || r.ctx.Err() != nil {
			out <- reservation{err: context.Canceled}
			return
		}
		// Interactive tools (ask_user, sudo-bearing run_command, etc.) must
		// not interleave their prompt/sudo card with an in-flight turn, so
		// they reserve the same busy slot. Non-interactive tools run
		// concurrently with whatever the session is doing.
		if interactive && r.app.busy {
			out <- reservation{err: fmt.Errorf("interactive session is busy; retry when its current turn finishes")}
			return
		}
		if interactive {
			r.app.busy = true
			r.app.mcpExecuting = true
		}
		r.app.appendActivity("MCP consumer is calling " + tview.Escape(name) + "; any sudo approval is yours to grant or deny.")
		if t := r.app.mcpTunnel; t != nil {
			t.inFlight = append(t.inFlight, name)
			r.app.refreshMCPTunnelBar()
		}
		out <- reservation{env: r.app.runner.ExecEnv(), interactive: interactive}
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
					r.release(name, v.interactive)
				}
			case <-r.app.uiq.stopped:
			}
		}()
		return tools.Result{}, context.Canceled
	}
	if v.err != nil {
		return tools.Result{}, v.err
	}
	defer r.release(name, v.interactive)
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
func (r *interactiveMCPRegistry) release(name string, interactive bool) {
	r.app.postUI(func() {
		if t := r.app.mcpTunnel; t != nil {
			for i := len(t.inFlight) - 1; i >= 0; i-- {
				if t.inFlight[i] == name {
					t.inFlight = append(t.inFlight[:i], t.inFlight[i+1:]...)
					break
				}
			}
			r.app.refreshMCPTunnelBar()
		}
		if interactive {
			r.app.mcpExecuting = false
			r.app.busy = false
			if err := r.app.saveSession(); err != nil {
				r.app.appendActivity("MCP session save failed: " + tview.Escape(err.Error()))
			}
			r.app.runIdleQueue()
			r.app.refreshFooter()
		}
	})
}

// isInteractiveMCPTool reports whether a tool may block on the user (ask_user)
// or gate on sudo approval, in which case it must not run concurrently with an
// in-flight turn. Read-only / non-prompting tools are safe to run in parallel.
func isInteractiveMCPTool(name string, args json.RawMessage) bool {
	switch name {
	case "ask_user":
		return true
	case "run_command":
		// Only sudo-bearing commands gate on the user. Inspect the command
		// field the same way the shell tool does.
		var req struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(args, &req) == nil && tools.IsSudoCommand(req.Command) {
			return true
		}
		return false
	}
	return false
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
