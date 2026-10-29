package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/tjcoder-labs/cli/internal/appdir"
)

// cleanEnv isolates the config dir and clears every opt-out gate.
func cleanEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"DO_NOT_TRACK", "CODER_TELEMETRY", "CI", "CONTINUOUS_INTEGRATION", "BUILD_NUMBER", "RUN_ID", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TRAVIS", "JENKINS_URL", "TEAMCITY_VERSION", "TF_BUILD", "CODEBUILD_BUILD_ID", "SENTRY_DSN", "POSTHOG_KEY", "POSTHOG_HOST"} {
		t.Setenv(k, "")
	}
}

func TestDisabledReasonGates(t *testing.T) {
	cleanEnv(t)
	if r := DisabledReason(); r != "" {
		t.Fatalf("clean env: got %q", r)
	}
	t.Setenv("DO_NOT_TRACK", "1")
	if r := DisabledReason(); r != "DO_NOT_TRACK" {
		t.Fatalf("DO_NOT_TRACK: got %q", r)
	}
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CODER_TELEMETRY", "0")
	if r := DisabledReason(); r != "CODER_TELEMETRY" {
		t.Fatalf("CODER_TELEMETRY: got %q", r)
	}
	t.Setenv("CODER_TELEMETRY", "")
	t.Setenv("CI", "true")
	if r := DisabledReason(); r != "ci" {
		t.Fatalf("CI: got %q", r)
	}
	t.Setenv("CI", "")
	if err := SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if r := DisabledReason(); r != "user" {
		t.Fatalf("user off: got %q", r)
	}
	if err := SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if r := DisabledReason(); r != "" {
		t.Fatalf("user on: got %q", r)
	}
}

func TestStartNoopWhenDisabledOrUnconfigured(t *testing.T) {
	cleanEnv(t)
	if c := Start(Config{Version: "1.0.0"}); c != nil {
		t.Fatal("no keys should be a no-op")
	}
	t.Setenv("DO_NOT_TRACK", "1")
	if c := Start(Config{PostHogKey: "phc_test", Version: "1.0.0"}); c != nil {
		t.Fatal("DO_NOT_TRACK should disable")
	}
	t.Setenv("DO_NOT_TRACK", "")
	if c := Start(Config{PostHogKey: "phc_test", Version: "1.0.0", Headless: true}); c != nil {
		t.Fatal("headless before the TUI notice should be a no-op")
	}
	// nil client methods are safe.
	var c *Client
	c.Track("x", nil)
	c.Error("x", errors.New("y"))
	c.Close()
}

func TestAllowedDropsUnknownAndUnsafe(t *testing.T) {
	got := Allowed("tool_completed", map[string]any{
		"tool": "run_command", "ok": true, "latency": "1-5s",
		"args": "rm -rf /", "path": "/home/me/x",
	})
	if len(got) != 3 || got["tool"] != "run_command" {
		t.Fatalf("got %v", got)
	}
	got = Allowed("model_selected", map[string]any{"model": "/Users/tj/secret model.gguf"})
	if _, ok := got["model"]; ok {
		t.Fatalf("path-like model should be dropped: %v", got)
	}
	got = Allowed("model_selected", map[string]any{"model": "sk-abcdefghijklmnop"})
	if _, ok := got["model"]; ok {
		t.Fatalf("key-like value should be dropped: %v", got)
	}
	if got := Allowed("unknown_event", map[string]any{"a": 1}); len(got) != 0 {
		t.Fatalf("unknown event: %v", got)
	}
}

func TestScrubText(t *testing.T) {
	in := "open /home/tj/project/main.go failed for tj@tjcoder.com via https://api.x.com/v1?key=1 with sk-abcdef1234567890 and C:\\Users\\tj\\a.txt"
	out := ScrubText(in)
	for _, bad := range []string{"/home/tj", "tj@tjcoder.com", "api.x.com", "sk-abcdef", "Users"} {
		if strings.Contains(out, bad) {
			t.Fatalf("leaked %q in %q", bad, out)
		}
	}
	if n := len([]rune(ScrubText(strings.Repeat("a ", 500)))); n > 201 {
		t.Fatalf("not truncated: %d", n)
	}
}

func TestScrubSentryEvent(t *testing.T) {
	ev := sentry.NewEvent()
	ev.User = sentry.User{ID: "anon", Email: "tj@tjcoder.com", Username: "tj", IPAddress: "1.2.3.4"}
	ev.ServerName = "tjs-macbook"
	ev.Message = "read /Users/tj/notes.txt"
	ev.Tags = map[string]string{"class": "io", "cwd": "/Users/tj"}
	ev.Contexts = map[string]sentry.Context{"os": {"name": "darwin", "kernel_version": "x"}, "device": {"hostname": "tjs-macbook"}}
	ev.Breadcrumbs = []*sentry.Breadcrumb{{Message: "prompt text"}}
	ev.Exception = []sentry.Exception{{Value: "token sk-abcdef1234567890", Stacktrace: &sentry.Stacktrace{Frames: []sentry.Frame{{
		AbsPath: "/Users/tj/go/src/github.com/tjcoder-labs/cli/internal/tui/app.go", Filename: "/Users/tj/go/src/github.com/tjcoder-labs/cli/internal/tui/app.go",
		ContextLine: "password := x", Vars: map[string]interface{}{"password": "hunter2"},
	}}}}}
	out := scrubSentryEvent(ev, nil)
	b, _ := json.Marshal(out)
	s := string(b)
	for _, bad := range []string{"tj@tjcoder.com", "1.2.3.4", "tjs-macbook", "/Users/tj", "prompt text", "sk-abcdef", "hunter2", "password :="} {
		if strings.Contains(s, bad) {
			t.Fatalf("leaked %q: %s", bad, s)
		}
	}
	if out.User.ID != "anon" || out.Tags["class"] != "io" {
		t.Fatalf("kept fields wrong: %+v", out.User)
	}
	if f := out.Exception[0].Stacktrace.Frames[0].Filename; f != "internal/tui/app.go" {
		t.Fatalf("filename = %q", f)
	}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "boom /home/x" }

func TestClassifiers(t *testing.T) {
	if ToolClass("mcp_private_server_tool") != "mcp" || ToolClass("read_file") != "read_file" {
		t.Fatal("ToolClass")
	}
	if c := ErrorClass(fmt.Errorf("wrap: %w", context.DeadlineExceeded)); c != "timeout" {
		t.Fatalf("timeout: %q", c)
	}
	if c := ErrorClass(fmt.Errorf("wrap: %w", fakeErr{})); c != "telemetry.fakeErr" {
		t.Fatalf("typed: %q", c)
	}
	if c := ErrorClass(errors.New("secret text")); c != "other" {
		t.Fatalf("plain: %q", c)
	}
	if LatencyBucket(300*time.Millisecond) == LatencyBucket(30*time.Second) {
		t.Fatal("latency buckets collapse")
	}
	if TokenBucket(10) == TokenBucket(100000) {
		t.Fatal("token buckets collapse")
	}
}

type hbServer struct {
	mu     sync.Mutex
	events []string
	auth   []string
	bodies []heartbeatBody
}

func newHBServer(t *testing.T) (*hbServer, *httptest.Server) {
	hs := &hbServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/cli/heartbeat" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var b heartbeatBody
		_ = json.NewDecoder(r.Body).Decode(&b)
		hs.mu.Lock()
		hs.events = append(hs.events, b.Event)
		hs.auth = append(hs.auth, r.Header.Get("Authorization"))
		hs.bodies = append(hs.bodies, b)
		hs.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return hs, srv
}

func TestHeartbeatRun(t *testing.T) {
	cleanEnv(t)
	hs, srv := newHBServer(t)
	hb := &Heartbeat{BaseURL: srv.URL + "/", Version: "0.9.182", InstallID: "id-1", Interval: 20 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()
	hb.Run(ctx, true)

	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.events) < 3 || hs.events[0] != "install" || hs.events[1] != "session_start" || hs.events[2] != "heartbeat" {
		t.Fatalf("events = %v", hs.events)
	}
	b := hs.bodies[0]
	if b.InstallID != "id-1" || b.Version != "0.9.182" || b.OS == "" || b.Arch == "" {
		t.Fatalf("body = %+v", b)
	}
	for _, a := range hs.auth {
		if a != "" {
			t.Fatalf("anonymous run sent auth %q", a)
		}
	}
}

func TestHeartbeatBearerOnlyWhenSignedIn(t *testing.T) {
	cleanEnv(t)
	hs, srv := newHBServer(t)
	hb := &Heartbeat{BaseURL: srv.URL, Version: "1", InstallID: "id", Token: func() string { return "ergo_cli_key" }}
	hb.Send(context.Background(), HeartbeatSessionStart)
	hb.Token = func() string { return "" }
	hb.Send(context.Background(), HeartbeatTick)
	if hs.auth[0] != "Bearer ergo_cli_key" || hs.auth[1] != "" {
		t.Fatalf("auth = %q", hs.auth)
	}
}

func TestHeartbeatOptOutCheckedEverySend(t *testing.T) {
	for _, env := range [][2]string{{"DO_NOT_TRACK", "1"}, {"CODER_TELEMETRY", "off"}, {"GITHUB_ACTIONS", "true"}} {
		cleanEnv(t)
		hs, srv := newHBServer(t)
		t.Setenv(env[0], env[1])
		(&Heartbeat{BaseURL: srv.URL, Version: "1", InstallID: "id"}).Run(timeoutCtx(t, 40*time.Millisecond), true)
		if len(hs.events) != 0 {
			t.Fatalf("%s: sent %v", env[0], hs.events)
		}
	}
	cleanEnv(t)
	hs, srv := newHBServer(t)
	_ = SetEnabled(false)
	(&Heartbeat{BaseURL: srv.URL, Version: "1", InstallID: "id"}).Send(context.Background(), HeartbeatInstall)
	if len(hs.events) != 0 {
		t.Fatalf("user off: sent %v", hs.events)
	}
}

func timeoutCtx(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestHeartbeatIgnoresErrorStatuses(t *testing.T) {
	cleanEnv(t)
	for _, code := range []int{400, 413, 429, 500} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		(&Heartbeat{BaseURL: srv.URL, Version: "1", InstallID: "id"}).Send(context.Background(), HeartbeatTick)
		srv.Close()
	}
}

func TestStatusLineDoesNotMintID(t *testing.T) {
	cleanEnv(t)
	_ = StatusLine()
	if id := appdirInstallIDOnDisk(); id != "" {
		t.Fatalf("status minted %q", id)
	}
}

func TestHeartbeatNeverBlocksOnSlowServer(t *testing.T) {
	cleanEnv(t)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	hb := &Heartbeat{BaseURL: srv.URL, Version: "1", InstallID: "id", HTTP: &http.Client{Timeout: 50 * time.Millisecond}}
	start := time.Now()
	hb.Send(context.Background(), HeartbeatTick) // must return, error swallowed
	if time.Since(start) > HeartbeatTimeout+time.Second {
		t.Fatal("Send blocked")
	}
	// Unreachable host: also silent.
	(&Heartbeat{BaseURL: "http://127.0.0.1:1", InstallID: "id"}).Send(context.Background(), HeartbeatTick)
}

func TestClaimFirstRunOnce(t *testing.T) {
	cleanEnv(t)
	id1, first := ClaimFirstRun()
	if !first || id1 == "" {
		t.Fatalf("first claim: %q %v", id1, first)
	}
	id2, again := ClaimFirstRun()
	if again || id2 != id1 {
		t.Fatalf("second claim: %q %v", id2, again)
	}
}

func appdirInstallIDOnDisk() string { return appdir.Load().InstallID }
