package client

import (
	"net/http"
	"testing"

	"github.com/tjcoder-labs/cli/internal/appdir"
	"github.com/tjcoder-labs/cli/internal/telemetry"
)

func TestApplyErgoIdentityHeaders(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	Version = "0.9.278"

	req, _ := http.NewRequest(http.MethodGet, "https://api.tjcoder.com/v1/models", nil)
	ApplyErgoIdentityHeaders(req)
	if got := req.Header.Get("X-Ergo-Client"); got != "tjcoder-cli/0.9.278" {
		t.Fatalf("X-Ergo-Client = %q", got)
	}
	if got := req.Header.Get("X-Ergo-Client-Version"); got != "0.9.278" {
		t.Fatalf("X-Ergo-Client-Version = %q", got)
	}
	if got := req.Header.Get("X-Ergo-Interface"); got != "tui" {
		t.Fatalf("X-Ergo-Interface = %q", got)
	}
	if got := req.Header.Get("X-Ergo-Install-Id"); got != "" {
		t.Fatalf("should not mint install id, got %q", got)
	}
	if got := req.Header.Get("X-Ergo-Session-Id"); got != telemetry.LaunchSessionID() || got == "" {
		t.Fatalf("X-Ergo-Session-Id = %q", got)
	}

	if _, err := appdir.Update(func(s *appdir.Settings) { s.InstallID = "3f2b8c1e-9d4a-4c6b-8e2f-1a2b3c4d5e6f" }); err != nil {
		t.Fatal(err)
	}
	req2, _ := http.NewRequest(http.MethodPost, "https://api.tjcoder.com/v1/chat/completions", nil)
	ApplyErgoIdentityHeaders(req2)
	if got := req2.Header.Get("X-Ergo-Install-Id"); got != "3f2b8c1e-9d4a-4c6b-8e2f-1a2b3c4d5e6f" {
		t.Fatalf("X-Ergo-Install-Id = %q", got)
	}
}
