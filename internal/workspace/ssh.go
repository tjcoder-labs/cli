package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Runner builds the process for one ssh invocation. Tests inject a fake
// ssh; production uses exec.CommandContext.
type Runner func(ctx context.Context, name string, args ...string) *exec.Cmd

// SSHFS runs every file operation as a POSIX sh script over the system
// ssh binary, addressed by the ~/.ssh/config alias only, so HostName,
// User, Port, IdentityFile, ProxyJump and Match rules all come from the
// user's own config. Connections are multiplexed through a ControlMaster
// socket under the user cache dir: the first operation authenticates
// (prompting through SSH_ASKPASS when needed) and later ones reuse it.
type SSHFS struct {
	Host       string
	SSHBin     string   // default "ssh"
	ControlDir string   // default <UserCacheDir>/coder/ssh
	Persist    string   // ControlPersist, default "10m"
	AuthEnv    []string // askpass bridge env (SSH_ASKPASS & co)
	Run        Runner

	mu      sync.Mutex
	alive   time.Time // last successful master check
	connErr error
}

// NewSSHFS returns a backend for alias with the given askpass env.
func NewSSHFS(alias string, authEnv []string) *SSHFS {
	return &SSHFS{Host: alias, AuthEnv: authEnv}
}

func (s *SSHFS) Kind() Kind { return KindSSH }

func (s *SSHFS) bin() string {
	if s.SSHBin != "" {
		return s.SSHBin
	}
	return "ssh"
}

func (s *SSHFS) runner() Runner {
	if s.Run != nil {
		return s.Run
	}
	return exec.CommandContext
}

// ControlPath is the multiplexing socket pattern (%C = hash of the
// connection tuple, so it stays short and unique per host/user/port).
func (s *SSHFS) ControlPath() string {
	dir := s.ControlDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		dir = filepath.Join(base, "coder", "ssh")
	}
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "%C")
}

func (s *SSHFS) commonOpts() []string {
	return []string{
		"-o", "ControlPath=" + s.ControlPath(),
		"-o", "ServerAliveInterval=30",
		"-o", "ConnectTimeout=20",
	}
}

func (s *SSHFS) env() []string {
	return append(os.Environ(), s.AuthEnv...)
}

func (s *SSHFS) cmd(ctx context.Context, args ...string) *exec.Cmd {
	c := s.runner()(ctx, s.bin(), args...)
	c.Env = s.env()
	detach(c)
	return c
}

// Connect makes sure a ControlMaster for the host is running,
// authenticating if needed. Prompts (password, passphrase, OTP, host
// key) arrive through SSH_ASKPASS; without an answer it fails cleanly.
func (s *SSHFS) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.alive) < 15*time.Second {
		return nil
	}
	check := s.cmd(ctx, append(append([]string{"-O", "check"}, s.commonOpts()...), s.Host)...)
	if err := check.Run(); err == nil {
		s.alive = time.Now()
		return nil
	}
	persist := s.Persist
	if persist == "" {
		persist = "10m"
	}
	args := append([]string{"-M", "-N", "-f",
		"-o", "ControlMaster=yes",
		"-o", "ControlPersist=" + persist,
	}, s.commonOpts()...)
	args = append(args, s.Host)
	master := s.cmd(ctx, args...)
	// The backgrounded master keeps stderr open for its lifetime; a
	// pipe would make Wait hang, so stderr goes to a temp file.
	errf, err := os.CreateTemp("", "coder-ssh-*.log")
	if err != nil {
		return err
	}
	defer os.Remove(errf.Name())
	defer errf.Close()
	master.Stderr = errf
	master.Stdout = nil
	master.Stdin = nil
	if err := master.Run(); err != nil {
		msg, _ := os.ReadFile(errf.Name())
		s.connErr = fmt.Errorf("ssh %s: %v: %s", s.Host, err, strings.TrimSpace(string(msg)))
		return s.connErr
	}
	s.alive = time.Now()
	s.connErr = nil
	return nil
}

// Close stops the ControlMaster (best effort).
func (s *SSHFS) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.cmd(ctx, append(append([]string{"-O", "exit"}, s.commonOpts()...), s.Host)...).Run()
}

// remoteArgs builds "ssh <opts> -T host -- <cmdline>". The command line
// is a single sh -c invocation with POSIX single-quote quoting.
func (s *SSHFS) remoteArgs(script string) []string {
	args := append([]string{"-T", "-o", "ControlMaster=no", "-o", "BatchMode=no"}, s.commonOpts()...)
	return append(args, s.Host, "--", "sh -c "+Quote(script))
}

// exit codes the helper scripts use for typed errors
const (
	exitNotExist = 3
	exitIsDir    = 4
	exitExists   = 5
	exitDangling = 6
	exitNotDir   = 7
)

type remoteErr struct {
	code   int
	stderr string
}

func (e *remoteErr) Error() string {
	if e.stderr != "" {
		return e.stderr
	}
	return fmt.Sprintf("remote command exited %d", e.code)
}

// run executes script remotely with optional stdin, returning stdout.
func (s *SSHFS) run(ctx context.Context, script string, stdin []byte, p string) ([]byte, error) {
	if err := s.Connect(ctx); err != nil {
		return nil, err
	}
	c := s.cmd(ctx, s.remoteArgs(script)...)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	err := c.Run()
	if err == nil {
		return out.Bytes(), nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(errb.String())
		switch ee.ExitCode() {
		case exitNotExist:
			return nil, &os.PathError{Op: "open", Path: p, Err: os.ErrNotExist}
		case exitIsDir:
			return nil, fmt.Errorf("%s is a directory", p)
		case exitExists:
			return nil, &os.PathError{Op: "create", Path: p, Err: os.ErrExist}
		case exitDangling:
			return nil, fmt.Errorf("%s is a dangling symlink", p)
		case exitNotDir:
			return nil, fmt.Errorf("%s is not a directory", p)
		case 255:
			// ssh itself failed: drop the cached liveness.
			s.mu.Lock()
			s.alive = time.Time{}
			s.mu.Unlock()
			return nil, fmt.Errorf("ssh %s: %s", s.Host, msg)
		}
		return nil, &remoteErr{code: ee.ExitCode(), stderr: msg}
	}
	return nil, err
}

// Quote single-quotes s for a POSIX shell.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (s *SSHFS) ReadFile(ctx context.Context, p string) ([]byte, error) {
	q := Quote(p)
	return s.run(ctx, fmt.Sprintf(`[ -e %[1]s ] || exit %[2]d; [ -d %[1]s ] && exit %[3]d; cat -- %[1]s`, q, exitNotExist, exitIsDir), nil, p)
}

// modeScript copies the existing file mode onto the temp file (GNU or
// BSD stat), defaulting to 0644 for new files.
const modeScript = `if [ -e "$p" ]; then m=$(stat -c %a -- "$p" 2>/dev/null || stat -f %Lp -- "$p" 2>/dev/null) && chmod "$m" "$t"; else chmod 644 "$t"; fi`

func (s *SSHFS) WriteFile(ctx context.Context, p string, data []byte) error {
	script := `p=` + Quote(p) + `; [ -d "$p" ] && exit ` + strconv.Itoa(exitIsDir) + `
d=$(dirname -- "$p"); mkdir -p -- "$d" || exit 1
t=$(mktemp "$d/.coder-XXXXXX") || exit 1
trap 'rm -f -- "$t"' EXIT
cat > "$t" || exit 1
` + modeScript + `
mv -f -- "$t" "$p" || exit 1
trap - EXIT`
	_, err := s.run(ctx, script, nonNil(data), p)
	return err
}

func (s *SSHFS) CreateFile(ctx context.Context, p string, data []byte) error {
	script := `p=` + Quote(p) + `; [ -e "$p" ] && exit ` + strconv.Itoa(exitExists) + `
mkdir -p -- "$(dirname -- "$p")" || exit 1
( set -C; cat > "$p" ) 2>/dev/null || exit ` + strconv.Itoa(exitExists)
	_, err := s.run(ctx, script, nonNil(data), p)
	return err
}

func (s *SSHFS) AppendFile(ctx context.Context, p string, data []byte) error {
	script := `p=` + Quote(p) + `; [ -d "$p" ] && exit ` + strconv.Itoa(exitIsDir) + `
mkdir -p -- "$(dirname -- "$p")" || exit 1
cat >> "$p"`
	_, err := s.run(ctx, script, nonNil(data), p)
	return err
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

func (s *SSHFS) Stat(ctx context.Context, p string) (Info, error) {
	script := `p=` + Quote(p) + `; [ -e "$p" ] || exit ` + strconv.Itoa(exitNotExist) + `
m=$(stat -c %a -- "$p" 2>/dev/null || stat -f %Lp -- "$p" 2>/dev/null || echo 644)
if [ -d "$p" ]; then printf 'd %s 0' "$m"; else printf 'f %s %s' "$m" "$(wc -c < "$p" | tr -d ' ')"; fi`
	out, err := s.run(ctx, script, nil, p)
	if err != nil {
		return Info{}, err
	}
	f := strings.Fields(string(out))
	if len(f) != 3 {
		return Info{}, fmt.Errorf("stat %s: unexpected output %q", p, out)
	}
	mode, _ := strconv.ParseUint(f[1], 8, 32)
	size, _ := strconv.ParseInt(f[2], 10, 64)
	info := Info{IsDir: f[0] == "d", Mode: os.FileMode(mode), Size: size}
	if info.IsDir {
		info.Mode |= os.ModeDir
	}
	return info, nil
}

func (s *SSHFS) Remove(ctx context.Context, p string) error {
	q := Quote(p)
	_, err := s.run(ctx, fmt.Sprintf(`[ -e %[1]s ] || [ -L %[1]s ] || exit %[2]d; [ -d %[1]s ] && exit %[3]d; rm -f -- %[1]s`, q, exitNotExist, exitIsDir), nil, p)
	return err
}

func (s *SSHFS) Rename(ctx context.Context, src, dst string) error {
	script := fmt.Sprintf(`[ -e %[1]s ] || exit %[3]d; mkdir -p -- "$(dirname -- %[2]s)" && mv -- %[1]s %[2]s`, Quote(src), Quote(dst), exitNotExist)
	_, err := s.run(ctx, script, nil, src)
	return err
}

// Walk lists with find: directories first, then an empty record, then
// everything else, all NUL-separated (portable to GNU and BSD find).
func (s *SSHFS) Walk(ctx context.Context, root string, depth int) ([]Entry, error) {
	maxd := ""
	if depth > 0 {
		maxd = " -maxdepth " + strconv.Itoa(depth)
	}
	expr := `find . -mindepth 1` + maxd + ` \( -name .git -o -name node_modules \) -prune -o `
	script := `cd -- ` + Quote(root) + ` 2>/dev/null || exit ` + strconv.Itoa(exitNotDir) + `
` + expr + `-type d -print0; printf '\0'; ` + expr + `! -type d -print0`
	out, err := s.run(ctx, script, nil, root)
	if err != nil {
		return nil, err
	}
	return parseWalk(out), nil
}

func parseWalk(out []byte) []Entry {
	var entries []Entry
	dirs := true
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			if dirs {
				dirs = false
			}
			continue
		}
		rel := strings.TrimPrefix(string(rec), "./")
		entries = append(entries, Entry{Rel: rel, IsDir: dirs})
	}
	return entries
}

// realpathScript resolves the longest existing prefix of $p with
// cd -P/pwd -P (readlink -f for a symlinked last component) and
// re-appends the missing tail.
const realpathScript = `tail=
while [ ! -e "$p" ] && [ ! -L "$p" ]; do
  [ "$p" = / ] && break
  tail="/$(basename -- "$p")$tail"; p=$(dirname -- "$p")
done
if [ -L "$p" ] && [ ! -e "$p" ]; then exit 6; fi
if [ -d "$p" ]; then r=$(cd -P -- "$p" && pwd -P) || exit 1
elif [ -L "$p" ]; then r=$(readlink -f -- "$p" 2>/dev/null || realpath -- "$p" 2>/dev/null) || exit 1
else d=$(cd -P -- "$(dirname -- "$p")" && pwd -P) || exit 1; r="${d%/}/$(basename -- "$p")"
fi
printf '%s%s' "$r" "$tail"`

func (s *SSHFS) Realpath(ctx context.Context, p string) (string, error) {
	out, err := s.run(ctx, `p=`+Quote(path.Clean(p))+"\n"+realpathScript, nil, p)
	if err != nil {
		return "", err
	}
	r := string(out)
	if !strings.HasPrefix(r, "/") {
		return "", fmt.Errorf("realpath %s: unexpected output %q", p, r)
	}
	return path.Clean(r), nil
}

// Home is the remote $HOME.
func (s *SSHFS) Home(ctx context.Context) (string, error) {
	out, err := s.run(ctx, `printf '%s' "$HOME"`, nil, "~")
	if err != nil {
		return "", err
	}
	h := strings.TrimSpace(string(out))
	if !strings.HasPrefix(h, "/") {
		return "", fmt.Errorf("remote $HOME is %q", h)
	}
	return h, nil
}

// ListDirs returns the sub-directories of dir (for /add path completion).
func (s *SSHFS) ListDirs(ctx context.Context, dir string) ([]string, error) {
	script := `cd -- ` + Quote(dir) + ` 2>/dev/null || exit ` + strconv.Itoa(exitNotDir) + `
for f in * .[!.]*; do [ -d "$f" ] && printf '%s\0' "$f"; done; true`
	out, err := s.run(ctx, script, nil, dir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) > 0 {
			dirs = append(dirs, string(rec))
		}
	}
	return dirs, nil
}

// envNameRe guards exported variable names in remote command prefixes.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RemoteScript wraps script as "cd <dir> && <exports> exec bash -lc …"
// (sh when bash is missing). Exposed for tests and the sudo gate.
func RemoteScript(dir, script string, env []string) string {
	var b strings.Builder
	b.WriteString("cd -- " + Quote(dir) + " || exit 1\n")
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !envNameRe.MatchString(k) {
			continue
		}
		b.WriteString("export " + k + "=" + Quote(v) + "\n")
	}
	q := Quote(script)
	b.WriteString("if command -v bash >/dev/null 2>&1; then exec bash -lc " + q + "; else exec sh -lc " + q + "; fi")
	return b.String()
}

// Command prepares an ssh process that runs script in dir on the host.
// Connect runs first so an authentication prompt happens before the
// caller wires stdio; the returned Cmd only reuses the master.
func (s *SSHFS) Command(ctx context.Context, dir, script string, env []string) (*exec.Cmd, error) {
	if err := s.Connect(ctx); err != nil {
		return nil, err
	}
	return s.cmd(ctx, s.remoteArgs(RemoteScript(dir, script, env))...), nil
}

// NewSSH connects to alias and returns a workspace rooted at remotePath
// ("" or "~" = remote $HOME; relative and ~/ paths are under $HOME).
// The root must exist and be a directory.
func NewSSH(ctx context.Context, alias, remotePath string, fs *SSHFS) (*Workspace, error) {
	if fs == nil {
		fs = NewSSHFS(alias, nil)
	}
	if err := fs.Connect(ctx); err != nil {
		return nil, err
	}
	p := strings.TrimSpace(remotePath)
	if p == "" || p == "~" || !strings.HasPrefix(p, "/") {
		home, err := fs.Home(ctx)
		if err != nil {
			return nil, err
		}
		p = path.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	real, err := fs.Realpath(ctx, p)
	if err != nil {
		return nil, err
	}
	st, err := fs.Stat(ctx, real)
	if err != nil {
		return nil, fmt.Errorf("%s:%s: %w", alias, p, err)
	}
	if !st.IsDir {
		return nil, fmt.Errorf("%s:%s is not a directory", alias, p)
	}
	return &Workspace{Name: alias, Kind: KindSSH, Root: real, Host: alias, FS: fs}, nil
}

// ParseSSHTarget splits "host[:path]".
func ParseSSHTarget(s string) (host, p string) {
	host, p, _ = strings.Cut(strings.TrimSpace(s), ":")
	return host, p
}
