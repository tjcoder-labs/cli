package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/tjcoder-labs/cli/internal/workspace"
)

// runRemoteCommand runs a run_command call in an SSH workspace: the
// same timeout, output and background-job handling as local, executed
// as `cd <dir> && <command>` on the host over the shared ControlMaster.
// PTY mode is not available remotely (ssh -T); sudo goes through
// authorizeRemoteSudo.
func runRemoteCommand(ctx context.Context, ws *workspace.Workspace, dir, command string, timeoutSeconds int, background bool, env ExecEnv) (Result, error) {
	if background {
		if IsSudoCommand(command) {
			return Result{}, fmt.Errorf("%w: sudo is not supported for background commands; run it in the foreground", errSudoDenied)
		}
		return startBackgroundJob(env, ws.Name+": "+command, ws.Host+":"+dir, timeoutSeconds, func(runCtx context.Context) (*exec.Cmd, error) {
			return ws.FS.Command(runCtx, dir, command, nil)
		})
	}
	script := command
	note := ""
	var stdin []byte
	if IsSudoCommand(command) {
		g, err := authorizeRemoteSudo(ctx, command, env, ws, dir)
		if err != nil {
			return Result{Content: err.Error(), Preview: "sudo denied"}, err
		}
		defer g.Close()
		script, note, stdin = g.Script, g.Note, g.Stdin
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	cmd, err := ws.FS.Command(runCtx, dir, script, nil)
	if err != nil {
		return Result{}, err
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	text := withSudoNote(note, truncateOutput(string(out)))
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && stdin != nil && isRemoteSudoAuthFailure(string(out), ee.ExitCode()) {
			if env.Prompter != nil {
				env.Prompter.Record("sudo authentication failed on " + ws.Host + ": " + command)
			}
			return Result{Content: text, Preview: "sudo denied"}, fmt.Errorf("%w: sudo authentication failed on %s", errSudoDenied, ws.Host)
		}
		if errors.As(err, &ee) && ee.ExitCode() == 255 {
			return Result{Content: text, Preview: preview(text)}, fmt.Errorf("ssh %s failed (exit 255): %w", ws.Host, err)
		}
		return Result{Content: text, Preview: preview(text)}, fmt.Errorf("command failed: %w", err)
	}
	return Result{Content: text, Preview: preview(text)}, nil
}
