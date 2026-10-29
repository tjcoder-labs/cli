package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.9.182", "0.9.181", 1}, {"v0.9.10", "0.9.9", 1}, {"1.0.0", "1.0.0", 0},
		{"1.0.0-rc.1", "1.0.0", -1}, {"1.0.0-rc.2", "1.0.0-rc.10", -1}, {"0.10.0", "0.9.99", 1},
		{"dev", "0.1.0", -1}, {"1.2", "1.2.0", 0}, {"1.0.0+build", "1.0.0", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

type fakeGH struct {
	srv                 *httptest.Server
	manifest, release   string
	tags                string
	assets              map[string][]byte // path -> body
	manifestHits, other atomic.Int32
}

func newFakeGH(t *testing.T) *fakeGH {
	f := &fakeGH{assets: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/raw/package.json":
			f.manifestHits.Add(1)
			if f.manifest == "" {
				http.Error(w, "down", 500)
				return
			}
			fmt.Fprint(w, f.manifest)
			return
		case "/api/releases/latest":
			if f.release == "" {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `{"tag_name":%q}`, f.release)
			return
		case "/api/tags":
			fmt.Fprint(w, f.tags)
			return
		}
		if b, ok := f.assets[r.URL.Path]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGH) checker() *Checker {
	return &Checker{
		HTTP:        f.srv.Client(),
		ManifestURL: f.srv.URL + "/raw/package.json",
		ReleasesURL: f.srv.URL + "/api/releases/latest",
		TagsURL:     f.srv.URL + "/api/tags",
		DownloadURL: f.srv.URL + "/dl",
		Now:         time.Now,
	}
}

func TestLatestVersionFallbacks(t *testing.T) {
	f := newFakeGH(t)
	f.manifest = `{"version":"0.9.181"}`
	f.release = "v0.9.170"
	f.tags = `[{"name":"v0.9.150"},{"name":"v0.9.171"}]`
	c := f.checker()
	if v, err := c.LatestVersion(context.Background()); err != nil || v != "0.9.181" {
		t.Fatalf("manifest: %q %v", v, err)
	}
	f.manifest = ""
	if v, _ := c.LatestVersion(context.Background()); v != "0.9.170" {
		t.Fatalf("releases fallback: %q", v)
	}
	f.release = ""
	if v, _ := c.LatestVersion(context.Background()); v != "0.9.171" {
		t.Fatalf("tags fallback picks max semver: %q", v)
	}
	f.tags = `[]`
	if _, err := c.LatestVersion(context.Background()); err == nil {
		t.Fatal("all sources failing must error")
	}
}

func TestCheckCachedOncePerDay(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f := newFakeGH(t)
	f.manifest = `{"version":"0.9.182"}`
	c := f.checker()
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	r, err := c.CheckCached(context.Background(), "0.9.181")
	if err != nil || !r.Available() || r.Latest != "0.9.182" {
		t.Fatalf("%+v %v", r, err)
	}
	now = now.Add(23 * time.Hour)
	r, _ = c.CheckCached(context.Background(), "0.9.181")
	if f.manifestHits.Load() != 1 || !r.Available() {
		t.Fatalf("expected cache hit, hits=%d", f.manifestHits.Load())
	}
	now = now.Add(2 * time.Hour)
	c.CheckCached(context.Background(), "0.9.182")
	if f.manifestHits.Load() != 2 {
		t.Fatal("expected refetch after 24h")
	}
}

func TestCheckTimeoutIsSilentError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer slow.Close()
	c := &Checker{HTTP: &http.Client{Timeout: 50 * time.Millisecond}, ManifestURL: slow.URL, ReleasesURL: slow.URL, TagsURL: slow.URL, Now: time.Now}
	start := time.Now()
	r, err := c.CheckCached(context.Background(), "0.9.181")
	if err == nil || r.Available() {
		t.Fatal("expected a failed, non-available result")
	}
	if time.Since(start) > 400*time.Millisecond {
		t.Fatal("check should respect the short timeout")
	}
}

func TestDisabledGates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"CI", "GITHUB_ACTIONS", "BUILD_NUMBER", "CONTINUOUS_INTEGRATION", "RUN_ID", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TRAVIS", "JENKINS_URL", "TEAMCITY_VERSION", "TF_BUILD", "CODEBUILD_BUILD_ID", "CODER_NO_UPDATE_CHECK"} {
		t.Setenv(k, "")
	}
	if r := Disabled(false); r != "" {
		t.Fatalf("enabled expected, got %q", r)
	}
	if Disabled(true) != "headless" {
		t.Fatal("headless must disable")
	}
	t.Setenv("CI", "true")
	if Disabled(false) != "ci" {
		t.Fatal("CI must disable")
	}
	t.Setenv("CI", "")
	t.Setenv("CODER_NO_UPDATE_CHECK", "1")
	if Disabled(false) != "CODER_NO_UPDATE_CHECK" {
		t.Fatal("env opt-out")
	}
}

func TestDetectMethod(t *testing.T) {
	if m := DetectMethod("/usr/lib/node_modules/@tjcoder/cli/lib/coder", ""); m != MethodNPM {
		t.Fatal(m)
	}
	if m := DetectMethod("/home/u/.local/bin/coder", "binary"); m != MethodBinary {
		t.Fatal(m)
	}
	if m := DetectMethod("/home/u/go/bin/coder", ""); m != MethodSource {
		t.Fatal(m)
	}
}

func tgz(t *testing.T, name string, body []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestBinaryUpdateFallsBackToPublishedReleaseAndSwapsAtomically(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz path")
	}
	f := newFakeGH(t)
	f.release = "v0.9.170"
	archive := tgz(t, "coder", []byte("NEW-BINARY"))
	asset := fmt.Sprintf("/dl/v0.9.170/coder-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	f.assets[asset] = archive
	sum := sha256.Sum256(archive)
	f.assets["/dl/v0.9.170/checksums.txt"] = []byte(hex.EncodeToString(sum[:]) + "  " + filepath.Base(asset) + "\n")

	dir := t.TempDir()
	exe := filepath.Join(dir, "coder")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	c := f.checker()
	// main says 0.9.181 but there is no v0.9.181 release (404) → use 0.9.170.
	p, err := c.Prepare(context.Background(), MethodBinary, "0.9.160", "0.9.181", exe)
	if err != nil || p.Version != "0.9.170" || !strings.HasSuffix(p.Asset, asset) {
		t.Fatalf("%+v %v", p, err)
	}
	if err := c.Apply(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEW-BINARY" {
		t.Fatalf("binary not replaced: %q", b)
	}
	if st, _ := os.Stat(exe); st.Mode().Perm()&0o100 == 0 {
		t.Fatal("replaced binary must be executable")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".coder-update-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}

	// No newer release than current → clear error, nothing touched.
	if _, err := c.Prepare(context.Background(), MethodBinary, "0.9.170", "0.9.181", exe); err == nil || !strings.Contains(err.Error(), "no newer release") {
		t.Fatalf("got %v", err)
	}
}

func TestBinaryUpdateChecksumMismatchAborts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz path")
	}
	f := newFakeGH(t)
	name := fmt.Sprintf("coder_v0.9.182_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	f.assets["/dl/v0.9.182/"+name] = tgz(t, "coder", []byte("EVIL"))
	f.assets["/dl/v0.9.182/"+name+".sha256"] = []byte(strings.Repeat("0", 64) + "\n")
	exe := filepath.Join(t.TempDir(), "coder")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	c := f.checker()
	p, err := c.Prepare(context.Background(), MethodBinary, "0.9.181", "0.9.182", exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(context.Background(), p, nil); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Fatal("binary must be untouched on mismatch")
	}
}

func TestNPMAndSourcePlans(t *testing.T) {
	c := NewChecker()
	p, _ := c.Prepare(context.Background(), MethodNPM, "1", "2", "")
	if p.Command != "npm i -g @tjcoder/cli@latest" {
		t.Fatal(p.Command)
	}
	p, _ = c.Prepare(context.Background(), MethodSource, "1", "2", "")
	if err := c.Apply(context.Background(), p, nil); err == nil || !strings.Contains(err.Error(), "git pull") {
		t.Fatalf("source must only print instructions: %v", err)
	}
}
