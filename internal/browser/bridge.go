package browser

import (
	"context"
	"fmt"
	"os"
	"sync"
)

// Bridge bundles a CDP client with the tab-ownership registry and a stable
// session identity so tool calls can safely share one Chrome instance.
type Bridge struct {
	Client   *Client
	Registry *Registry
	// ID identifies this agent session in the ownership registry.
	ID    string
	Agent string
	// Port is the CDP port this Bridge is connected to. Stored so every
	// ownership call can be scoped to the correct Chrome instance when
	// multiple instances run on different ports.
	Port int

	mu       sync.Mutex
	sessions map[string]string // targetID -> sessionID cache
}

// DialBridge connects to Chrome on port, loads the shared registry, and
// returns a ready bridge. agent names the owning agent for bookkeeping.
func DialBridge(ctx context.Context, port int, agent string) (*Bridge, error) {
	if port <= 0 {
		port = 9222
	}
	cli, err := Connect(ctx, port)
	if err != nil {
		return nil, err
	}
	id := fmt.Sprintf("pid-%d", os.Getpid())
	reg, err := NewRegistry("", id)
	if err != nil {
		cli.Close()
		return nil, err
	}
	b := &Bridge{Client: cli, Registry: reg, ID: id, Agent: agent, Port: port, sessions: map[string]string{}}
	return b, nil
}

// Close releases the underlying connection (registry is file-backed, so
// ownership entries persist; run ReleaseAll first if you want a clean handoff).
func (b *Bridge) Close() error {
	return b.Client.Close()
}

// SessionFor returns an attached CDP sessionID for targetID, attaching lazily
// and caching the mapping.
func (b *Bridge) SessionFor(ctx context.Context, targetID string) (string, error) {
	b.mu.Lock()
	if sid, ok := b.sessions[targetID]; ok && sid != "" {
		b.mu.Unlock()
		return sid, nil
	}
	b.mu.Unlock()

	sid, err := b.Client.Attach(ctx, targetID)
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	b.sessions[targetID] = sid
	b.mu.Unlock()
	return sid, nil
}

// ReleaseAll drops every tab this session owns (does not close the tabs).
// Callers choose CloseOwnedTabs first if they want the tabs gone too.
func (b *Bridge) ReleaseAll(ctx context.Context) error {
	targets, err := b.Client.ListTargets(ctx)
	if err != nil {
		return err
	}
	for _, t := range targets {
		if b.Registry.OwnershipOf(b.Port, t.ID) == OwnMine {
			_ = b.Registry.Release(b.Port, t.ID)
		}
	}
	return nil
}

// IsAlive reports whether the underlying CDP WebSocket is still connected.
// When Chrome crashes or is manually killed, the pump goroutine exits and
// the client is marked closed. This lets callers detect stale bridges
// and reconnect without surfacing "broken pipe" errors to the agent.
func (b *Bridge) IsAlive() bool {
	return b != nil && b.Client != nil && !b.Client.Closed()
}

// ResetSessions clears the cached target→sessionID mappings. Call this
// after reconnecting to a restarted Chrome instance so stale session IDs
// don't produce "session not found" errors.
func (b *Bridge) ResetSessions() {
	b.mu.Lock()
	b.sessions = map[string]string{}
	b.mu.Unlock()
}

// GCRegistry prunes ownership entries for tabs that no longer exist.
func (b *Bridge) GCRegistry(ctx context.Context) {
	targets, err := b.Client.ListTargets(ctx)
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, t := range targets {
		live[t.ID] = true
	}
	b.Registry.GC(b.Port, live)
}
