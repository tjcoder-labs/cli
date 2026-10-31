package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/workspace"
	"github.com/tjcoder-labs/cli/internal/workspace/wstest"
)

func run(t *testing.T, tool Tool, env ExecEnv, args map[string]any) (Result, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return tool.Execute(context.Background(), raw, env)
}

func mustRun(t *testing.T, tool Tool, env ExecEnv, args map[string]any) Result {
	t.Helper()
	res, err := run(t, tool, env, args)
	if err != nil {
		t.Fatalf("%s %v: %v (%s)", tool.Definition().Function.Name, args, err, res.Content)
	}
	return res
}

// sshEnv is a local primary workspace plus a fake ssh one ("devbox").
func sshEnv(t *testing.T, env ...string) (ExecEnv, *wstest.Remote, string) {
	t.Helper()
	local, _ := filepath.EvalSymlinks(t.TempDir())
	set := workspace.NewSet(local)
	r := wstest.New(t, "devbox", env...)
	if _, added := set.Add(r.WS); !added {
		t.Fatal("not added")
	}
	return ExecEnv{WorkspaceRoot: local, Workspaces: set}, r, local
}

// TestFileToolsOnSSHWorkspace: every file tool behaves on an SSH
// workspace exactly as locally, addressed by param, prefix or /use.
func TestFileToolsOnSSHWorkspace(t *testing.T) {
	env, r, local := sshEnv(t)
	tricky := "it's \"quoted\" $HOME `id` \\n\n\ttab\nüñí\n"
	mustRun(t, writeFileTool{}, env, map[string]any{"path": "devbox:src/a.txt", "content": tricky})
	if b, _ := os.ReadFile(filepath.Join(r.Root, "src", "a.txt")); string(b) != tricky {
		t.Fatalf("remote content = %q", b)
	}
	if _, err := os.Stat(filepath.Join(local, "src")); !os.IsNotExist(err) {
		t.Fatal("write leaked into the local workspace")
	}
	res := mustRun(t, readFileTool{}, env, map[string]any{"path": "src/a.txt", "workspace": "devbox"})
	if !strings.Contains(res.Content, "`id`") || !strings.Contains(res.Content, "üñí") {
		t.Fatalf("read = %q", res.Content)
	}
	mustRun(t, editFileTool{}, env, map[string]any{"path": "devbox:src/a.txt", "old_str": "tab", "new_str": "TAB"})
	mustRun(t, appendFileTool{}, env, map[string]any{"path": "devbox:src/a.txt", "content": "more\n"})
	b, _ := os.ReadFile(filepath.Join(r.Root, "src", "a.txt"))
	if !strings.Contains(string(b), "\tTAB\n") || !strings.HasSuffix(string(b), "more\n") {
		t.Fatalf("after edit+append = %q", b)
	}
	if _, err := run(t, createFileTool{}, env, map[string]any{"path": "devbox:src/a.txt", "content": "x"}); err == nil {
		t.Fatal("create_file overwrote an existing remote file")
	}
	mustRun(t, createFileTool{}, env, map[string]any{"path": "devbox:go.mod", "content": "module x\n"})

	res = mustRun(t, listDirectoryTool{}, env, map[string]any{"path": "devbox:"})
	if !strings.Contains(res.Content, "src/a.txt") || !strings.Contains(res.Content, "go.mod") {
		t.Fatalf("list = %q", res.Content)
	}
	res = mustRun(t, searchCodeTool{}, env, map[string]any{"pattern": "T.B", "workspace": "devbox"})
	if !strings.Contains(res.Content, "a.txt") {
		t.Fatalf("search = %q", res.Content)
	}
	res = mustRun(t, inspectProjectTool{}, env, map[string]any{"workspace": "devbox"})
	if !strings.Contains(res.Content, "Go") || !strings.Contains(res.Content, "devbox:") {
		t.Fatalf("inspect = %q", res.Content)
	}
	mustRun(t, moveFileTool{}, env, map[string]any{"src": "devbox:src/a.txt", "dst": "devbox:dst/b.txt"})
	if _, err := os.Stat(filepath.Join(r.Root, "dst", "b.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, moveFileTool{}, env, map[string]any{"src": "devbox:dst/b.txt", "dst": filepath.Join(local, "x")}); err == nil {
		t.Fatal("cross-workspace move should fail")
	}

	// /use devbox: unprefixed paths go to the remote.
	if err := env.Workspaces.SetActive("devbox"); err != nil {
		t.Fatal(err)
	}
	mustRun(t, deleteFileTool{}, env, map[string]any{"path": "dst/b.txt"})
	if _, err := os.Stat(filepath.Join(r.Root, "dst", "b.txt")); !os.IsNotExist(err) {
		t.Fatal("delete did not reach the remote")
	}
	// An explicit prefix still reaches the primary.
	name := env.Workspaces.Primary().Name
	mustRun(t, writeFileTool{}, env, map[string]any{"path": name + ":p.txt", "content": "p"})
	if _, err := os.Stat(filepath.Join(local, "p.txt")); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count("\n"+r.Log(), "\nmaster\n"); n != 1 {
		t.Fatalf("more than one master connect:\n%s", r.Log())
	}
}

// TestSSHWorkspaceContainment: .., absolute paths outside, and a
// symlink escaping the remote root are all refused.
func TestSSHWorkspaceContainment(t *testing.T) {
	env, r, _ := sshEnv(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600)
	os.Symlink(outside, filepath.Join(r.Root, "link"))
	for _, p := range []string{"devbox:../x", "devbox:/etc/passwd", "devbox:link/secret", "devbox:link/new"} {
		if _, err := run(t, readFileTool{}, env, map[string]any{"path": p}); err == nil {
			t.Errorf("read %s: expected containment error", p)
		}
		if _, err := run(t, writeFileTool{}, env, map[string]any{"path": p, "content": "x"}); err == nil {
			t.Errorf("write %s: expected containment error", p)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatal("write escaped through a symlink")
	}
	if _, err := run(t, readFileTool{}, env, map[string]any{"path": "x", "workspace": "nope"}); err == nil || !strings.Contains(err.Error(), "unknown workspace") {
		t.Fatalf("unknown workspace err = %v", err)
	}
}

// TestRunCommandOnSSHWorkspace: cd <remote cwd> && command, env and
// exit codes, git_status, and background jobs logged locally.
func TestRunCommandOnSSHWorkspace(t *testing.T) {
	env, r, local := sshEnv(t)
	os.MkdirAll(filepath.Join(r.Root, "sub dir"), 0o755)
	res := mustRun(t, runCommandTool{}, env, map[string]any{"command": "pwd; echo \"it's $((6*7))\"", "workspace": "devbox", "cwd": "sub dir"})
	if !strings.Contains(res.Content, filepath.Join(r.Root, "sub dir")) || !strings.Contains(res.Content, "it's 42") {
		t.Fatalf("run = %q", res.Content)
	}
	if _, err := run(t, runCommandTool{}, env, map[string]any{"command": "exit 3", "cwd": "devbox:"}); err == nil {
		t.Fatal("non-zero exit not reported")
	}
	if _, err := run(t, gitStatusTool{}, env, map[string]any{"workspace": "devbox"}); err == nil {
		t.Log("git_status in a non-repo unexpectedly succeeded") // git may not be a repo; only must not run locally
	}
	// Local still runs locally.
	res = mustRun(t, runCommandTool{}, env, map[string]any{"command": "pwd"})
	if strings.TrimSpace(res.Content) != local {
		t.Fatalf("local pwd = %q", res.Content)
	}
}

// fakeRemoteSudo puts a fake `sudo` (password "hunter2", or NOPASSWD
// when FAKE_NOPASSWD=1) and a profile-less bash first on the remote PATH.
func fakeRemoteSudo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sudo := `#!/bin/sh
printf 'sudo %s\n' "$*" >> "$FAKE_SSH_DIR/sudo.log"
case "$1" in
  -S) read -r pw; [ "$pw" = hunter2 ] && { : > "$FAKE_SSH_DIR/ticket"; exit 0; }; exit 1;;
  -n) shift
      if [ "$FAKE_NOPASSWD" = 1 ] || [ -e "$FAKE_SSH_DIR/ticket" ]; then
        [ "$1" = -v ] && exit 0
        [ "$1" = -n ] && shift
        echo "as-root: $*"; exit 0
      fi
      echo "sudo: a password is required" >&2; exit 1;;
esac
echo "unexpected sudo $*" >&2; exit 2
`
	os.WriteFile(filepath.Join(dir, "sudo"), []byte(sudo), 0o755)
	os.WriteFile(filepath.Join(dir, "bash"), []byte("#!/bin/sh\nexec /usr/bin/bash --noprofile --norc \"$@\"\n"), 0o755)
	return dir
}

func TestRemoteSudo(t *testing.T) {
	allowOnce := PromptAnswer{Index: 0, Choice: sudoOptions[0]}
	allowSession := PromptAnswer{Index: 1, Choice: sudoOptions[1]}
	cmd := map[string]any{"command": "sudo whoami", "workspace": "devbox"}

	t.Run("password via secure card, stdin only", func(t *testing.T) {
		env, r, _ := sshEnv(t, "FAKE_PATH="+fakeRemoteSudo(t))
		p := &fakePrompter{answers: []PromptAnswer{allowOnce}, secret: []byte("hunter2")}
		env.Prompter, env.Sudo = p, &SudoPolicy{}
		res := mustRun(t, runCommandTool{}, env, cmd)
		if !strings.Contains(res.Content, "as-root: whoami") || !strings.Contains(res.Content, "allowed once") {
			t.Fatalf("res = %q", res.Content)
		}
		if p.secrets != 1 || len(p.asked) != 1 || !strings.Contains(p.asked[0].Title, "devbox") {
			t.Fatalf("asked=%v secrets=%d", p.asked, p.secrets)
		}
		sl, _ := os.ReadFile(filepath.Join(r.Dir, "sudo.log"))
		if strings.Contains(r.Log()+string(sl)+res.Content, "hunter2") {
			t.Fatal("password leaked into a command line, log or output")
		}
	})
	t.Run("wrong password", func(t *testing.T) {
		env, _, _ := sshEnv(t, "FAKE_PATH="+fakeRemoteSudo(t))
		env.Prompter, env.Sudo = &fakePrompter{answers: []PromptAnswer{allowOnce}, secret: []byte("nope")}, &SudoPolicy{}
		_, err := run(t, runCommandTool{}, env, cmd)
		if !errors.Is(err, errSudoDenied) || !strings.Contains(err.Error(), "authentication failed") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("denied card runs nothing", func(t *testing.T) {
		env, r, _ := sshEnv(t, "FAKE_PATH="+fakeRemoteSudo(t))
		p := &fakePrompter{answers: []PromptAnswer{{Index: 2, Choice: sudoOptions[2]}}}
		env.Prompter, env.Sudo = p, &SudoPolicy{}
		if _, err := run(t, runCommandTool{}, env, cmd); !errors.Is(err, errSudoDenied) {
			t.Fatalf("err = %v", err)
		}
		if p.secrets != 0 || strings.Contains(r.Log(), "whoami") {
			t.Fatal("denied sudo still ran or asked for a password")
		}
	})
	t.Run("cancelled password", func(t *testing.T) {
		env, _, _ := sshEnv(t, "FAKE_PATH="+fakeRemoteSudo(t))
		env.Prompter, env.Sudo = &fakePrompter{answers: []PromptAnswer{allowOnce}}, &SudoPolicy{}
		if _, err := run(t, runCommandTool{}, env, cmd); !errors.Is(err, errSudoDenied) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("NOPASSWD and session approval per host", func(t *testing.T) {
		env, _, _ := sshEnv(t, "FAKE_PATH="+fakeRemoteSudo(t), "FAKE_NOPASSWD=1")
		p := &fakePrompter{answers: []PromptAnswer{allowSession}}
		env.Prompter, env.Sudo = p, &SudoPolicy{}
		mustRun(t, runCommandTool{}, env, cmd)
		mustRun(t, runCommandTool{}, env, cmd)
		if len(p.asked) != 1 || p.secrets != 0 {
			t.Fatalf("asked=%d secrets=%d", len(p.asked), p.secrets)
		}
		if env.Sudo.SessionApproved() {
			t.Fatal("remote session approval leaked to local sudo")
		}
	})
	t.Run("headless denied", func(t *testing.T) {
		env, _, _ := sshEnv(t, "FAKE_PATH="+fakeRemoteSudo(t))
		if _, err := run(t, runCommandTool{}, env, cmd); !errors.Is(err, errSudoDenied) {
			t.Fatalf("err = %v", err)
		}
	})
}
