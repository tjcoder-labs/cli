package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NewLocal returns a local workspace for dir (made absolute, symlinks
// resolved). name defaults to the folder basename.
func NewLocal(dir, name string) *Workspace {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if name == "" {
		name = filepath.Base(abs)
		if name == "/" || name == "." || name == "" {
			name = "root"
		}
	}
	return &Workspace{Name: name, Kind: KindLocal, Root: abs, FS: LocalFS{}}
}

// LocalFS is the os-backed FS.
type LocalFS struct{}

func (LocalFS) Kind() Kind { return KindLocal }

func (LocalFS) ReadFile(_ context.Context, p string) ([]byte, error) { return os.ReadFile(p) }

func (LocalFS) WriteFile(_ context.Context, p string, data []byte) error {
	return atomicWriteLocal(p, data)
}

func atomicWriteLocal(p string, data []byte) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(p)+".coder-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, p); err != nil {
		return err
	}
	ok = true
	return nil
}

func (LocalFS) CreateFile(_ context.Context, p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (LocalFS) AppendFile(_ context.Context, p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (LocalFS) Stat(_ context.Context, p string) (Info, error) {
	st, err := os.Stat(p)
	if err != nil {
		return Info{}, err
	}
	return Info{IsDir: st.IsDir(), Mode: st.Mode(), Size: st.Size()}, nil
}

func (LocalFS) Remove(_ context.Context, p string) error { return os.Remove(p) }

func (LocalFS) Rename(_ context.Context, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// skipDir reports directories every walk skips.
func skipDir(name string) bool { return name == ".git" || name == "node_modules" }

func (LocalFS) Walk(ctx context.Context, root string, depth int) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		out = append(out, Entry{Rel: rel, IsDir: d.IsDir()})
		if d.IsDir() && depth > 0 && strings.Count(rel, "/")+1 >= depth {
			return filepath.SkipDir
		}
		return nil
	})
	return out, err
}

// Realpath resolves symlinks in the longest existing prefix of p and
// appends the not-yet-existing remainder, so a file about to be
// created is still checked against where its parent really lives.
func (LocalFS) Realpath(_ context.Context, p string) (string, error) {
	p = filepath.Clean(p)
	var tail []string
	cur := p
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			parts := append([]string{real}, tail...)
			return filepath.Join(parts...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if _, lerr := os.Lstat(cur); lerr == nil {
			// A dangling symlink: its target is unknown territory.
			return "", fmt.Errorf("%s is a dangling symlink", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, nil
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}

func (LocalFS) Command(ctx context.Context, dir, script string, env []string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "bash", "-lc", script)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd, nil
}
