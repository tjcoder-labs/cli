// Package browser implements a Chrome DevTools Protocol (CDP) client that
// gives the agent full control of a locally running Chrome instance that was
// started with --remote-debugging-port (default 9222).
//
// Design goals:
//   - Single persistent browser-level WebSocket connection, multiplexed with
//     "flatten" per-target sessions.
//   - Considerate tab ownership: a registry records which session "owns" which
//     tab so concurrent agents never touch each other's (or the user's) tabs.
//   - Reliable automation on modern web apps: shadow-piercing selectors,
//     trusted input events, and self-verifying actions so hydration races are
//     invisible to callers.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// DefaultDebugPort is where Chrome exposes CDP when launched with
	// --remote-debugging-port=9222.
	DefaultDebugPort = 9222

	// OpTimeout bounds a single CDP command round-trip.
	OpTimeout = 45 * time.Second
)

// Client is a browser-level CDP connection that multiplexes commands to any
// number of attached page targets.
type Client struct {
	wsURL string

	mu      sync.Mutex // guards conn writes + waiters + subs
	conn    *websocket.Conn
	waiters map[int64]chan rpcMessage
	subs    map[string][]chan Event // method -> subscribers
	seq     int64
	closed  atomic.Bool
	done    chan struct{}
}

type rpcMessage struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Event is a CDP event delivered to subscribers.
type Event struct {
	Method    string
	SessionID string
	Params    json.RawMessage
}

// targetInfo mirrors the entries returned by /json and Target.getTargets.
type TargetInfo struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// wsURLForDebugPort fetches the browser-level webSocketDebuggerUrl from the
// HTTP discovery endpoint.
func wsURLForDebugPort(ctx context.Context, port int) (string, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	cli := &http.Client{Timeout: 5 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return "", fmt.Errorf("chrome devtools not reachable on port %d (start chrome with --remote-debugging-port=%d): %w", port, port, err)
	}
	defer resp.Body.Close()
	var v struct {
		WSURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.WSURL == "" {
		return "", fmt.Errorf("no webSocketDebuggerUrl from %s", url)
	}
	return v.WSURL, nil
}

// Connect dials the browser-level CDP endpoint for the given debug port and
// starts the read pump.
func Connect(ctx context.Context, port int) (*Client, error) {
	if port == 0 {
		port = DefaultDebugPort
	}
	wsURL, err := wsURLForDebugPort(ctx, port)
	if err != nil {
		return nil, err
	}
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := d.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("cdp dial: %w", err)
	}
	c := &Client{
		wsURL:   wsURL,
		conn:    conn,
		waiters: map[int64]chan rpcMessage{},
		subs:    map[string][]chan Event{},
		done:    make(chan struct{}),
	}
	go c.pump()
	return c, nil
}

// Closed reports whether the underlying connection has been torn down.
func (c *Client) Closed() bool {
	return c.closed.Load()
}

// Close tears down the connection.
func (c *Client) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		close(c.done)
		return c.conn.Close()
	}
	return nil
}

// pump reads frames, routing command responses to waiters and events to subs.
func (c *Client) pump() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.failAll()
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch, ok := c.waiters[msg.ID]
			if ok {
				delete(c.waiters, msg.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- msg
			}
			continue
		}
		// Event.
		if msg.Method != "" {
			ev := Event{Method: msg.Method, SessionID: msg.SessionID, Params: msg.Params}
			c.mu.Lock()
			var targets []chan Event
			for _, ch := range c.subs[msg.Method] {
				targets = append(targets, ch)
			}
			targets = append(targets, c.subs["*"]...)
			c.mu.Unlock()
			for _, ch := range targets {
				select {
				case ch <- ev:
				default: // drop if subscriber is slow; never block the pump
				}
			}
		}
	}
}

func (c *Client) failAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.waiters {
		delete(c.waiters, id)
		close(ch)
	}
}

// Call issues a CDP command on the browser session (empty sessionID) and
// returns the raw result. It applies OpTimeout.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.CallSession(ctx, "", method, params)
}

// CallSession issues a CDP command scoped to a specific target session.
func (c *Client) CallSession(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("cdp client closed")
	}
	id := atomic.AddInt64(&c.seq, 1)

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		rawParams = b
	}

	req := map[string]any{"id": id, "method": method}
	if sessionID != "" {
		req["sessionId"] = sessionID
	}
	if rawParams != nil {
		req["params"] = rawParams
	}

	wait := make(chan rpcMessage, 1)
	c.mu.Lock()
	c.waiters[id] = wait
	err := c.conn.WriteJSON(req)
	c.mu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("cdp write: %w", err)
	}

	tctx, cancel := context.WithTimeout(ctx, OpTimeout)
	defer cancel()
	select {
	case <-tctx.Done():
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("cdp %s: %w", method, tctx.Err())
	case resp, ok := <-wait:
		if !ok {
			return nil, fmt.Errorf("cdp connection lost during %s", method)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("cdp %s: %d %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// Subscribe registers a listener for a CDP method (or "*" for all events).
// The returned channel must be drained by the caller and released via
// Unsubscribe. Buffer is sized to absorb bursts; overflow events are dropped.
func (c *Client) Subscribe(method string, buf int) chan Event {
	if buf <= 0 {
		buf = 64
	}
	ch := make(chan Event, buf)
	c.mu.Lock()
	c.subs[method] = append(c.subs[method], ch)
	c.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscription channel.
func (c *Client) Unsubscribe(method string, ch chan Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := c.subs[method]
	for i, x := range list {
		if x == ch {
			c.subs[method] = append(list[:i], list[i+1:]...)
			break
		}
	}
}

// Attach attaches to a target using the flattened session model and returns
// its sessionID for use in CallSession.
func (c *Client) Attach(ctx context.Context, targetID string) (string, error) {
	res, err := c.Call(ctx, "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	return out.SessionID, nil
}

// Detach releases a previously attached session.
func (c *Client) Detach(ctx context.Context, sessionID string) error {
	_, err := c.Call(ctx, "Target.detachFromTarget", map[string]any{"sessionId": sessionID})
	return err
}

// ListTargets returns all current page/iframe targets via the HTTP endpoint.
func (c *Client) ListTargets(ctx context.Context) ([]TargetInfo, error) {
	// Use the browser-level Target.getTargets so we don't need the debug port
	// number here.
	res, err := c.Call(ctx, "Target.getTargets", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	infos := make([]TargetInfo, 0, len(out.TargetInfos))
	for _, t := range out.TargetInfos {
		infos = append(infos, TargetInfo{ID: t.TargetID, Type: t.Type, Title: t.Title, URL: t.URL})
	}
	return infos, nil
}

// NewTab creates a new tab (optionally with a starting URL) and returns its
// targetID.
func (c *Client) NewTab(ctx context.Context, url string) (string, error) {
	params := map[string]any{"url": "about:blank"}
	if url != "" {
		params["url"] = url
	}
	res, err := c.Call(ctx, "Target.createTarget", params)
	if err != nil {
		return "", err
	}
	var out struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	return out.TargetID, nil
}

// CloseTab closes (destroys) the given target/tab.
func (c *Client) CloseTab(ctx context.Context, targetID string) error {
	_, err := c.Call(ctx, "Target.closeTarget", map[string]any{"targetId": targetID})
	return err
}
