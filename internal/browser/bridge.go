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

	mu       sync.Mutex
	sessions map[string]string // targetID -> sessionID cache
}

// DialBridge connects to Chrome on port, loads the shared registry, and
// returns a ready bridge. agent names the owning agent for bookkeeping.
func DialBridge(ctx context.Context, port int, agent string) (*Bridge, error) {
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
	b := &Bridge{Client: cli, Registry: reg, ID: id, Agent: agent, sessions: map[string]string{}}
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
		if b.Registry.OwnershipOf(t.ID) == OwnMine {
			_ = b.Registry.Release(t.ID)
		}
	}
	return nil
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
	b.Registry.GC(live)
}
