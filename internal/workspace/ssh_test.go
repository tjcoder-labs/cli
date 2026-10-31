package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeSSH emulates the ssh client: options are ignored, "-O check|exit"
// consult a master marker file, "-M -N -f" authenticates by invoking
// $SSH_ASKPASS for each prompt in FAKE_PROMPTS ('|'-separated) and
// comparing with FAKE_A1, FAKE_A2, ..., and a remote command is run
// with the local sh, so "the remote" is this machine.
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
log() { printf '%s\n' "$*" >> "$FAKE_SSH_DIR/log"; }
case "$mode" in
  ctl-check) [ -e "$FAKE_SSH_DIR/master" ] && exit 0; exit 255;;
  ctl-exit) rm -f "$FAKE_SSH_DIR/master"; exit 0;;
  master)
    log "connect $host require=$SSH_ASKPASS_REQUIRE"
    i=0
    IFS='|'
    for prompt in $FAKE_PROMPTS; do
      i=$((i+1))
      ans=$("$SSH_ASKPASS" "$prompt") || { echo "Permission denied (keyboard-interactive)." >&2; exit 255; }
      eval "exp=\$FAKE_A$i"
      [ "$ans" = "$exp" ] || { echo "Permission denied, please try again." >&2; exit 255; }
      log "answered prompt $i"
    done
    : > "$FAKE_SSH_DIR/master"; exit 0;;
  run)
    [ -e "$FAKE_SSH_DIR/master" ] || { echo "no master" >&2; exit 255; }
    log "run"
    exec sh -c "$1";;
esac
`

type fakeRemote struct {
	dir  string
	bin  string
	env  []string
	fs   *SSHFS
	asks []AskRequest
	mu   sync.Mutex
}

func newFakeRemote(t *testing.T, env ...string) *fakeRemote {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ssh")
	if err := os.WriteFile(bin, []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRemote{dir: dir, bin: bin, env: append([]string{"FAKE_SSH_DIR=" + dir}, env...)}
	fr.fs = &SSHFS{
		Host:       "devbox",
		ControlDir: filepath.Join(dir, "ctl"),
		Run: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			c := exec.CommandContext(ctx, bin, args...)
			return c
		},
	}
	fr.fs.AuthEnv = fr.env
	return fr
}

func (fr *fakeRemote) withBridge(t *testing.T, answer func(AskRequest) ([]byte, bool)) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBridge(exe, func(ctx context.Context, r AskRequest) ([]byte, bool) {
		fr.mu.Lock()
		fr.asks = append(fr.asks, r)
		fr.mu.Unlock()
		return answer(r)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	fr.fs.AuthEnv = append(append([]string(nil), fr.env...), b.Env("devbox")...)
}

func (fr *fakeRemote) askList() []AskRequest {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return append([]AskRequest(nil), fr.asks...)
}

func (fr *fakeRemote) log() string {
	b, _ := os.ReadFile(filepath.Join(fr.dir, "log"))
	return string(b)
}

func TestSSHFSOpsViaFakeRunner(t *testing.T) {
	ctx := context.Background()
	fr := newFakeRemote(t)
	remote := t.TempDir()
	writeFile(t, filepath.Join(remote, "src", "main.go"), "package main\n")
	writeFile(t, filepath.Join(remote, ".git", "HEAD"), "ref")
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret"), "s")
	os.Symlink(outside, filepath.Join(remote, "escape"))

	w, err := NewSSH(ctx, "devbox", remote, fr.fs)
	if err != nil {
		t.Fatal(err)
	}
	if w.Kind != KindSSH || w.Name != "devbox" || w.Host != "devbox" {
		t.Fatalf("workspace = %+v", w)
	}
	real, _ := filepath.EvalSymlinks(remote)
	if w.Root != real {
		t.Fatalf("root = %q want %q", w.Root, real)
	}
	p, err := w.Resolve(ctx, "src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.FS.ReadFile(ctx, p)
	if err != nil || string(b) != "package main\n" {
		t.Fatalf("read = %q %v", b, err)
	}
	if _, err := w.FS.ReadFile(ctx, filepath.Join(real, "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing read err = %v", err)
	}
	// A quote, a dollar and a newline survive the shell quoting.
	tricky := "x'y $HOME `id`\nline2\x00bin"
	np, _ := w.Resolve(ctx, "new dir/it's.txt")
	if err := w.FS.WriteFile(ctx, np, []byte(tricky)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(real, "new dir", "it's.txt")); string(got) != tricky {
		t.Errorf("written = %q", got)
	}
	os.Chmod(np, 0o600)
	w.FS.WriteFile(ctx, np, []byte("v2"))
	if st, _ := os.Stat(np); st.Mode().Perm() != 0o600 {
		t.Errorf("mode lost on rewrite: %v", st.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(real, "new dir", ".coder-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	if err := w.FS.CreateFile(ctx, np, []byte("x")); !errors.Is(err, os.ErrExist) {
		t.Errorf("create over existing = %v", err)
	}
	w.FS.AppendFile(ctx, np, []byte("+"))
	st, err := w.FS.Stat(ctx, np)
	if err != nil || st.IsDir || st.Size != 3 || st.Mode.Perm() != 0o600 {
		t.Errorf("stat = %+v %v", st, err)
	}
	if st, _ := w.FS.Stat(ctx, real); !st.IsDir {
		t.Error("root not a dir")
	}
	ents, err := w.FS.Walk(ctx, real, 0)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, e := range ents {
		if e.IsDir {
			rels = append(rels, e.Rel+"/")
		} else {
			rels = append(rels, e.Rel)
		}
	}
	sort.Strings(rels)
	want := []string{"escape", "new dir/", "new dir/it's.txt", "src/", "src/main.go"}
	if !reflect.DeepEqual(rels, want) {
		t.Errorf("walk = %q want %q", rels, want)
	}
	dst, _ := w.Resolve(ctx, "moved/m.go")
	if err := w.FS.Rename(ctx, p, dst); err != nil {
		t.Fatal(err)
	}
	if err := w.FS.Remove(ctx, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(real, "moved", "m.go")); !os.IsNotExist(err) {
		t.Error("remove failed")
	}
	// Containment uses the remote realpath.
	for _, bad := range []string{"escape/secret", "escape/new", "../x", "/etc/passwd"} {
		if p, err := w.Resolve(ctx, bad); err == nil {
			t.Errorf("Resolve(%q) = %q, want refusal", bad, p)
		}
	}
	cmd, err := w.FS.Command(ctx, real, `printf '%s|%s' "$(pwd -P)" "$GREETING"`, []string{"GREETING=it's me", "BAD NAME=x"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil || string(out) != real+"|it's me" {
		t.Errorf("command = %q %v", out, err)
	}
	dirs, err := fr.fs.ListDirs(ctx, real)
	sort.Strings(dirs)
	if err != nil || !reflect.DeepEqual(dirs, []string{".git", "escape", "moved", "new dir", "src"}) {
		t.Errorf("ListDirs = %q %v", dirs, err)
	}
	if n := strings.Count(fr.log(), "connect"); n != 1 {
		t.Errorf("connections = %d, want 1 (ControlMaster reuse)\n%s", n, fr.log())
	}
}

func TestSSHHomeDefault(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "proj", "x"), "x")
	fr := newFakeRemote(t, "HOME="+home)
	real, _ := filepath.EvalSymlinks(home)
	for in, want := range map[string]string{"": real, "~": real, "proj": filepath.Join(real, "proj"), "~/proj": filepath.Join(real, "proj")} {
		w, err := NewSSH(ctx, "devbox", in, fr.fs)
		if err != nil || w.Root != want {
			t.Errorf("NewSSH(%q) root = %v %v, want %s", in, w, err, want)
		}
	}
	if _, err := NewSSH(ctx, "devbox", "proj/x", fr.fs); err == nil {
		t.Error("file accepted as root")
	}
	if _, err := NewSSH(ctx, "devbox", "/does/not/exist", fr.fs); err == nil {
		t.Error("missing root accepted")
	}
}

func TestAskpassFlows(t *testing.T) {
	ctx := context.Background()
	hostKey := "The authenticity of host 'devbox (10.0.0.1)' can't be established.\nED25519 key fingerprint is SHA256:abc.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? "
	cases := []struct {
		name      string
		prompts   string
		expect    []string
		answers   map[string]string // prompt substring -> answer; missing = cancel
		wantOK    bool
		wantAsks  int
		confirmAt int // 1-based index of the host-key ask, 0 = none
	}{
		{"password", "deploy@devbox's password: ", []string{"hunter2"}, map[string]string{"password": "hunter2"}, true, 1, 0},
		{"password then otp", "deploy@devbox's password: |Verification code: ", []string{"pw", "123456"},
			map[string]string{"password": "pw", "Verification": "123456"}, true, 2, 0},
		{"host key yes then password", hostKey + "|deploy@devbox's password: ", []string{"yes", "pw"},
			map[string]string{"yes/no": "yes", "password": "pw"}, true, 2, 1},
		{"host key no", hostKey, []string{"yes"}, map[string]string{"yes/no": "no"}, false, 1, 1},
		{"cancel", "Enter passphrase for key '/home/u/.ssh/id_ed25519': ", []string{"pp"}, map[string]string{}, false, 1, 0},
		{"wrong password", "deploy@devbox's password: ", []string{"right"}, map[string]string{"password": "wrong"}, false, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := []string{"FAKE_PROMPTS=" + c.prompts}
			for i, e := range c.expect {
				env = append(env, "FAKE_A"+string(rune('1'+i))+"="+e)
			}
			fr := newFakeRemote(t, env...)
			fr.withBridge(t, func(r AskRequest) ([]byte, bool) {
				for k, v := range c.answers {
					if strings.Contains(r.Prompt, k) {
						return []byte(v), true
					}
				}
				return nil, false
			})
			err := fr.fs.Connect(ctx)
			if (err == nil) != c.wantOK {
				t.Fatalf("Connect err = %v, wantOK %v\nlog:\n%s", err, c.wantOK, fr.log())
			}
			asks := fr.askList()
			if len(asks) != c.wantAsks {
				t.Fatalf("asks = %d (%+v), want %d", len(asks), asks, c.wantAsks)
			}
			for i, a := range asks {
				if a.Host != "devbox" {
					t.Errorf("ask host = %q", a.Host)
				}
				if a.IsConfirm() != (i+1 == c.confirmAt) {
					t.Errorf("ask %d IsConfirm = %v (%q)", i+1, a.IsConfirm(), a.Prompt)
				}
			}
			if !strings.Contains(fr.log(), "require=force") {
				t.Errorf("SSH_ASKPASS_REQUIRE=force not set:\n%s", fr.log())
			}
			for _, e := range c.expect {
				if strings.Contains(fr.log(), e) && len(e) > 3 {
					t.Errorf("secret %q leaked into the ssh log", e)
				}
			}
			if c.wantOK {
				// The master persists: later operations never prompt.
				n := len(fr.askList())
				fr.fs.alive = fr.fs.alive.AddDate(-1, 0, 0)
				if _, err := fr.fs.Home(ctx); err != nil {
					t.Fatal(err)
				}
				if len(fr.askList()) != n {
					t.Error("prompted again despite a live ControlMaster")
				}
			}
		})
	}
}

func TestAskpassRejectsBadToken(t *testing.T) {
	exe, _ := os.Executable()
	called := false
	b, err := NewBridge(exe, func(context.Context, AskRequest) ([]byte, bool) { called = true; return []byte("x"), true })
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	st, _ := os.Stat(b.sock)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v", st.Mode().Perm())
	}
	dst, _ := os.Stat(b.dir)
	if dst.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", dst.Mode().Perm())
	}
	t.Setenv(envAskpassSocket, b.sock)
	t.Setenv(envAskpassToken, "forged")
	var out strings.Builder
	if code := AskpassMain([]string{"password:"}, &out); code == 0 || out.Len() != 0 || called {
		t.Errorf("forged token: code=%d out=%q called=%v", code, out.String(), called)
	}
	t.Setenv(envAskpassToken, b.token)
	if code := AskpassMain([]string{"password:"}, &out); code != 0 || out.String() != "x\n" {
		t.Errorf("valid token: code=%d out=%q", code, out.String())
	}
}
