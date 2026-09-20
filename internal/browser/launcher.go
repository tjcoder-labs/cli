package browser

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ChromeLaunch captures the knob-set for spawning a managed Chrome instance.
type ChromeLaunch struct {
	Port        int    // CDP debug port (required, e.g. 9222)
	UserDataDir string // profile root; default $HOME/.chrome-debug-<port>
	ChromeBin   string // path to chrome/chromium; auto-detected if empty
	Headless    bool   // pass --headless=new
	ExtraArgs   []string
}

// DefaultChromeDataDir returns the conventional per-port profile root.
func DefaultChromeDataDir(port int) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	if port == 9222 {
		// Preserve the historical primary profile location.
		return filepath.Join(home, ".chrome-debug")
	}
	return filepath.Join(home, fmt.Sprintf(".chrome-debug-%d", port))
}

// FindChrome returns the chrome binary path by probing common names.
func FindChrome() (string, error) {
	candidates := []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
		"chrome",
	}
	for _, name := range candidates {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	// macOS fallback
	macPaths := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	for _, p := range macPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("chrome/chromium not found in PATH (tried %v)", candidates)
}

// IsPortListening reports whether anything accepts TCP connections on the
// given loopback port. Used to preflight --remote-debugging-port collisions.
func IsPortListening(port int) bool {
	if port <= 0 {
		return false
	}
	d := &net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := d.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// StartChrome launches Chrome with the requested CDP port and profile.
// Returns the PID. Does not wait for Chrome to be ready — callers should
// poll IsPortListening or CDP /json/version.
func StartChrome(ctx context.Context, opts ChromeLaunch) (int, string, error) {
	if opts.Port <= 0 || opts.Port >= 65536 {
		return 0, "", fmt.Errorf("invalid port %d", opts.Port)
	}
	bin := opts.ChromeBin
	if bin == "" {
		var err error
		bin, err = FindChrome()
		if err != nil {
			return 0, "", err
		}
	}
	dataDir := opts.UserDataDir
	if dataDir == "" {
		dataDir = DefaultChromeDataDir(opts.Port)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return 0, "", fmt.Errorf("mkdir profile %s: %w", dataDir, err)
	}
	if IsPortListening(opts.Port) {
		return 0, dataDir, fmt.Errorf("port %d already in use — is Chrome already running with this profile?", opts.Port)
	}
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", opts.Port),
		fmt.Sprintf("--user-data-dir=%s", dataDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-features=TranslateUI",
		"--disable-session-crashed-bubble",
		"--hide-crash-restore-bubble",
	}
	if opts.Headless {
		args = append(args, "--headless=new", "--disable-gpu")
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.CommandContext(ctx, bin, args...)
	// Detach so the agent process can exit without killing Chrome.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return 0, dataDir, fmt.Errorf("start chrome: %w", err)
	}
	pid := cmd.Process.Pid
	// Reap the child; Chrome is now daemonized via setsid.
	go func() { _ = cmd.Wait() }()
	return pid, dataDir, nil
}

// WaitForChrome polls the CDP endpoint until it responds or the timeout elapses.
func WaitForChrome(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if IsPortListening(port) {
			// Port is up; try a Connect to be sure CDP is happy.
			c, err := Connect(ctx, port)
			if err == nil {
				_ = c.Close()
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return fmt.Errorf("chrome on port %d not ready after %s", port, timeout)
}

// StopChrome attempts a graceful shutdown of any Chrome it can reach on the
// given CDP port: Browser.close over the browser-level connection. Does not
// SIGKILL — if Chrome refuses, the caller can still do that themselves.
func StopChrome(ctx context.Context, port int) error {
	c, err := Connect(ctx, port)
	if err != nil {
		return fmt.Errorf("connect to chrome on %d: %w", port, err)
	}
	defer c.Close()
	_, err = c.Call(ctx, "Browser.close", nil)
	if err != nil && !strings.Contains(err.Error(), "websocket: close") {
		// Browser.close drops the connection before responding on
		// success; treat a mid-call close as success.
		return err
	}
	return nil
}
