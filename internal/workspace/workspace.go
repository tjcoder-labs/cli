// Package workspace models the folders a session can work in: the
// primary local directory plus any the user adds with /add, local or
// remote over SSH. Tools reach files and run commands through the FS
// backend of the workspace a path resolves to, so a remote workspace
// behaves like a local one for the agent.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// Kind is the backend type of a workspace.
type Kind string

const (
	KindLocal Kind = "local"
	KindSSH   Kind = "ssh"
)

// Info is the subset of file metadata the tools need.
type Info struct {
	IsDir bool
	Mode  os.FileMode
	Size  int64
}

// Entry is one walked path, relative to the walk root, slash-separated.
type Entry struct {
	Rel   string
	IsDir bool
}

// ErrNotExist is returned (wrapped) by Stat and ReadFile for a missing
// path on any backend; errors.Is(err, os.ErrNotExist) also holds.
var ErrNotExist = os.ErrNotExist

// FS is a workspace backend. Paths are absolute in the backend's own
// syntax (filepath for local, POSIX for SSH); containment is enforced
// by Workspace.Resolve before any FS call.
type FS interface {
	Kind() Kind
	ReadFile(ctx context.Context, p string) ([]byte, error)
	// WriteFile replaces p atomically (temp file + rename), creating
	// parents and keeping an existing file's mode.
	WriteFile(ctx context.Context, p string, data []byte) error
	// CreateFile writes p only if it does not exist yet.
	CreateFile(ctx context.Context, p string, data []byte) error
	AppendFile(ctx context.Context, p string, data []byte) error
	Stat(ctx context.Context, p string) (Info, error)
	Remove(ctx context.Context, p string) error
	// Rename moves src to dst, creating dst's parents.
	Rename(ctx context.Context, src, dst string) error
	// Walk lists root's tree up to depth levels (<=0: unlimited),
	// skipping .git and node_modules.
	Walk(ctx context.Context, root string, depth int) ([]Entry, error)
	// Realpath resolves symlinks; for a path that does not exist yet
	// the longest existing prefix is resolved and the rest appended.
	Realpath(ctx context.Context, p string) (string, error)
	// Command prepares script to run with a login shell in dir.
	Command(ctx context.Context, dir, script string, env []string) (*exec.Cmd, error)
}

// Workspace is one named root.
type Workspace struct {
	Name string // short name: folder basename or ssh alias (unique in a Set)
	Kind Kind
	Root string // absolute, symlinks resolved
	Host string // ssh alias (KindSSH only)
	FS   FS
}

// Label is the user-facing form: "name (local: /root)" / "name (ssh host:/root)".
func (w *Workspace) Label() string {
	if w.Kind == KindSSH {
		return fmt.Sprintf("%s (ssh %s:%s)", w.Name, w.Host, w.Root)
	}
	return fmt.Sprintf("%s (local: %s)", w.Name, w.Root)
}

// Spec is the persisted form of an added workspace.
type Spec struct {
	Kind Kind   `json:"kind"`
	Path string `json:"path"`
	Host string `json:"host,omitempty"`
	Name string `json:"name,omitempty"`
}

func (w *Workspace) Spec() Spec { return Spec{Kind: w.Kind, Path: w.Root, Host: w.Host, Name: w.Name} }

func (w *Workspace) isAbs(p string) bool {
	if w.Kind == KindSSH {
		return strings.HasPrefix(p, "/")
	}
	return filepath.IsAbs(p)
}

func (w *Workspace) join(root, p string) string {
	if w.Kind == KindSSH {
		if strings.HasPrefix(p, "/") {
			return path.Clean(p)
		}
		return path.Join(root, p)
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, p)
}

// Within reports whether p is root or inside it. Comparison is by path
// components (Rel), so /a/bc is never inside /a/b.
func within(kind Kind, root, p string) bool {
	if kind == KindSSH {
		root, p = path.Clean(root), path.Clean(p)
		if root == "/" {
			return strings.HasPrefix(p, "/")
		}
		return p == root || strings.HasPrefix(p, root+"/")
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// Resolve turns p (relative to the root, or absolute) into an absolute
// backend path inside the workspace. ".." escapes and symlinks pointing
// outside the root are refused; symlinks are resolved on the backend
// (EvalSymlinks locally, the remote shell over SSH).
func (w *Workspace) Resolve(ctx context.Context, p string) (string, error) {
	joined := w.join(w.Root, p)
	if !within(w.Kind, w.Root, joined) {
		return "", fmt.Errorf("path %q escapes workspace %s", p, w.Name)
	}
	real, err := w.FS.Realpath(ctx, joined)
	if err != nil {
		return "", err
	}
	if !within(w.Kind, w.Root, real) {
		return "", fmt.Errorf("path %q resolves outside workspace %s (symlink)", p, w.Name)
	}
	return real, nil
}

// Rel renders an absolute backend path relative to the root.
func (w *Workspace) Rel(p string) string {
	if w.Kind == KindSSH {
		r := strings.TrimPrefix(strings.TrimPrefix(path.Clean(p), path.Clean(w.Root)), "/")
		if r == "" {
			return "."
		}
		return r
	}
	r, err := filepath.Rel(w.Root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

// Set is the session's workspaces. The first is the primary (the shell
// cwd and default). Safe for concurrent use: the UI mutates it while
// tools read it from the runner goroutine.
type Set struct {
	mu     sync.RWMutex
	list   []*Workspace
	active string
}

// NewSet builds a set whose primary workspace is the local dir root.
func NewSet(root string) *Set {
	primary := NewLocal(root, "")
	return &Set{list: []*Workspace{primary}, active: primary.Name}
}

// Primary returns the first workspace.
func (s *Set) Primary() *Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.list[0]
}

// All returns a snapshot, primary first.
func (s *Set) All() []*Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*Workspace(nil), s.list...)
}

// Extra returns the added (non-primary) workspaces.
func (s *Set) Extra() []*Workspace {
	all := s.All()
	return all[1:]
}

// Get finds a workspace by name (case-insensitive).
func (s *Set) Get(name string) (*Workspace, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range s.list {
		if strings.EqualFold(w.Name, name) {
			return w, true
		}
	}
	return nil, false
}

// Active is the default target for unqualified relative paths and
// commands; the primary unless the user picked another (/use).
func (s *Set) Active() *Workspace {
	if w, ok := s.Get(s.ActiveName()); ok {
		return w
	}
	return s.Primary()
}

func (s *Set) ActiveName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// SetActive selects the default workspace by name.
func (s *Set) SetActive(name string) error {
	w, ok := s.Get(name)
	if !ok {
		return fmt.Errorf("no workspace named %q", name)
	}
	s.mu.Lock()
	s.active = w.Name
	s.mu.Unlock()
	return nil
}

// Add appends w, giving it a unique name (suffix -2, -3, ...). Adding a
// root that is already present returns the existing workspace and false.
func (s *Set) Add(w *Workspace) (*Workspace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.list {
		if e.Kind == w.Kind && e.Host == w.Host && e.Root == w.Root {
			return e, false
		}
	}
	base := w.Name
	if base == "" {
		base = "ws"
	}
	name := base
	for i := 2; s.nameTakenLocked(name); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	w.Name = name
	s.list = append(s.list, w)
	return w, true
}

func (s *Set) nameTakenLocked(name string) bool {
	for _, e := range s.list {
		if strings.EqualFold(e.Name, name) {
			return true
		}
	}
	return false
}

// Remove drops an added workspace by name. The primary cannot be
// removed; removing the active one makes the primary active again.
func (s *Set) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, w := range s.list {
		if !strings.EqualFold(w.Name, name) {
			continue
		}
		if i == 0 {
			return errors.New("the primary workspace cannot be removed")
		}
		s.list = append(s.list[:i], s.list[i+1:]...)
		if strings.EqualFold(s.active, w.Name) {
			s.active = s.list[0].Name
		}
		if c, ok := w.FS.(interface{ Close() }); ok {
			go c.Close()
		}
		return nil
	}
	return fmt.Errorf("no workspace named %q", name)
}

// Specs returns the added workspaces in persisted form.
func (s *Set) Specs() []Spec {
	var out []Spec
	for _, w := range s.Extra() {
		out = append(out, w.Spec())
	}
	return out
}

// Target picks the workspace and absolute path for a tool argument:
//
//  1. an explicit workspace name (the tool's "workspace" parameter);
//  2. a "name:relative/path" prefix naming a workspace;
//  3. an absolute local path inside a local workspace;
//  4. otherwise the active workspace.
//
// The path is then resolved and contained by Workspace.Resolve.
func (s *Set) Target(ctx context.Context, wsName, p string) (*Workspace, string, error) {
	w, rest, err := s.pick(wsName, p)
	if err != nil {
		return nil, "", err
	}
	abs, err := w.Resolve(ctx, rest)
	if err != nil {
		return nil, "", err
	}
	return w, abs, nil
}

// Pick is Target without resolving: the workspace and the path within it.
func (s *Set) Pick(wsName, p string) (*Workspace, string, error) { return s.pick(wsName, p) }

func (s *Set) pick(wsName, p string) (*Workspace, string, error) {
	if wsName = strings.TrimSpace(wsName); wsName != "" {
		w, ok := s.Get(wsName)
		if !ok {
			return nil, "", fmt.Errorf("unknown workspace %q (have: %s)", wsName, strings.Join(s.Names(), ", "))
		}
		if n, rest, ok := splitPrefix(p); ok && strings.EqualFold(n, w.Name) {
			p = rest
		}
		return w, p, nil
	}
	if n, rest, ok := splitPrefix(p); ok {
		if w, ok := s.Get(n); ok {
			return w, rest, nil
		}
	}
	active := s.Active()
	if filepath.IsAbs(p) && active.Kind == KindLocal {
		for _, w := range s.All() {
			if w.Kind == KindLocal && within(KindLocal, w.Root, p) {
				return w, p, nil
			}
		}
	}
	return active, p, nil
}

// splitPrefix splits "name:rest" where name has no path separator.
func splitPrefix(p string) (string, string, bool) {
	i := strings.Index(p, ":")
	if i <= 0 {
		return "", "", false
	}
	name := p[:i]
	if strings.ContainsAny(name, `/\ `) {
		return "", "", false
	}
	return name, p[i+1:], true
}

// Names lists workspace names, primary first.
func (s *Set) Names() []string {
	var out []string
	for _, w := range s.All() {
		out = append(out, w.Name)
	}
	return out
}

// PromptBlock describes the workspaces for the system prompt. Empty when
// only the primary exists (nothing new to tell the model).
func (s *Set) PromptBlock() string {
	all := s.All()
	if len(all) < 2 {
		return ""
	}
	active := s.ActiveName()
	var b strings.Builder
	b.WriteString("[workspaces]\n")
	b.WriteString("This session spans several workspaces. File tools and run_command take an optional \"workspace\" parameter (a name below); a path may also be written as name:relative/path. Without either, relative paths and commands target the active workspace. SSH workspaces behave exactly like local ones: the same tools read, write, search and run commands on the remote host.\n")
	for i, w := range all {
		tags := []string{string(w.Kind)}
		if i == 0 {
			tags = append(tags, "primary")
		}
		if strings.EqualFold(w.Name, active) {
			tags = append(tags, "active")
		}
		root := w.Root
		if w.Kind == KindSSH {
			root = w.Host + ":" + w.Root
		}
		fmt.Fprintf(&b, "- %s [%s] %s\n", w.Name, strings.Join(tags, ", "), root)
	}
	return strings.TrimRight(b.String(), "\n")
}
