package convsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
)

// EnvOptOut disables transcript sync when set to 0/false/off/no.
const EnvOptOut = "CODER_TRANSCRIPT_SYNC"

// CursorFileName lives next to session.json in <workspace>/.ergo-cli-go/.
const CursorFileName = "conversation-sync.json"

const (
	maxBatchItems = 200
	maxBatchBytes = 3 << 20
	minBackoff    = 2 * time.Second
	maxBackoff    = 5 * time.Minute
)

// EnvDisabled reports whether the environment opts out of sync.
func EnvDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvOptOut))) {
	case "0", "false", "off", "no", "disabled":
		return true
	}
	return false
}

// Snapshot is the state handed to the syncer after a turn or session save.
type Snapshot struct {
	SessionID string
	// CursorPath is where this session's sync cursor lives (see CursorPathFor).
	CursorPath string
	History    []client.Message
	Meta       Meta
}

// Options configures a Syncer. Token and BaseURL are read on every attempt
// so /login and /logout take effect without restarting.
type Options struct {
	BaseURL func() string
	Token   func() string
	Enabled func() bool
	HTTP    *http.Client
	Now     func() time.Time
	// Logf receives one-line, secret-free status notes (optional).
	Logf func(format string, args ...any)
}

// Syncer is a single background worker that coalesces snapshots and sends
// the unsynced tail of each session. Safe for concurrent use.
type Syncer struct {
	opts Options

	mu       sync.Mutex
	pending  *Snapshot
	started  bool
	closed   bool
	wake     chan struct{}
	done     chan struct{}
	backoff  time.Duration
	authFail string        // hash of the token that last got 401/403
	missing  bool          // server lacks the endpoint (404): stop for this run
	sendSem  chan struct{} // one sync pass at a time; acquisition honours ctx
}

// New builds a Syncer; the worker starts on the first Notify.
func New(opts Options) *Syncer {
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Enabled == nil {
		opts.Enabled = func() bool { return true }
	}
	return &Syncer{opts: opts, wake: make(chan struct{}, 1), done: make(chan struct{}), sendSem: make(chan struct{}, 1)}
}

func (s *Syncer) logf(format string, args ...any) {
	if s.opts.Logf != nil {
		s.opts.Logf(format, args...)
	}
}

// Notify queues the latest snapshot without blocking. Older unsent
// snapshots of the same workspace are superseded: the newest History
// contains everything they did.
func (s *Syncer) Notify(snap Snapshot) {
	if s == nil || snap.SessionID == "" || snap.CursorPath == "" {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	cp := snap
	cp.History = append([]client.Message(nil), snap.History...)
	if s.pending != nil && s.pending.SessionID != cp.SessionID && s.pending.CursorPath == cp.CursorPath {
		// Session switched (/clear): finish the old one first, best-effort.
		old := *s.pending
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = s.syncOnce(ctx, old)
		}()
	}
	s.pending = &cp
	if !s.started {
		s.started = true
		go s.loop()
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Syncer) loop() {
	defer close(s.done)
	var retry <-chan time.Time
	for {
		select {
		case <-s.wake:
			if retry != nil {
				continue // honour the backoff; the pending snapshot is kept
			}
		case <-retry:
			retry = nil
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		snap := s.pending
		s.mu.Unlock()
		if snap == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		err := s.syncOnce(ctx, *snap)
		cancel()
		s.mu.Lock()
		if err == nil || !isTransient(err) {
			if s.pending == snap {
				s.pending = nil
			}
			s.backoff = 0
			again := s.pending != nil
			s.mu.Unlock()
			if again {
				select {
				case s.wake <- struct{}{}:
				default:
				}
			}
			continue
		}
		if s.backoff == 0 {
			s.backoff = minBackoff
		} else if s.backoff *= 2; s.backoff > maxBackoff {
			s.backoff = maxBackoff
		}
		d := s.backoff
		s.mu.Unlock()
		s.logf("transcript sync offline, retrying in %s", d.Round(time.Second))
		retry = time.After(d)
	}
}

// Flush synchronously sends the pending snapshot (used on exit and by the
// headless path). It gives up when ctx expires.
func (s *Syncer) Flush(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	snap := s.pending
	s.mu.Unlock()
	if snap == nil {
		return nil
	}
	err := s.syncOnce(ctx, *snap)
	if err == nil {
		s.mu.Lock()
		if s.pending == snap {
			s.pending = nil
		}
		s.mu.Unlock()
	}
	return err
}

// Close flushes (bounded by timeout) and stops the worker.
func (s *Syncer) Close(timeout time.Duration) {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	_ = s.Flush(ctx)
	cancel()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	started := s.started
	s.mu.Unlock()
	if started {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// ---- errors ----

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("transcript sync: HTTP %d %s", e.status, e.msg)
}

var errNotAuthenticated = errors.New("transcript sync: not signed in")

func isTransient(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.status == 429 || he.status >= 500
	}
	return !errors.Is(err, errNotAuthenticated) && !errors.Is(err, errDisabled)
}

var errDisabled = errors.New("transcript sync: disabled")

func tokenHash(t string) string {
	h := sha256.Sum256([]byte(t))
	return fmt.Sprintf("%x", h[:8])
}

// ---- one sync pass ----

func (s *Syncer) syncOnce(ctx context.Context, snap Snapshot) error {
	select {
	case s.sendSem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.sendSem }()
	if EnvDisabled() || !s.opts.Enabled() {
		return errDisabled
	}
	s.mu.Lock()
	missing := s.missing
	s.mu.Unlock()
	if missing {
		return errDisabled
	}
	token := ""
	if s.opts.Token != nil {
		token = strings.TrimSpace(s.opts.Token())
	}
	if token == "" {
		return errNotAuthenticated
	}
	th := tokenHash(token)
	s.mu.Lock()
	if s.authFail == th {
		s.mu.Unlock()
		return errNotAuthenticated
	}
	s.mu.Unlock()
	base := strings.TrimRight(s.opts.BaseURL(), "/")

	cur, ok := LoadCursor(snap.CursorPath)
	if !ok || cur.SessionID != snap.SessionID {
		cur = Cursor{SessionID: snap.SessionID}
		// Local cursor missing: ask the server where this session left off.
		if rc, found, err := s.remoteCursor(ctx, base, token, snap.SessionID); err != nil {
			return s.classify(err, th)
		} else if found {
			cur = rc
		}
	}
	items, next := Plan(snap.History, cur, snap.Meta, s.opts.Now())
	if len(items) == 0 && cur.ConversationID != "" {
		if next.SyncedLen != cur.SyncedLen || next.LastFingerprint != cur.LastFingerprint {
			_ = SaveCursor(snap.CursorPath, next)
		}
		return nil
	}
	for _, batch := range batches(items) {
		convID, err := s.post(ctx, base, token, snap.SessionID, snap.Meta, batch)
		if err != nil {
			var he *httpError
			if errors.As(err, &he) && he.status == 400 {
				// Poison batch: skip it rather than retrying forever.
				s.logf("transcript sync skipped %d item(s): %s", len(batch), he.msg)
			} else {
				return s.classify(err, th)
			}
		}
		if convID != "" {
			cur.ConversationID = convID
		}
		last := batch[len(batch)-1]
		cur.NextSeq = last.Seq + 1
	}
	next.ConversationID = cur.ConversationID
	next.UpdatedAt = s.opts.Now().UTC().Format(time.RFC3339)
	return SaveCursor(snap.CursorPath, next)
}

func (s *Syncer) classify(err error, th string) error {
	var he *httpError
	if errors.As(err, &he) {
		switch {
		case he.status == 401 || he.status == 403:
			s.mu.Lock()
			s.authFail = th
			s.mu.Unlock()
			return errNotAuthenticated
		case he.status == 404:
			s.mu.Lock()
			s.missing = true
			s.mu.Unlock()
			return errDisabled
		}
	}
	return err
}

func batches(items []Item) [][]Item {
	var out [][]Item
	var cur []Item
	size := 0
	for _, it := range items {
		b, _ := json.Marshal(it)
		if len(cur) > 0 && (len(cur) >= maxBatchItems || size+len(b) > maxBatchBytes) {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, it)
		size += len(b)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func (s *Syncer) do(ctx context.Context, method, u, token string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Ergo-Client", "tjcoder-cli")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.opts.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Error.Code
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, &httpError{status: resp.StatusCode, msg: msg}
	}
	return data, nil
}

func (s *Syncer) post(ctx context.Context, base, token, sessionID string, meta Meta, items []Item) (string, error) {
	u := base + "/cli/conversations/" + url.PathEscape(sessionID) + "/messages"
	data, err := s.do(ctx, http.MethodPost, u, token, map[string]any{"meta": meta, "messages": items})
	if err != nil {
		return "", err
	}
	var r struct {
		ConversationID string `json:"conversation_id"`
	}
	_ = json.Unmarshal(data, &r)
	return r.ConversationID, nil
}

func (s *Syncer) remoteCursor(ctx context.Context, base, token, sessionID string) (Cursor, bool, error) {
	data, err := s.do(ctx, http.MethodGet, base+"/cli/conversations/"+url.PathEscape(sessionID), token, nil)
	if err != nil {
		var he *httpError
		if errors.As(err, &he) && he.status == 404 {
			// 404 here means "no conversation yet" only when the server
			// answered with our JSON error; an old server 404s the route.
			if he.msg == "not_found" {
				return Cursor{}, false, nil
			}
		}
		return Cursor{}, false, err
	}
	var r struct {
		ConversationID  string  `json:"conversation_id"`
		LastSeq         *int    `json:"last_seq"`
		LastFingerprint *string `json:"last_fingerprint"`
	}
	if json.Unmarshal(data, &r) != nil {
		return Cursor{}, false, nil
	}
	c := Cursor{SessionID: sessionID, ConversationID: r.ConversationID}
	if r.LastSeq != nil {
		c.NextSeq = *r.LastSeq + 1
	}
	if r.LastFingerprint != nil {
		c.LastFingerprint = *r.LastFingerprint
		c.Recovered = true
	}
	return c, true, nil
}

// ---- cursor persistence ----

// CursorPathFor returns the cursor file for a session.json path:
// <dir>/conversation-sync.json for the workspace session.json, and
// <dir>/<name>.conversation-sync.json for explicit --session-path files.
func CursorPathFor(sessionPath string) string {
	dir, base := filepath.Split(sessionPath)
	if base == "session.json" || base == "" {
		return filepath.Join(dir, CursorFileName)
	}
	return filepath.Join(dir, strings.TrimSuffix(base, filepath.Ext(base))+"."+CursorFileName)
}

// LoadCursor reads a cursor file.
func LoadCursor(path string) (Cursor, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Cursor{}, false
	}
	var c Cursor
	if json.Unmarshal(data, &c) != nil || c.SessionID == "" {
		return Cursor{}, false
	}
	return c, true
}

// SaveCursor writes the cursor atomically (0600).
func SaveCursor(path string, c Cursor) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".conversation-sync-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	_ = os.Chmod(name, 0o600)
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// DefaultBaseURL is the gateway (ERGO_BASE_URL overrides, as for /v1).
func DefaultBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("ERGO_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return client.ErgoDefaultBaseURL
}

// Platform is "<os>/<arch>".
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// SyncNow runs one synchronous pass for snap (headless `coder ask`).
// Signed-out and opted-out are not errors.
func (s *Syncer) SyncNow(ctx context.Context, snap Snapshot) error {
	err := s.syncOnce(ctx, snap)
	if errors.Is(err, errNotAuthenticated) || errors.Is(err, errDisabled) {
		return nil
	}
	return err
}
