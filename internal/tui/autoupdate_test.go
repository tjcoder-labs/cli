package tui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/update"
)

// fakeUpdates points UpdateChecker at a manifest advertising 9.9.9 and
// stubs the executable path and the prepare/apply steps, so no real
// network, npm, or binary swap is ever touched.
type fakeUpdates struct {
	applied  atomic.Int32
	applyErr error
}

func installFakeUpdates(t *testing.T, exe, buildMethod string) *fakeUpdates {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"version":"9.9.9"}`)
	}))
	t.Cleanup(srv.Close)
	f := &fakeUpdates{}
	oldChecker, oldExe, oldPrep, oldApply, oldBM := UpdateChecker, currentExecutable, prepareUpdate, applyUpdate, BuildMethod
	t.Cleanup(func() {
		UpdateChecker, currentExecutable, prepareUpdate, applyUpdate, BuildMethod = oldChecker, oldExe, oldPrep, oldApply, oldBM
	})
	UpdateChecker = func() *update.Checker {
		c := update.NewChecker()
		c.ManifestURL = srv.URL + "/package.json"
		c.ReleasesURL = srv.URL + "/none"
		c.TagsURL = srv.URL + "/none"
		return c
	}
	currentExecutable = func() (string, error) { return exe, nil }
	BuildMethod = buildMethod
	prepareUpdate = func(_ context.Context, _ *update.Checker, m update.Method, _, latest, exe string) (update.Plan, error) {
		return update.Plan{Method: m, Version: latest, Target: exe}, nil
	}
	applyUpdate = func(context.Context, *update.Checker, update.Plan, io.Writer) error {
		f.applied.Add(1)
		return f.applyErr
	}
	return f
}

const npmExe = "/nonexistent/lib/node_modules/@tjcoder/cli/lib/coder"

func transcriptText(a *App) func() string {
	return func() string { return a.transcript.GetText(true) }
}

func TestAutoUpdateInstallsNPMWithoutPrompt(t *testing.T) {
	a, _ := startSimApp(t)
	t.Setenv(update.AutoUpdateEnv, "")
	f := installFakeUpdates(t, npmExe, "")
	a.checkAndMaybeAutoUpdate(context.Background())
	waitForText(t, a, "Updated to v9.9.9 · restart coder to use it", transcriptText(a))
	if n := f.applied.Load(); n != 1 {
		t.Fatalf("apply ran %d times, want 1", n)
	}
	if installing.Load() {
		t.Fatal("install guard not released")
	}
}

func TestAutoUpdateBinaryFailureSuggestsUpdate(t *testing.T) {
	a, _ := startSimApp(t)
	t.Setenv(update.AutoUpdateEnv, "")
	f := installFakeUpdates(t, "/usr/local/bin/coder", "binary")
	f.applyErr = errors.New("checksum mismatch\nmore detail")
	a.checkAndMaybeAutoUpdate(context.Background())
	waitForText(t, a, "Automatic update to v9.9.9 failed: checksum mismatch · run /update", transcriptText(a))
}

func TestAutoUpdateOffOnlyNotifies(t *testing.T) {
	a, _ := startSimApp(t)
	t.Setenv(update.AutoUpdateEnv, "0")
	f := installFakeUpdates(t, npmExe, "")
	a.checkAndMaybeAutoUpdate(context.Background())
	waitForText(t, a, "Update available: v0.0.0 → v9.9.9 · run /update", transcriptText(a))
	if n := f.applied.Load(); n != 0 {
		t.Fatalf("auto-update off must not install (apply ran %d times)", n)
	}
}

func TestAutoUpdateNeverInstallsSourceBuilds(t *testing.T) {
	a, _ := startSimApp(t)
	t.Setenv(update.AutoUpdateEnv, "")
	f := installFakeUpdates(t, "/home/me/go/bin/coder", "")
	a.checkAndMaybeAutoUpdate(context.Background())
	waitForText(t, a, "source build; update with: git pull", transcriptText(a))
	if n := f.applied.Load(); n != 0 {
		t.Fatalf("source build must not install (apply ran %d times)", n)
	}
}

func TestAutoUpdateSkipsWhileAnotherInstallRuns(t *testing.T) {
	a, _ := startSimApp(t)
	t.Setenv(update.AutoUpdateEnv, "")
	f := installFakeUpdates(t, npmExe, "")
	if !installing.CompareAndSwap(false, true) {
		t.Fatal("install guard already held")
	}
	defer installing.Store(false)
	a.checkAndMaybeAutoUpdate(context.Background())
	onLoop(t, a, func() {}) // drain posted notices
	time.Sleep(50 * time.Millisecond)
	onLoop(t, a, func() {})
	if n := f.applied.Load(); n != 0 {
		t.Fatalf("second install started (apply ran %d times)", n)
	}
	if strings.Contains(a.transcript.GetText(true), "Updated to") {
		t.Fatal("unexpected success notice")
	}
}
