// Package wstest provides a fake ssh client for tests of code built on
// the workspace layer. "The remote" is this machine: remote commands
// run with the local sh, so tests exercise the real SSHFS scripts,
// quoting and exit codes without an sshd.
package wstest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tjcoder-labs/cli/internal/workspace"
)

// fakeSSH: -O check/exit consult a master marker, -M -N -f creates it,
// anything else runs the remote command with sh. FAKE_PATH, when set,
// is prepended to PATH for remote commands; a `bash` there can stand
// in for the login shell.
const fakeSSH = `#!/bin/sh
mode=run; host=
while [ $# -gt 0 ]; do
  case "$1" in
    -o) shift 2;;
    -O) mode="ctl-$2"; shift 2;;
    -N) mode=master; shift;;
    --) shift; break;;
    -*) shift;;
    *) if [ -z "$host" ]; then host=$1; shift; else break; fi;;
  esac
done
printf '%s\n' "$mode" >> "$FAKE_SSH_DIR/log"
case "$mode" in
  ctl-check) [ -e "$FAKE_SSH_DIR/master" ] && exit 0; exit 255;;
  ctl-exit) rm -f "$FAKE_SSH_DIR/master"; exit 0;;
  master) : > "$FAKE_SSH_DIR/master"; exit 0;;
  run)
    [ -e "$FAKE_SSH_DIR/master" ] || { echo "no master" >&2; exit 255; }
    printf 'script %s\n' "$1" >> "$FAKE_SSH_DIR/log"
    [ -n "$FAKE_PATH" ] && PATH="$FAKE_PATH:$PATH" && export PATH
    exec sh -c "$1";;
esac
`

// Remote is a fake SSH workspace and its directories.
type Remote struct {
	WS   *workspace.Workspace
	FS   *workspace.SSHFS
	Root string // the "remote" root (a local temp dir, realpath'd)
	Dir  string // fake ssh state (log, master marker)
}

// New returns an SSH workspace for host whose root is a fresh temp dir.
// env is added to every ssh invocation (e.g. FAKE_PATH=...).
func New(t testing.TB, host string, env ...string) *Remote {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ssh")
	if err := os.WriteFile(bin, []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fs := &workspace.SSHFS{
		Host:       host,
		ControlDir: filepath.Join(dir, "ctl"),
		AuthEnv:    append([]string{"FAKE_SSH_DIR=" + dir}, env...),
		Run: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, bin, args...)
		},
	}
	ws, err := workspace.NewSSH(context.Background(), host, root, fs)
	if err != nil {
		t.Fatal(err)
	}
	return &Remote{WS: ws, FS: fs, Root: root, Dir: dir}
}

// Log is the fake ssh invocation log (one mode per line).
func (r *Remote) Log() string {
	b, _ := os.ReadFile(filepath.Join(r.Dir, "log"))
	return string(b)
}
