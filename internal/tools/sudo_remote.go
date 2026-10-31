package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/tjcoder-labs/cli/internal/workspace"
)

// Remote sudo (SSH workspaces) uses the same authorization card as
// local sudo, approved per host. The password, when the host needs
// one, is typed in the secure card and sent to the remote shell on
// stdin, never on a command line: the script reads it into a shell
// variable, primes `sudo -S -v` with the builtin printf, unsets it, and
// runs the command behind a `sudo -n` shim, all in one remote shell so
// the ticket (keyed to that shell when there is no tty) is the one the
// command uses. Nothing is cached locally.

// remoteSudoAuthFailed is the exit code the remote script uses when
// sudo rejects the password.
const remoteSudoAuthFailed = 97

func (p *SudoPolicy) hostApproved(host string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hosts[host]
}

func (p *SudoPolicy) approveHost(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hosts == nil {
		p.hosts = map[string]bool{}
	}
	p.hosts[host] = true
}

// remoteSudoGrant is the script and stdin for one authorized command.
type remoteSudoGrant struct {
	Script string
	Stdin  []byte // "password\n" or nil; zeroed by Close
	Note   string
}

func (g *remoteSudoGrant) Close() {
	if g != nil {
		zero(g.Stdin)
		g.Stdin = nil
	}
}

// remoteSudoShim puts a `sudo -n` wrapper first on PATH for the rest of
// the remote script and removes it on exit.
const remoteSudoShim = `__coder_real=$(command -v sudo) || { echo "sudo is not installed on this host" >&2; exit 1; }
__coder_shim=$(mktemp -d) || exit 1
trap 'rm -rf "$__coder_shim"' EXIT
printf '#!/bin/sh\nexec "%s" -n "$@"\n' "$__coder_real" > "$__coder_shim/sudo" && chmod 700 "$__coder_shim/sudo" || exit 1
PATH="$__coder_shim:$PATH"; export PATH
`

// remoteSudoPrime reads the password line from stdin and primes sudo.
var remoteSudoPrime = `IFS= read -r __coder_pw || exit 1
printf '%s\n' "$__coder_pw" | sudo -S -p '' -v 2>/dev/null
__coder_s=$?
unset __coder_pw
[ "$__coder_s" -eq 0 ] || { echo "sudo authentication failed" >&2; exit ` + fmt.Sprint(remoteSudoAuthFailed) + `; }
`

func authorizeRemoteSudo(ctx context.Context, command string, env ExecEnv, ws *workspace.Workspace, dir string) (*remoteSudoGrant, error) {
	for _, w := range sudoInvocations(command) {
		if w != "sudo" {
			return nil, fmt.Errorf("%w: invoke sudo by name (\"sudo …\"), not by path %q", errSudoDenied, w)
		}
	}
	policy := env.Sudo
	if env.Prompter == nil {
		if policy == nil || !policy.AllowNonInteractive {
			return nil, fmt.Errorf("%w: non-interactive session; re-run with --allow-sudo to permit non-interactive sudo (sudo -n)", errSudoDenied)
		}
		return &remoteSudoGrant{Script: remoteSudoShim + command, Note: "[sudo on " + ws.Host + ": allowed non-interactively via --allow-sudo (sudo -n)]"}, nil
	}
	if policy == nil {
		policy = &SudoPolicy{}
	}
	decision := "allowed (session approval)"
	if !policy.hostApproved(ws.Host) {
		ans, err := env.Prompter.Ask(ctx, PromptRequest{
			Kind:     PromptAuthorization,
			Title:    "sudo on " + ws.Host,
			Question: fmt.Sprintf("The agent wants to run a command with sudo on %s (ssh workspace %s).", ws.Host, ws.Name),
			Detail:   command,
			Options:  append([]string(nil), sudoOptions...),
		})
		if err != nil {
			return nil, err
		}
		switch {
		case !ans.Cancelled && ans.Index == 1 && ans.Choice == sudoOptions[1]:
			decision = "allowed for this session"
			policy.approveHost(ws.Host)
		case !ans.Cancelled && ans.Index == 0 && ans.Choice == sudoOptions[0]:
			decision = "allowed once"
		default:
			env.Prompter.Record("sudo denied on " + ws.Host + ": " + command)
			return nil, fmt.Errorf("%w: the user denied sudo for this command", errSudoDenied)
		}
	}
	g := &remoteSudoGrant{Note: "[sudo on " + ws.Host + ": " + decision + " by the user]"}
	if remoteSudoCached(ctx, ws, dir) {
		g.Script = remoteSudoShim + command
	} else {
		pw, err := env.Prompter.AskSecret(ctx, "sudo password", "[sudo] password on "+ws.Host)
		if err != nil || pw == nil {
			zero(pw)
			env.Prompter.Record("sudo cancelled at password prompt on " + ws.Host + ": " + command)
			return nil, fmt.Errorf("%w: the user cancelled the password prompt", errSudoDenied)
		}
		stdin := make([]byte, 0, len(pw)+1)
		stdin = append(stdin, pw...)
		stdin = append(stdin, '\n')
		zero(pw)
		g.Stdin = stdin
		g.Script = remoteSudoPrime + remoteSudoShim + command
	}
	env.Prompter.Record("sudo " + decision + " on " + ws.Host + ": " + command)
	return g, nil
}

// remoteSudoCached reports whether sudo runs without a password on the
// host (NOPASSWD); tickets do not outlive one ssh session without a tty.
func remoteSudoCached(ctx context.Context, ws *workspace.Workspace, dir string) bool {
	cmd, err := ws.FS.Command(ctx, dir, "sudo -n -v 2>/dev/null", nil)
	if err != nil {
		return false
	}
	return cmd.Run() == nil
}

// isRemoteSudoAuthFailure reports the prime step's exit code.
func isRemoteSudoAuthFailure(out string, code int) bool {
	return code == remoteSudoAuthFailed && strings.Contains(out, "sudo authentication failed")
}
