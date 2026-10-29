package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Sudo gate
//
// run_command never lets a model-authored command reach sudo on its own.
// When a command invokes sudo the gate:
//
//  1. asks the user (inline transcript prompt) to authorize the exact
//     command: allow once / allow sudo for this session / deny (Esc);
//  2. if sudo needs a password, collects it with a masked prompt and
//     feeds it to `sudo -S -v` on stdin from this process, priming
//     sudo's own timestamp cache. The password never reaches the model,
//     the transcript, session history, logs, or disk, and the buffer
//     is zeroed straight after the write;
//  3. runs the command with a `sudo` shim first on PATH that forces
//     `sudo -n`, so sudo can never fall back to prompting on the TUI's
//     terminal.
//
// "Allow for this session" skips step 1 for later commands and relies
// on sudo's timestamp cache (re-asking for the password only when the
// ticket has expired). "Allow once" drops the ticket again (`sudo -k`)
// when the gate primed it. While a gate-primed ticket may be live,
// non-sudo commands run detached from the terminal session so an
// obfuscated sudo cannot ride the cached ticket.
//
// Headless (no Prompter) denies sudo unless --allow-sudo was passed, and
// even then never prompts: the command runs with the `sudo -n` shim.

// SudoPolicy is shared by every run_command call in a process.
type SudoPolicy struct {
	// AllowNonInteractive permits sudo in headless runs (--allow-sudo),
	// always non-interactively (sudo -n).
	AllowNonInteractive bool

	mu              sync.Mutex
	sessionApproved bool
	primed          bool
}

// SessionApproved reports whether the user chose "allow for this session".
func (p *SudoPolicy) SessionApproved() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionApproved
}

// TicketMayBeLive reports whether the gate primed a sudo ticket that may
// still be valid; callers then detach non-sudo commands from the tty.
func (p *SudoPolicy) TicketMayBeLive() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.primed
}

func (p *SudoPolicy) set(session, primed *bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if session != nil {
		p.sessionApproved = *session
	}
	if primed != nil {
		p.primed = *primed
	}
}

// sudoBackend is the part of sudo the gate shells out to; swapped in tests.
type sudoBackend interface {
	// Path resolves the real sudo binary.
	Path() (string, error)
	// Cached reports whether sudo can run without a password right now
	// (`sudo -n -v`: NOPASSWD or a live timestamp).
	Cached(ctx context.Context) bool
	// Validate primes the timestamp with password via `sudo -S -v`.
	Validate(ctx context.Context, password []byte) error
	// Invalidate drops the timestamp (`sudo -k`).
	Invalidate(ctx context.Context)
}

var sudoOps sudoBackend = execSudo{}

type execSudo struct{}

func (execSudo) Path() (string, error) { return exec.LookPath("sudo") }

func (s execSudo) Cached(ctx context.Context) bool {
	p, err := s.Path()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, p, "-n", "-v").Run() == nil
}

func (s execSudo) Validate(ctx context.Context, password []byte) error {
	p, err := s.Path()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// -S reads the password from stdin, -p '' suppresses the prompt
	// text. Output is discarded: sudo only ever prints errors here.
	cmd := exec.CommandContext(ctx, p, "-S", "-p", "", "-v")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	buf := make([]byte, len(password)+1)
	copy(buf, password)
	buf[len(password)] = '\n'
	_, werr := stdin.Write(buf)
	zero(buf)
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		return errors.New("sudo rejected the password")
	}
	return werr
}

func (s execSudo) Invalidate(ctx context.Context) {
	if p, err := s.Path(); err == nil {
		_ = exec.CommandContext(ctx, p, "-k").Run()
	}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// commandWrappers may precede the real command word in a segment.
var commandWrappers = map[string]bool{
	"env": true, "nohup": true, "time": true, "command": true, "exec": true,
	"builtin": true, "nice": true, "xargs": true, "then": true, "do": true,
	"else": true, "!": true, "{": true, "stdbuf": true, "timeout": true,
}

// sudoInvocations returns every sudo command word found in command
// position (start of a pipeline / list segment, after env assignments
// and common wrappers, or at the start of a quoted `sh -c` string).
// Each entry is the word as written ("sudo", "/usr/bin/sudo").
func sudoInvocations(command string) []string {
	var found []string
	// Normalise list/pipe/subshell separators to newlines.
	r := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "&", "\n",
		"$(", "\n", "`", "\n", "(", "\n", ")", "\n", "\"", "\n", "'", "\n")
	for _, seg := range strings.Split(r.Replace(command), "\n") {
		fields := strings.Fields(seg)
		for _, f := range fields {
			if strings.Contains(f, "=") && !strings.HasPrefix(f, "=") && !strings.Contains(f, "/") {
				continue // VAR=value
			}
			if commandWrappers[f] || strings.HasPrefix(f, "-") || isDigits(f) {
				continue // wrapper, its flags (nice -n 5, timeout 10)
			}
			if filepath.Base(f) == "sudo" {
				found = append(found, f)
			}
			break
		}
	}
	return found
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && c != 's' && c != 'm' && c != '.' {
			return false
		}
	}
	return true
}

// IsSudoCommand reports whether command invokes sudo.
func IsSudoCommand(command string) bool { return len(sudoInvocations(command)) > 0 }

// sudoGrant is an authorized sudo run: the extra env for the command
// and a cleanup to call once it finishes.
type sudoGrant struct {
	Env     []string
	Note    string
	cleanup func()
}

func (g *sudoGrant) Close() {
	if g != nil && g.cleanup != nil {
		g.cleanup()
	}
}

// errSudoDenied wraps every refusal so callers can tell it apart.
var errSudoDenied = errors.New("sudo denied")

// authorizeSudo runs the gate for command. On success the returned
// grant's Env must be applied to the command and Close called after it
// exits. On denial the error explains why (and is safe to show the
// model: it never contains secrets).
func authorizeSudo(ctx context.Context, command string, env ExecEnv) (*sudoGrant, error) {
	for _, w := range sudoInvocations(command) {
		if w != "sudo" {
			return nil, fmt.Errorf("%w: invoke sudo by name (\"sudo …\"), not by path %q", errSudoDenied, w)
		}
	}
	policy := env.Sudo
	real, err := sudoOps.Path()
	if err != nil {
		return nil, fmt.Errorf("%w: sudo is not installed", errSudoDenied)
	}

	if env.Prompter == nil {
		if policy == nil || !policy.AllowNonInteractive {
			return nil, fmt.Errorf("%w: non-interactive session; re-run with --allow-sudo to permit non-interactive sudo (sudo -n)", errSudoDenied)
		}
		g, err := newSudoShim(real)
		if err != nil {
			return nil, err
		}
		g.Note = "[sudo: allowed non-interactively via --allow-sudo (sudo -n)]"
		return g, nil
	}
	if policy == nil {
		policy = &SudoPolicy{}
	}

	once := false
	decision := "allowed (session approval)"
	if !policy.SessionApproved() {
		ans, err := env.Prompter.Ask(ctx, PromptRequest{
			Kind:     PromptAuthorization,
			Title:    "sudo",
			Question: "The agent wants to run a command with sudo.",
			Detail:   command,
			Options:  []string{"Allow once", "Allow sudo for this session", "Deny"},
		})
		if err != nil {
			return nil, err
		}
		switch {
		case ans.Cancelled || ans.Index == 2:
			env.Prompter.Record("sudo denied: " + command)
			return nil, fmt.Errorf("%w: the user denied sudo for this command", errSudoDenied)
		case ans.Index == 1:
			decision = "allowed for this session"
			t := true
			policy.set(&t, nil)
		default:
			once = true
			decision = "allowed once"
		}
	}

	primedNow := false
	if !sudoOps.Cached(ctx) {
		who := "you"
		if u, err := user.Current(); err == nil {
			who = u.Username
		}
		pw, err := env.Prompter.AskSecret(ctx, "sudo password", "[sudo] password for "+who)
		if err != nil || pw == nil {
			zero(pw)
			env.Prompter.Record("sudo cancelled at password prompt: " + command)
			return nil, fmt.Errorf("%w: the user cancelled the password prompt", errSudoDenied)
		}
		verr := sudoOps.Validate(ctx, pw)
		zero(pw)
		if verr != nil {
			env.Prompter.Record("sudo authentication failed: " + command)
			return nil, fmt.Errorf("%w: sudo authentication failed", errSudoDenied)
		}
		primedNow = true
		t := true
		policy.set(nil, &t)
	}

	g, err := newSudoShim(real)
	if err != nil {
		return nil, err
	}
	env.Prompter.Record("sudo " + decision + ": " + command)
	g.Note = "[sudo: " + decision + " by the user]"
	inner := g.cleanup
	g.cleanup = func() {
		inner()
		if once && primedNow {
			sudoOps.Invalidate(context.Background())
			f := false
			policy.set(nil, &f)
		}
	}
	return g, nil
}

// newSudoShim writes a tiny `sudo` wrapper that forces -n (never
// prompt) and returns the PATH override that puts it first. The shim
// holds no secrets.
func newSudoShim(realSudo string) (*sudoGrant, error) {
	dir, err := os.MkdirTemp("", "coder-sudo-")
	if err != nil {
		return nil, fmt.Errorf("sudo shim: %w", err)
	}
	script := "#!/bin/sh\nexec " + shellQuote(realSudo) + " -n \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("sudo shim: %w", err)
	}
	return &sudoGrant{
		Env:     []string{"CODER_SUDO_SHIM=" + dir},
		cleanup: func() { _ = os.RemoveAll(dir) },
	}, nil
}

// sudoPrelude re-prepends the shim directory after login-shell profile
// scripts (which may reset PATH) have run.
const sudoPrelude = `PATH="$CODER_SUDO_SHIM:$PATH"; export PATH; unset CODER_SUDO_SHIM; `

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
