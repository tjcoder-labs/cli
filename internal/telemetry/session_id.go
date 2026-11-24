package telemetry

import (
	"sync"

	"github.com/tjcoder-labs/cli/internal/appdir"
)

var (
	launchSessionOnce sync.Once
	launchSessionID   string
)

// LaunchSessionID returns the random id for this CLI process, minted once
// on first call. Concurrent TUI launches on the same machine each have
// their own process and therefore their own id. It is sent on heartbeats
// and as X-Ergo-Session-Id on gateway requests.
func LaunchSessionID() string {
	launchSessionOnce.Do(func() {
		launchSessionID = appdir.NewUUID()
	})
	return launchSessionID
}
