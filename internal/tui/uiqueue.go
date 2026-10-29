package tui

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

// UI posting
//
// tview's Application.QueueUpdate / QueueUpdateDraw (v0.42) send the
// closure to the event loop and then BLOCK until the event loop has
// run it. Calling either from the event-loop goroutine itself (a key
// handler, an InputField done func, a form/modal callback, a slash
// command, or a closure already running inside QueueUpdate) therefore
// deadlocks the whole TUI: the loop is waiting on itself. That is how
// `/telemetry` froze the app (submit → handleSlashCommand →
// handleTelemetryCommand → Record → QueueUpdateDraw).
//
// Every UI mutation that may originate outside a known context goes
// through postUI / postUIWait instead of calling QueueUpdate directly:
//
//   - On the event-loop goroutine the closure runs inline, immediately,
//     so output keeps its natural order relative to the caller's other
//     direct writes, and the loop's own post-event draw renders it.
//   - On any other goroutine the closure is appended to a single FIFO
//     (never blocking on the UI) and one pump goroutine forwards
//     batches to QueueUpdateDraw. A single queue keeps every off-loop
//     post in submission order across all call sites (e.g. a runner
//     event, then an invoke_cli_command output, then the next event).
//
// postUIWait additionally waits (off-loop only) until the closure has
// run, preserving back-pressure for streaming callers. It never waits
// when called on the event loop, and returns if the event loop stops.
//
// Never hold a.mu while posting from a goroutine that waits.

type uiQueue struct {
	mu       sync.Mutex
	items    []func()
	wake     chan struct{}
	stopped  chan struct{} // closed when the event loop has exited
	start    sync.Once
	stop     sync.Once
	pumpOnce sync.Once
	loopGID  atomic.Int64 // goroutine id of the tview event loop; 0 = unknown
}

func (q *uiQueue) init() {
	q.start.Do(func() {
		q.wake = make(chan struct{}, 1)
		if q.stopped == nil {
			q.stopped = make(chan struct{})
		}
	})
}

// curGoroutineID parses the current goroutine's id from its stack
// header ("goroutine 123 [running]:"). Only used to recognise the
// event-loop goroutine; a parse failure yields 0 (= "not the loop"),
// which falls back to the always-safe asynchronous path.
func curGoroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	b := bytes.TrimPrefix(buf[:n], []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		if id, err := strconv.ParseInt(string(b[:i]), 10, 64); err == nil {
			return id
		}
	}
	return 0
}

// markUIGoroutine records the calling goroutine as the event loop.
func (a *App) markUIGoroutine() { a.uiq.loopGID.Store(curGoroutineID()) }

// onUIGoroutine reports whether the caller is the tview event loop.
func (a *App) onUIGoroutine() bool {
	id := a.uiq.loopGID.Load()
	return id != 0 && id == curGoroutineID()
}

// runUI runs the tview event loop on the calling goroutine and marks it
// as the UI goroutine for postUI.
func (a *App) runUI() error {
	a.uiq.init()
	a.markUIGoroutine()
	defer func() {
		a.uiq.loopGID.Store(0)
		a.uiq.stop.Do(func() { close(a.uiq.stopped) })
	}()
	return a.tv.Run()
}

// postUI schedules fn to run on the UI goroutine and returns without
// waiting. Safe from any goroutine, including the event loop itself.
func (a *App) postUI(fn func()) {
	if a == nil || a.tv == nil || fn == nil {
		return
	}
	if a.onUIGoroutine() {
		fn()
		return
	}
	a.enqueueUI(fn)
}

// postUIWait is postUI that, off the event loop, waits until fn has
// run (or the event loop has stopped). On the event loop it runs fn
// inline. Callers must not hold a.mu.
func (a *App) postUIWait(fn func()) {
	if a == nil || a.tv == nil || fn == nil {
		return
	}
	if a.onUIGoroutine() {
		fn()
		return
	}
	done := make(chan struct{})
	a.enqueueUI(func() {
		defer close(done)
		fn()
	})
	select {
	case <-done:
	case <-a.uiq.stopped:
	}
}

func (a *App) enqueueUI(fn func()) {
	q := &a.uiq
	q.init()
	q.mu.Lock()
	q.items = append(q.items, fn)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	q.startPump(a)
}

func (q *uiQueue) startPump(a *App) {
	q.pumpOnce.Do(func() { go q.pump(a) })
}

// pump forwards queued closures to the event loop, in order. Each
// wake drains everything queued so far into one QueueUpdateDraw so a
// burst of posts costs a single redraw.
func (q *uiQueue) pump(a *App) {
	for {
		select {
		case <-q.wake:
		case <-q.stopped:
			return
		}
		q.mu.Lock()
		batch := q.items
		q.items = nil
		q.mu.Unlock()
		if len(batch) == 0 {
			continue
		}
		a.tv.QueueUpdateDraw(func() {
			// Whatever goroutine runs this is the event loop; record it
			// so nested posts from inside these closures run inline.
			a.markUIGoroutine()
			for _, fn := range batch {
				fn()
			}
		})
	}
}
