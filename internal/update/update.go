// Package update checks GitHub for a newer coder release and applies it
// according to how the running binary was installed.
//
// Detection: the version in main's raw package.json is authoritative;
// when that is unreachable the GitHub releases API (then tags API) is
// used. Results are cached for 24h in update-check.json in the user
// config dir so the network is touched at most once a day, and every
// failure is silent.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/appdir"
)

const (
	Repo            = "tjcoder-labs/cli"
	NPMPackage      = "@tjcoder/cli"
	CheckInterval   = 24 * time.Hour
	DefaultTimeout  = 3 * time.Second
	cacheFile       = "update-check.json"
	maxMetadataSize = 1 << 20
)

// Checker holds the endpoints and HTTP client; tests point it at httptest.
type Checker struct {
	HTTP        *http.Client
	ManifestURL string // raw package.json on main
	ReleasesURL string // GitHub "latest release" API
	TagsURL     string // GitHub tags API
	DownloadURL string // release download base: <base>/v<ver>/<asset>
	Now         func() time.Time
}

// NewChecker returns a Checker for the real GitHub endpoints.
func NewChecker() *Checker {
	return &Checker{
		HTTP:        &http.Client{Timeout: DefaultTimeout},
		ManifestURL: "https://raw.githubusercontent.com/" + Repo + "/main/package.json",
		ReleasesURL: "https://api.github.com/repos/" + Repo + "/releases/latest",
		TagsURL:     "https://api.github.com/repos/" + Repo + "/tags?per_page=20",
		DownloadURL: "https://github.com/" + Repo + "/releases/download",
		Now:         time.Now,
	}
}

// Result is a completed check.
type Result struct {
	Current   string    `json:"-"`
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

// Available reports whether Latest is strictly newer than Current.
func (r Result) Available() bool {
	return r.Latest != "" && Compare(r.Latest, r.Current) > 0
}

// Disabled reports why background checks are off ("" when enabled).
// headless covers -p / ask mode.
func Disabled(headless bool) string {
	switch {
	case truthy(os.Getenv("CODER_NO_UPDATE_CHECK")):
		return "CODER_NO_UPDATE_CHECK"
	case headless:
		return "headless"
	case appdir.InCI():
		return "ci"
	}
	if s := appdir.Load(); s.UpdateCheck != nil && !*s.UpdateCheck {
		return "config"
	}
	return ""
}

func truthy(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v != "" && v != "0" && v != "false" && v != "no"
}

// CheckCached returns the cached result when it is younger than
// CheckInterval, otherwise fetches and caches a fresh one.
func (c *Checker) CheckCached(ctx context.Context, current string) (Result, error) {
	var cached Result
	if err := appdir.ReadJSON(cacheFile, &cached); err == nil && c.Now().Sub(cached.CheckedAt) < CheckInterval && cached.CheckedAt.Before(c.Now().Add(time.Minute)) {
		cached.Current = current
		return cached, nil
	}
	r, err := c.Check(ctx, current)
	if err != nil {
		// Record the attempt anyway so a broken network isn't retried on every launch.
		_ = appdir.WriteJSON(cacheFile, Result{Latest: cached.Latest, CheckedAt: c.Now()})
		return Result{Current: current, Latest: cached.Latest}, err
	}
	_ = appdir.WriteJSON(cacheFile, r)
	return r, nil
}

// Check fetches the latest version, bypassing the cache.
func (c *Checker) Check(ctx context.Context, current string) (Result, error) {
	v, err := c.LatestVersion(ctx)
	if err != nil {
		return Result{Current: current}, err
	}
	return Result{Current: current, Latest: v, CheckedAt: c.Now()}, nil
}

// LatestVersion returns the newest known version: main's package.json,
// falling back to the releases and then tags APIs.
func (c *Checker) LatestVersion(ctx context.Context) (string, error) {
	var errs []error
	var pkg struct {
		Version string `json:"version"`
	}
	if err := c.getJSON(ctx, c.ManifestURL, &pkg); err == nil && valid(pkg.Version) {
		return normalize(pkg.Version), nil
	} else if err != nil {
		errs = append(errs, err)
	}
	if v, err := c.LatestRelease(ctx); err == nil {
		return v, nil
	} else {
		errs = append(errs, err)
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := c.getJSON(ctx, c.TagsURL, &tags); err == nil {
		best := ""
		for _, t := range tags {
			if valid(t.Name) && (best == "" || Compare(t.Name, best) > 0) {
				best = normalize(t.Name)
			}
		}
		if best != "" {
			return best, nil
		}
		errs = append(errs, errors.New("no version tags"))
	} else {
		errs = append(errs, err)
	}
	return "", fmt.Errorf("update check: %w", errors.Join(errs...))
}

// LatestRelease returns the newest published GitHub release version.
func (c *Checker) LatestRelease(ctx context.Context) (string, error) {
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := c.getJSON(ctx, c.ReleasesURL, &rel); err != nil {
		return "", err
	}
	if !valid(rel.TagName) {
		return "", errors.New("no release tag")
	}
	return normalize(rel.TagName), nil
}

var errNotFound = errors.New("not found")

func (c *Checker) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	if url == "" {
		return nil, errors.New("no url")
	}
	req, err := newRequest(ctx, http.MethodGet, url)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (c *Checker) getJSON(ctx context.Context, url string, v any) error {
	b, err := c.get(ctx, url, maxMetadataSize)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// --- semver ---------------------------------------------------------------

func normalize(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

func valid(v string) bool {
	_, _, ok := parse(v)
	return ok
}

// parse splits "v1.2.3-rc.1+meta" into numeric core and prerelease.
func parse(v string) (core [3]int, pre string, ok bool) {
	v = normalize(v)
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 || parts[0] == "" {
		return core, "", false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return core, "", false
		}
		core[i] = n
	}
	return core, pre, true
}

// Compare returns -1, 0, or 1 comparing semver a to b. Unparseable
// versions (e.g. "dev") sort below everything.
func Compare(a, b string) int {
	ca, pa, oka := parse(a)
	cb, pb, okb := parse(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := range ca {
		if ca[i] != cb[i] {
			if ca[i] < cb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa == pb:
		return 0
	case pa == "":
		return 1 // release > prerelease
	case pb == "":
		return -1
	}
	return comparePre(pa, pb)
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		na, ea := strconv.Atoi(as[i])
		nb, eb := strconv.Atoi(bs[i])
		switch {
		case ea == nil && eb == nil:
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
		case ea == nil:
			return -1
		case eb == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

func newRequest(ctx context.Context, method, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tjcoder-cli-update")
	return req, nil
}
