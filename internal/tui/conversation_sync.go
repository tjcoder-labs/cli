package tui

import (
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/convsync"
	"github.com/tjcoder-labs/cli/internal/session"
)

// initConvSync wires transcript sync: when the user is signed in to the
// tjcoder.com gateway, every saved session is mirrored (in the background)
// into their ergo-ai-server conversations, whichever provider is active.
// Opt out with CODER_TRANSCRIPT_SYNC=0 or "transcript_sync": false in prefs.
func (a *App) initConvSync() {
	if testing.Testing() {
		return // never reach a real gateway (or keychain) from unit tests
	}
	a.convSync = convsync.New(convsync.Options{
		BaseURL: ergoBaseURL,
		Token:   auth.ResolveErgoToken,
		Enabled: session.TranscriptSyncEnabled,
	})
}

// ensureSessionID returns the conversation's stable id, minting one if needed.
func (a *App) ensureSessionID() string {
	if a.sessionID == "" {
		a.sessionID = session.NewSessionID()
	}
	return a.sessionID
}

// notifyConvSync hands the just-saved history to the background syncer.
// Non-blocking; called from saveSession on the UI goroutine.
func (a *App) notifyConvSync(history []client.Message) {
	if a.convSync == nil || a.workspaceRoot == "" || len(history) == 0 {
		return
	}
	a.convSync.Notify(convsync.Snapshot{
		SessionID:  a.ensureSessionID(),
		CursorPath: convsync.CursorPathFor(a.sessionPath()),
		History:    history,
		Meta: convsync.Meta{
			Provider:   client.ProviderKey(a.provider),
			Model:      a.currentModel,
			Agent:      a.currentAgent.Name,
			Cwd:        a.workspaceRoot,
			CLIVersion: a.appVersion,
			Platform:   convsync.Platform(),
			Title:      convsync.TitleFrom(history),
		},
	})
}

// closeConvSync gives the syncer a short, bounded chance to flush on exit.
func (a *App) closeConvSync() {
	if a.convSync != nil {
		a.convSync.Close(3 * time.Second)
	}
}
