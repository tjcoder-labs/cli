package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Method is how the running binary was installed.
type Method string

const (
	MethodNPM    Method = "npm"
	MethodBinary Method = "binary"
	MethodSource Method = "source"
)

// DetectMethod classifies an install. The npm shim spawns
// <pkg>/lib/coder, so a path inside node_modules/@tjcoder/cli is npm.
// Release builds stamp buildMethod="binary" via ldflags; anything else
// (go build, go install, make build) is a source build.
func DetectMethod(exe, buildMethod string) Method {
	p := filepath.ToSlash(exe)
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		p = filepath.ToSlash(real)
	}
	if strings.Contains(p, "node_modules/"+NPMPackage+"/") {
		return MethodNPM
	}
	if buildMethod == string(MethodBinary) {
		return MethodBinary
	}
	return MethodSource
}

// Plan describes what `coder update` will do, for the confirmation prompt.
type Plan struct {
	Method  Method
	Version string // target version
	Command string // npm command (npm) or instructions (source)
	Asset   string // release asset URL (binary)
	Target  string // binary path to replace (binary)
}

// ErrNoBinaryRelease means no published release is newer than current.
var ErrNoBinaryRelease = errors.New("no newer release binary is published yet")

// SourceInstructions is shown for source builds; nothing is executed.
const SourceInstructions = "git pull && make install   (or: go install github.com/" + Repo + "/cmd/coder@latest)"

// Prepare resolves the plan for latest. For binary installs the target
// is the newest *published release* (main's package.json can be ahead
// of the release, whose assets would 404).
func (c *Checker) Prepare(ctx context.Context, method Method, current, latest, exe string) (Plan, error) {
	switch method {
	case MethodNPM:
		return Plan{Method: method, Version: latest, Command: "npm i -g " + NPMPackage + "@latest"}, nil
	case MethodSource:
		return Plan{Method: method, Version: latest, Command: SourceInstructions}, nil
	}
	ver := latest
	asset, err := c.findAsset(ctx, ver)
	if errors.Is(err, errNotFound) {
		rel, rerr := c.LatestRelease(ctx)
		if rerr != nil {
			return Plan{}, rerr
		}
		if Compare(rel, current) <= 0 {
			return Plan{}, fmt.Errorf("%w (latest is v%s, newest release is v%s)", ErrNoBinaryRelease, latest, rel)
		}
		ver = rel
		asset, err = c.findAsset(ctx, ver)
	}
	if err != nil {
		return Plan{}, err
	}
	return Plan{Method: method, Version: ver, Asset: asset, Target: exe}, nil
}

func assetNames(ver string) []string {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	return []string{
		fmt.Sprintf("coder_v%s_%s_%s%s", ver, runtime.GOOS, runtime.GOARCH, ext),
		fmt.Sprintf("coder-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext),
	}
}

func (c *Checker) releaseURL(ver, name string) string {
	return strings.TrimRight(c.DownloadURL, "/") + "/v" + ver + "/" + name
}

// findAsset returns the first asset URL for this platform that exists.
func (c *Checker) findAsset(ctx context.Context, ver string) (string, error) {
	var last error = errNotFound
	for _, n := range assetNames(ver) {
		u := c.releaseURL(ver, n)
		ok, err := c.exists(ctx, u)
		if ok {
			return u, nil
		}
		if err != nil {
			last = err
		}
	}
	return "", last
}

func (c *Checker) exists(ctx context.Context, url string) (bool, error) {
	req, err := newRequest(ctx, "HEAD", url)
	if err != nil {
		return false, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	if resp.StatusCode == 404 {
		return false, nil
	}
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("HEAD %s: %s", url, resp.Status)
	}
	return true, nil
}

// Apply executes a plan. npm runs the global install (stdout/stderr
// passed through); binary downloads, verifies, and atomically swaps the
// executable; source returns an error carrying the instructions.
func (c *Checker) Apply(ctx context.Context, p Plan, out io.Writer) error {
	switch p.Method {
	case MethodNPM:
		cmd := execCommand(ctx, "npm", "i", "-g", NPMPackage+"@latest")
		cmd.Stdout, cmd.Stderr = out, out
		return cmd.Run()
	case MethodBinary:
		return c.applyBinary(ctx, p)
	default:
		return fmt.Errorf("built from source; update with: %s", SourceInstructions)
	}
}

var execCommand = exec.CommandContext

const maxAssetSize = 200 << 20

func (c *Checker) applyBinary(ctx context.Context, p Plan) error {
	archive, err := c.get(ctx, p.Asset, maxAssetSize)
	if err != nil {
		return fmt.Errorf("download %s: %w", p.Asset, err)
	}
	if err := c.verifyChecksum(ctx, p, archive); err != nil {
		return err
	}
	name := "coder"
	if runtime.GOOS == "windows" {
		name = "coder.exe"
	}
	bin, err := extract(p.Asset, archive, name)
	if err != nil {
		return err
	}
	return replaceExecutable(p.Target, bin)
}

// verifyChecksum checks the archive against a published checksum when
// one exists (checksums.txt or <asset>.sha256). None published is OK.
func (c *Checker) verifyChecksum(ctx context.Context, p Plan, archive []byte) error {
	base := p.Asset[strings.LastIndex(p.Asset, "/")+1:]
	dir := p.Asset[:strings.LastIndex(p.Asset, "/")+1]
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	for _, cand := range []string{dir + base + ".sha256", dir + "checksums.txt", dir + "SHA256SUMS"} {
		b, err := c.get(ctx, cand, maxMetadataSize)
		if errors.Is(err, errNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("fetch checksum: %w", err)
		}
		want := ""
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) == 1 && strings.HasSuffix(cand, ".sha256") {
				want = f[0]
			} else if len(f) >= 2 && strings.TrimPrefix(f[len(f)-1], "*") == base {
				want = f[0]
			}
		}
		if want == "" {
			continue
		}
		if !strings.EqualFold(want, got) {
			return fmt.Errorf("checksum mismatch for %s", base)
		}
		return nil
	}
	return nil
}

func extract(assetURL string, archive []byte, name string) ([]byte, error) {
	if strings.HasSuffix(assetURL, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxAssetSize))
			}
		}
		return nil, fmt.Errorf("%s not found in archive", name)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == name {
			return io.ReadAll(io.LimitReader(tr, maxAssetSize))
		}
	}
}

// replaceExecutable writes bin next to target and renames it into place
// so the swap is atomic; the running process keeps its old inode.
func replaceExecutable(target string, bin []byte) error {
	if len(bin) == 0 {
		return errors.New("empty binary in archive")
	}
	if real, err := filepath.EvalSymlinks(target); err == nil {
		target = real
	}
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".coder-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (try reinstalling with the original method): %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// A running .exe cannot be overwritten but can be renamed.
		old := target + ".old"
		_ = os.Remove(old)
		if err := os.Rename(target, old); err != nil {
			return err
		}
	}
	return os.Rename(tmpName, target)
}
