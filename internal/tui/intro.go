package tui

import (
	_ "embed"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/session"
)

// introMD is the first-launch welcome document. It is embedded so the
// binary always carries the same content the repo ships.
//
//go:embed intro.md
var introMD string

// showIntroIfFirstLaunch renders INTRO.md into the transcript on the
// user's very first TUI launch and opens the agent picker. The flag
// is persisted in the cross-workspace prefs file so the welcome shows
// exactly once across all repositories.
func (a *App) showIntroIfFirstLaunch() {
	if session.IntroSeen() {
		return
	}
	if err := session.MarkIntroSeen(); err != nil {
		// Non-fatal; the user will see the intro again on next launch.
	}
	body := strings.TrimSpace(introMD)
	if body == "" {
		return
	}
	a.appendTranscriptMarkdown(body)
	// Open the agent picker on the next event-loop tick. We are
	// currently inside tview's draw pass (afterDraw), so dispatching
	// the slash command synchronously would deadlock. A zero-delay
	// AfterFunc re-enters via the normal UI queue.
	time.AfterFunc(0, func() {
		a.postUI(func() { a.dispatchSlash("/agent") })
	})
}

// appendTranscriptMarkdown writes a markdown block to the transcript
// using the same markdown renderer as assistant replies.
func (a *App) appendTranscriptMarkdown(md string) {
	if a.transcript == nil {
		return
	}
	out := a.outStream().Write(md + "\n\n")
	a.transcript.Write([]byte(out))
	a.transcript.ScrollToEnd()
}
