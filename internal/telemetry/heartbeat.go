package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/appdir"
)

// Heartbeat events.
const (
	HeartbeatInstall      = "install"
	HeartbeatSessionStart = "session_start"
	HeartbeatTick         = "heartbeat"
)

// HeartbeatInterval is how often a running TUI pings the gateway.
const HeartbeatInterval = 2 * time.Minute

// HeartbeatTimeout bounds each request.
const HeartbeatTimeout = 3 * time.Second

// Heartbeat posts anonymous install/usage pings to the ergo gateway
// (POST {base}/cli/heartbeat) for dashboard stat cards. See
// /workspace/cli-heartbeat-contract.md (contract v1) for the server side;
// all failures, including 4xx/429/5xx, are ignored without retry.
type Heartbeat struct {
	BaseURL   string
	Version   string
	InstallID string
	// Token returns the ergo-cli key when signed in ("" = anonymous).
	Token    func() string
	HTTP     *http.Client
	Interval time.Duration
}

type heartbeatBody struct {
	InstallID string `json:"install_id"`
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Event     string `json:"event"`
}

// Send posts one event. It is fire-and-forget: errors are swallowed.
func (h *Heartbeat) Send(ctx context.Context, event string) {
	// Opt-out is checked before every send (contract v1), so
	// `coder telemetry off` in another shell stops a running TUI.
	if h == nil || h.BaseURL == "" || h.InstallID == "" || DisabledReason() != "" {
		return
	}
	body, _ := json.Marshal(heartbeatBody{
		InstallID: h.InstallID, Version: h.Version,
		OS: runtime.GOOS, Arch: runtime.GOARCH, Event: event,
	})
	ctx, cancel := context.WithTimeout(ctx, HeartbeatTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.BaseURL, "/")+"/cli/heartbeat", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tjcoder-cli/"+h.Version)
	if h.Token != nil {
		if tok := strings.TrimSpace(h.Token()); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	c := h.HTTP
	if c == nil {
		c = &http.Client{Timeout: HeartbeatTimeout}
	}
	resp, err := c.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
}

// Run sends "install" once per install (first run), "session_start",
// then "heartbeat" every Interval until ctx is done. Call it on its
// own goroutine, from the TUI only.
func (h *Heartbeat) Run(ctx context.Context, firstRun bool) {
	if h == nil {
		return
	}
	if firstRun {
		h.Send(ctx, HeartbeatInstall)
	}
	h.Send(ctx, HeartbeatSessionStart)
	every := h.Interval
	if every <= 0 {
		every = HeartbeatInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Send(ctx, HeartbeatTick)
		}
	}
}

// ClaimFirstRun reports whether the one-time install events still need
// sending and marks them sent. It mints the install id if needed.
func ClaimFirstRun() (installID string, first bool) {
	id, _ := appdir.InstallID()
	s, err := appdir.Update(func(s *appdir.Settings) {
		first = !s.InstallReported
		s.InstallReported = true
	})
	if err != nil || s.InstallID == "" {
		return id, false
	}
	return s.InstallID, first
}
