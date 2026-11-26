package tui

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// musicDucker fades system output volume while the agent or user is speaking
// and restores it when both are quiet. Rule-based; no model call.
// macOS uses osascript. Termux uses termux-volume if present.
type musicDucker struct {
	mu       sync.Mutex
	original int // 0..100, -1 if unknown
	ducked   bool
}

var duck musicDucker

func (a *App) duckMusic(speaking, listening bool) {
	if speaking || listening {
		duck.apply(20)
		return
	}
	duck.restore()
}

func (d *musicDucker) apply(target int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ducked {
		return
	}
	cur := readVolume()
	if cur < 0 {
		return
	}
	d.original = cur
	setVolume(target)
	d.ducked = true
}

func (d *musicDucker) restore() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.ducked || d.original < 0 {
		return
	}
	setVolume(d.original)
	d.ducked = false
	d.original = -1
}

func readVolume() int {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript", "-e", "output volume of (get volume settings)").Output()
		if err != nil {
			return -1
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			return -1
		}
		return n
	default:
		if _, err := exec.LookPath("termux-volume"); err == nil {
			// termux-volume music prints current level; not always available.
			return -1
		}
		return -1
	}
}

func setVolume(v int) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("osascript", "-e", "set volume output volume "+strconv.Itoa(v)).Run()
	default:
		if _, err := exec.LookPath("termux-volume"); err == nil {
			_ = exec.Command("termux-volume", "music", strconv.Itoa(v)).Run()
		}
	}
}
