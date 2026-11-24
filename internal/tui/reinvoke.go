package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// reinvoking guards against concurrent /reinvoke runs.
var reinvoking atomic.Bool

const reinvokePromptEnv = "CODER_REINVOKE_PROMPT"

const reinvokeContinuePrompt = `[Re-invocation] You successfully rebuilt, installed, and relaunched Coder CLI with /reinvoke. The session was restored. Acknowledge the successful restart briefly and continue the active work from the restored conversation. Do not run /reinvoke again unless the code changed or the user explicitly requests it.`

// reinvoke rebuilds, installs and re-executes the coder binary in
// place. It is the agent-facing self-update path: `make install`
// produces a fresh binary at ~/.local/bin/coder, then syscall.Exec
// replaces the running process so the session continues under the new
// code without losing the workspace or session file.
//
// Usage:
//
//	/reinvoke            build + install + relaunch
//	/reinvoke --no-exec  build + install only (report the new binary)
func (a *App) runReinvoke(parts []string) {
	noExec := false
	for _, p := range parts[1:] {
		switch p {
		case "--no-exec", "--no-relaunch", "-n":
			noExec = true
		}
	}

	exe, err := os.Executable()
	if err != nil {
		a.appendActivity(fmt.Sprintf("[%s::b]✗ reinvoke[-::B]: %v", a.palette.HexAlert, err))
		return
	}
	// Resolve the workspace root that owns the Makefile (the repo root).
	// The build must run from the source tree, not the installed binary.
	repoRoot := a.reinvokeRepoRoot(exe)
	if repoRoot == "" {
		a.appendActivity(fmt.Sprintf("[%s::b]✗ reinvoke[-::B]: cannot locate source tree (no Makefile found)", a.palette.HexAlert))
		return
	}

	if !reinvoking.CompareAndSwap(false, true) {
		a.appendActivity(fmt.Sprintf("[%s]reinvoke[-]: a build is already running", a.palette.HexFaint))
		return
	}
	defer reinvoking.Store(false)

	a.appendActivity(fmt.Sprintf("[%s::b]⟳ reinvoke[-::B] [%s]building in %s…[-]",
		a.palette.HexPurple, a.palette.HexDim, repoRoot))

	// Persist session before we replace the process.
	if err := a.saveSession(); err != nil {
		a.appendActivity(fmt.Sprintf("[%s::b]✗ reinvoke[-::B]: session save failed: %v", a.palette.HexAlert, err))
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		// `make install` builds bin/coder and installs to ~/.local/bin/coder
		// (and re-registers the browser native host).
		cmd := exec.CommandContext(ctx, "make", "install")
		cmd.Dir = repoRoot
		prefix := filepath.Dir(exe)
		// A development binary lives in <repo>/bin. Do not install over it
		// while it is still running; the canonical installed path is used
		// for the relaunch instead.
		if filepath.Base(prefix) == "bin" && filepath.Dir(prefix) == repoRoot {
			home, homeErr := os.UserHomeDir()
			if homeErr == nil {
				prefix = filepath.Join(home, ".local", "bin")
			}
		}
		cmd.Env = append(os.Environ(), "PREFIX="+prefix)
		out, err := cmd.CombinedOutput()
		nextExe := exe
		if prefix != filepath.Dir(exe) {
			nextExe = filepath.Join(prefix, "coder")
		}

		a.postUI(func() {
			if err != nil {
				a.appendActivity(fmt.Sprintf("[%s::b]✗ reinvoke build failed[-::B]: %v\n%s",
					a.palette.HexAlert, err, truncateLines(string(out), 6)))
				return
			}
			a.appendActivity(fmt.Sprintf("[%s::b]✓ reinvoke[-::B] [%s]built + installed %s[-]",
				a.palette.HexLavender, a.palette.HexDim, nextExe))

			if noExec {
				a.appendActivity(fmt.Sprintf("[%s]reinvoke[-]: --no-exec set; new binary is ready at %s. Restart coder to use it.",
					a.palette.HexFaint, nextExe))
				return
			}

			// Re-exec in place. syscall.Exec replaces the current process
			// image, so the session file (already saved) is reloaded by
			// the new binary on startup. Stdin/stdout/stderr are inherited.
			a.appendActivity(fmt.Sprintf("[%s::b]⟳ reinvoke[-::B] [%s]relaunching…[-]",
				a.palette.HexPurple, a.palette.HexDim))
			a.tv.Stop()
			// Small delay lets the terminal restore before exec.
			time.Sleep(100 * time.Millisecond)
			env := append(os.Environ(), reinvokePromptEnv+"="+reinvokeContinuePrompt)
			if err := syscall.Exec(nextExe, os.Args, env); err != nil {
				// Exec failed: fall back to spawning a child.
				fmt.Fprintf(os.Stderr, "reinvoke: exec failed (%v); starting new process\n", err)
				nc := exec.Command(nextExe, os.Args[1:]...)
				nc.Env = env
				nc.Stdin, nc.Stdout, nc.Stderr = os.Stdin, os.Stdout, os.Stderr
				if serr := nc.Start(); serr != nil {
					fmt.Fprintf(os.Stderr, "reinvoke: start failed: %v\n", serr)
					os.Exit(1)
				}
				os.Exit(0)
			}
		})
	}()
}

// submitAfterReinvoke consumes a one-shot handoff left by the previous
// process. It queues normal submission until the UI event loop is ready;
// existing busy/steering behavior keeps it single-flight.
func (a *App) submitAfterReinvoke() {
	prompt := strings.TrimSpace(os.Getenv(reinvokePromptEnv))
	if prompt == "" {
		return
	}
	_ = os.Unsetenv(reinvokePromptEnv)
	a.postUI(func() {
		a.input.SetText(prompt)
		a.submit()
	})
}

// reinvokeRepoRoot finds the source tree that can rebuild the binary.
// Strategy: walk up from the executable's directory looking for a
// Makefile that mentions "coder", then fall back to the workspace root.
func (a *App) reinvokeRepoRoot(exe string) string {
	candidates := []string{}
	if exe != "" {
		candidates = append(candidates, filepath.Dir(exe))
	}
	if a.workspaceRoot != "" {
		candidates = append(candidates, a.workspaceRoot)
	}
	for _, start := range candidates {
		dir := start
		for i := 0; i < 8; i++ {
			mk := filepath.Join(dir, "Makefile")
			if data, err := os.ReadFile(mk); err == nil {
				if strings.Contains(string(data), "coder") {
					return dir
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

// truncateLines returns the last n lines of s.
func truncateLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
