package client

import (
	"net/http"
	"strings"

	"github.com/tjcoder-labs/cli/internal/appdir"
	"github.com/tjcoder-labs/cli/internal/telemetry"
)

// ApplyErgoIdentityHeaders marks an HTTP request as coming from this
// Coder CLI process so the gateway can attribute usage and sessions.
// The install id is only sent when settings.json already has one — we
// never mint one just to tag a request (opt-out users should not get an id).
func ApplyErgoIdentityHeaders(req *http.Request) {
	if req == nil {
		return
	}
	ver := strings.TrimSpace(Version)
	if ver == "" {
		ver = "dev"
	}
	req.Header.Set("X-Ergo-Client", "tjcoder-cli/"+ver)
	req.Header.Set("X-Ergo-Client-Version", ver)
	req.Header.Set("X-Ergo-Interface", "tui")
	if id := strings.TrimSpace(appdir.Load().InstallID); id != "" {
		req.Header.Set("X-Ergo-Install-Id", id)
	}
	if sid := strings.TrimSpace(telemetry.LaunchSessionID()); sid != "" {
		req.Header.Set("X-Ergo-Session-Id", sid)
	}
}
