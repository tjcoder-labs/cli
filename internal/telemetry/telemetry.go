// Package telemetry sends anonymous usage analytics (PostHog) and crash
// reports (Sentry).
//
// Privacy contract:
//   - keys come from build-time ldflags (env overrides); empty = no-op
//   - off when DO_NOT_TRACK=1, CODER_TELEMETRY=0, in CI, or after
//     `coder telemetry off`
//   - the only identifier is a random install id from the config dir
//   - events carry an allow-listed set of properties; prompts, code,
//     file paths, tool arguments, emails, and keys are never sent, and
//     Sentry events are scrubbed in BeforeSend
//   - sending is batched and asynchronous; Close flushes with a short
//     timeout
package telemetry

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/posthog/posthog-go"
	"github.com/tjcoder-labs/cli/internal/appdir"
)

// DefaultPostHogHost is used when POSTHOG_HOST is unset.
const DefaultPostHogHost = "https://us.i.posthog.com"

// FlushTimeout bounds how long Close may delay exit.
const FlushTimeout = 2 * time.Second

// Config is the build/env configuration.
type Config struct {
	SentryDSN   string
	PostHogKey  string
	PostHogHost string
	Version     string
	Headless    bool
}

// FromEnv applies SENTRY_DSN / POSTHOG_KEY / POSTHOG_HOST overrides.
func (c Config) FromEnv() Config {
	if v := os.Getenv("SENTRY_DSN"); v != "" {
		c.SentryDSN = v
	}
	if v := os.Getenv("POSTHOG_KEY"); v != "" {
		c.PostHogKey = v
	}
	if v := os.Getenv("POSTHOG_HOST"); v != "" {
		c.PostHogHost = v
	}
	if c.PostHogHost == "" {
		c.PostHogHost = DefaultPostHogHost
	}
	return c
}

// DisabledReason reports why telemetry is off ("" = allowed). It covers
// the env/CI/user gates shared with the ergo heartbeat.
func DisabledReason() string {
	if v := strings.TrimSpace(os.Getenv("DO_NOT_TRACK")); v != "" && v != "0" && strings.ToLower(v) != "false" {
		return "DO_NOT_TRACK"
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("CODER_TELEMETRY"))); v == "0" || v == "false" || v == "off" || v == "no" {
		return "CODER_TELEMETRY"
	}
	if appdir.InCI() {
		return "ci"
	}
	if s := appdir.Load(); s.Telemetry != nil && !*s.Telemetry {
		return "user"
	}
	return ""
}

// SetEnabled persists `coder telemetry on|off`.
func SetEnabled(on bool) error {
	_, err := appdir.Update(func(s *appdir.Settings) { s.Telemetry = &on })
	if c := Default(); c != nil && !on {
		c.off.Store(true)
	}
	return err
}

// Client is a running telemetry session. The zero value and nil are
// valid no-ops.
type Client struct {
	ph        posthog.Client
	sentry    bool
	id        string
	base      map[string]any
	closeOnce sync.Once
	off       atomic.Bool // set by SetEnabled(false) mid-session
}

var (
	defaultMu sync.Mutex
	def       *Client
)

// Default returns the process-wide client (nil when disabled).
func Default() *Client {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	return def
}

// Start initialises telemetry if allowed and configured and makes it
// the Default client. It never blocks on the network.
func Start(cfg Config) *Client {
	cfg = cfg.FromEnv()
	if DisabledReason() != "" || (cfg.SentryDSN == "" && cfg.PostHogKey == "") {
		return nil
	}
	// Headless runs only report once the user has seen the TUI notice.
	if cfg.Headless && !appdir.Load().TelemetryNoticeShown {
		return nil
	}
	id, _ := appdir.InstallID()
	if id == "" {
		return nil
	}
	c := &Client{id: id, base: map[string]any{
		"version": cfg.Version, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"$process_person_profile": false,
	}}
	if cfg.PostHogKey != "" {
		noGeo := true
		ph, err := posthog.NewWithConfig(cfg.PostHogKey, posthog.Config{
			Endpoint:        cfg.PostHogHost,
			Interval:        10 * time.Second,
			BatchSize:       50,
			ShutdownTimeout: FlushTimeout,
			DisableGeoIP:    &noGeo,
			Logger:          quietLogger{},
		})
		if err == nil {
			c.ph = ph
		}
	}
	if cfg.SentryDSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.SentryDSN,
			Release:          "coder@" + cfg.Version,
			SendDefaultPII:   false,
			AttachStacktrace: true,
			ServerName:       "-",
			MaxBreadcrumbs:   -1,
			BeforeSend:       scrubSentryEvent,
			Transport:        newSentryTransport(),
		})
		if err == nil {
			c.sentry = true
			sentry.ConfigureScope(func(s *sentry.Scope) { s.SetUser(sentry.User{ID: id}) })
		}
	}
	defaultMu.Lock()
	def = c
	defaultMu.Unlock()
	return c
}

func newSentryTransport() sentry.Transport {
	t := sentry.NewHTTPTransport()
	t.Timeout = FlushTimeout
	return t
}

type quietLogger struct{}

func (quietLogger) Debugf(string, ...any) {}
func (quietLogger) Logf(string, ...any)   {}
func (quietLogger) Warnf(string, ...any)  {}
func (quietLogger) Errorf(string, ...any) {}

// Track enqueues an analytics event; props are filtered by Allowed.
func (c *Client) Track(event string, props map[string]any) {
	if c == nil || c.ph == nil || c.off.Load() {
		return
	}
	p := posthog.NewProperties()
	for k, v := range c.base {
		p.Set(k, v)
	}
	for k, v := range Allowed(event, props) {
		p.Set(k, v)
	}
	_ = c.ph.Enqueue(posthog.Capture{DistinctId: c.id, Event: event, Properties: p})
}

// Error reports an error class (e.g. "provider_timeout") to both
// sinks. Only the class string and a scrubbed message are sent.
func (c *Client) Error(class string, err error) {
	if c == nil || c.off.Load() {
		return
	}
	class = Bucketless(class)
	c.Track("error", map[string]any{"class": class})
	if c.sentry && err != nil {
		ev := sentry.NewEvent()
		ev.Level = sentry.LevelError
		ev.Message = class + ": " + ScrubText(err.Error())
		ev.Tags = map[string]string{"class": class}
		sentry.CaptureEvent(ev)
	}
}

// Recover reports a panic to Sentry, flushes, and re-panics. Use as
// `defer c.Recover()` at the top of main.
func (c *Client) Recover() {
	if r := recover(); r != nil {
		if c != nil && c.sentry {
			sentry.CurrentHub().Recover(r)
			sentry.Flush(FlushTimeout)
		}
		panic(r)
	}
}

// Close flushes pending events, waiting at most FlushTimeout.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		done := make(chan struct{})
		go func() {
			if c.ph != nil {
				_ = c.ph.Close()
			}
			if c.sentry {
				sentry.Flush(FlushTimeout)
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(FlushTimeout + 500*time.Millisecond):
		}
	})
}

// Package-level helpers on the Default client.

func Track(event string, props map[string]any) { Default().Track(event, props) }
func Error(class string, err error)            { Default().Error(class, err) }

// StatusLine describes the current telemetry state for humans.
func StatusLine() string {
	switch r := DisabledReason(); r {
	case "":
		id := appdir.Load().InstallID // never mint an id just to print status
		if id == "" {
			id = "not created yet"
		}
		return "Telemetry is on (anonymous id: " + id + "). Turn off with `coder telemetry off` or DO_NOT_TRACK=1."
	case "user":
		return "Telemetry is off (coder telemetry off)."
	case "ci":
		return "Telemetry is off (CI detected)."
	default:
		return "Telemetry is off (" + r + " set)."
	}
}
