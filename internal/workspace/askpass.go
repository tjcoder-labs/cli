package workspace

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Askpass env names shared by the bridge and the helper.
const (
	envAskpassSocket = "CODER_ASKPASS_SOCKET"
	envAskpassToken  = "CODER_ASKPASS_TOKEN"
	envAskpassHost   = "CODER_ASKPASS_HOST"
	// AskpassSubcommand is the hidden `coder __askpass` mode.
	AskpassSubcommand = "__askpass"
)

// AskRequest is one ssh prompt relayed by the helper.
type AskRequest struct {
	Host   string
	Prompt string
}

// IsConfirm reports whether the prompt is ssh's yes/no question (an
// unknown or changed host key), which must be a question card, not a
// secret field.
func (r AskRequest) IsConfirm() bool {
	l := strings.ToLower(r.Prompt)
	return strings.Contains(l, "(yes/no") || strings.Contains(l, "yes/no/[fingerprint]")
}

// AskFunc answers one prompt. ok=false (cancelled, dismissed, ctx done)
// makes the helper exit non-zero so ssh fails authentication cleanly.
// The returned slice is zeroed by the bridge after it is sent.
type AskFunc func(ctx context.Context, req AskRequest) (answer []byte, ok bool)

// Bridge is the TUI end of SSH_ASKPASS: a Unix socket (0600, inside a
// private 0700 temp dir) that accepts prompts from `coder __askpass`
// helpers carrying the per-session random token, and answers them via
// AskFunc. Prompts are answered one at a time, in arrival order.
type Bridge struct {
	dir     string
	sock    string
	wrapper string
	token   string
	ln      net.Listener
	ask     AskFunc
	modern  bool // OpenSSH >= 8.4: SSH_ASKPASS_REQUIRE=force
	serial  sync.Mutex
	closed  chan struct{}
	timeout time.Duration
}

// NewBridge starts the socket listener. exe is the coder binary that
// implements the hidden askpass subcommand.
func NewBridge(exe string, ask AskFunc) (*Bridge, error) {
	dir, err := os.MkdirTemp("", "coder-askpass-")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	b := &Bridge{
		dir:     dir,
		sock:    filepath.Join(dir, "s"),
		wrapper: filepath.Join(dir, "askpass"),
		token:   hex.EncodeToString(tok),
		ask:     ask,
		closed:  make(chan struct{}),
		timeout: 10 * time.Minute,
		modern:  OpenSSHSupportsRequire(),
	}
	// ssh runs SSH_ASKPASS as `<path> <prompt>`; the wrapper adds the
	// hidden subcommand so the coder binary knows which mode it is in.
	script := "#!/bin/sh\nexec " + Quote(exe) + " " + AskpassSubcommand + " \"$@\"\n"
	if err := os.WriteFile(b.wrapper, []byte(script), 0o700); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	ln, err := net.Listen("unix", b.sock)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if err := os.Chmod(b.sock, 0o600); err != nil {
		ln.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	b.ln = ln
	go b.serve()
	return b, nil
}

// Close stops the listener and removes the temp dir.
func (b *Bridge) Close() {
	select {
	case <-b.closed:
		return
	default:
	}
	close(b.closed)
	b.ln.Close()
	os.RemoveAll(b.dir)
}

// Env is the environment for ssh processes of host.
func (b *Bridge) Env(host string) []string {
	env := []string{
		"SSH_ASKPASS=" + b.wrapper,
		envAskpassSocket + "=" + b.sock,
		envAskpassToken + "=" + b.token,
		envAskpassHost + "=" + host,
	}
	if b.modern {
		env = append(env, "SSH_ASKPASS_REQUIRE=force")
	}
	// Older OpenSSH only uses askpass with DISPLAY set and no tty; the
	// process is started with setsid (no controlling terminal).
	if os.Getenv("DISPLAY") == "" {
		env = append(env, "DISPLAY=coder-askpass:0")
	}
	return env
}

func (b *Bridge) serve() {
	for {
		c, err := b.ln.Accept()
		if err != nil {
			return
		}
		go b.handle(c)
	}
}

type askWire struct {
	Token  string `json:"token"`
	Host   string `json:"host"`
	Prompt string `json:"prompt"`
}

func (b *Bridge) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReaderSize(c, 64<<10).ReadBytes('\n')
	if err != nil {
		return
	}
	var req askWire
	if json.Unmarshal(line, &req) != nil {
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(b.token)) != 1 {
		io.WriteString(c, "NO\n")
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	// The helper half-closes nothing; if ssh dies (or the user's turn
	// is cancelled and ssh is killed) the read returns and the card is
	// withdrawn.
	go func() {
		one := make([]byte, 1)
		c.Read(one)
		cancel()
	}()
	go func() {
		select {
		case <-b.closed:
			cancel()
		case <-ctx.Done():
		}
	}()
	b.serial.Lock()
	ans, ok := b.ask(ctx, AskRequest{Host: req.Host, Prompt: req.Prompt})
	b.serial.Unlock()
	defer wipe(ans)
	if !ok || ctx.Err() != nil {
		io.WriteString(c, "NO\n")
		return
	}
	msg := make([]byte, 0, len(ans)+3)
	msg = append(msg, "OK\n"...)
	msg = append(msg, ans...)
	c.Write(msg)
	wipe(msg)
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// AskpassMain is `coder __askpass <prompt>`: relay the prompt to the
// TUI and print the answer for ssh. Exit 1 when cancelled.
func AskpassMain(args []string, stdout io.Writer) int {
	sock, tok := os.Getenv(envAskpassSocket), os.Getenv(envAskpassToken)
	if sock == "" || tok == "" {
		fmt.Fprintln(os.Stderr, "coder askpass: no session socket")
		return 1
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "coder askpass: session unavailable")
		return 1
	}
	defer c.Close()
	req, _ := json.Marshal(askWire{Token: tok, Host: os.Getenv(envAskpassHost), Prompt: strings.Join(args, " ")})
	if _, err := c.Write(append(req, '\n')); err != nil {
		return 1
	}
	resp, err := io.ReadAll(c)
	defer wipe(resp)
	if err != nil || len(resp) < 3 || string(resp[:3]) != "OK\n" {
		return 1
	}
	out := append(resp[3:], '\n')
	_, err = stdout.Write(out)
	wipe(out)
	if err != nil {
		return 1
	}
	return 0
}

var sshVersionRe = regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`)

var (
	versionOnce sync.Once
	versionOK   bool
)

// OpenSSHSupportsRequire reports whether the local ssh is OpenSSH 8.4+
// (SSH_ASKPASS_REQUIRE). Unknown versions use the DISPLAY/setsid path.
func OpenSSHSupportsRequire() bool {
	versionOnce.Do(func() {
		out, err := exec.Command("ssh", "-V").CombinedOutput()
		if err != nil && len(out) == 0 {
			return
		}
		versionOK = sshVersionAtLeast(string(out), 8, 4)
	})
	return versionOK
}

func sshVersionAtLeast(v string, major, minor int) bool {
	m := sshVersionRe.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a > major || (a == major && b >= minor)
}

// ErrCancelled is reported when the user declines an ssh prompt.
var ErrCancelled = errors.New("ssh authentication cancelled")
