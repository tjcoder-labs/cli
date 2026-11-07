package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

var publicCapabilityURL = regexp.MustCompile(`^https://api\.tjcoder\.com/mcp/hosts/[A-Za-z0-9_-]{43}$`)

type Publication struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	ExpiresAt  string `json:"expiresAt"`
	RelayToken string `json:"relayToken"`
}

// Relay opens one outbound HTTPS SSE stream; results and heartbeats go back via
// bounded POSTs. No local network listener or inbound firewall access is needed.
type Relay struct {
	BaseURL string
	Key     string
	Server  *Server
	Client  *http.Client
	// Events, when set, receives reconnect and consumer notices. Events never
	// carry the capability URL, relay token or caller identity, only the
	// server-assigned opaque consumer id.
	Events func(RelayEvent)
}

type RelayEventKind int

const (
	// RelayReconnecting: the stream dropped; the same URL stays valid while the
	// relay dials back in (server grace is 10 minutes within the 1h maximum).
	RelayReconnecting RelayEventKind = iota
	// RelayReconnected: the same session is attached again.
	RelayReconnected
	// RelayConsumer: a request arrived from an opaque consumer id.
	RelayConsumer
)

type RelayEvent struct {
	Kind       RelayEventKind
	Attempt    int
	ConsumerID string
	Err        error
}

// Tunables (overridden in tests).
var (
	relayHeartbeatEvery = 10 * time.Second
	relayStreamIdle     = 35 * time.Second // server keepalive is every 10s
	relayBackoffBase    = time.Second
	relayBackoffMax     = 30 * time.Second
)

var opaqueConsumer = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// errSessionGone means the relay no longer knows this session (401): it was
// revoked or expired. The CLI never silently mints a replacement URL.
var errSessionGone = errors.New("tunnel session ended (revoked or expired); start a new tunnel for a new link")

type statusError int

func (e statusError) Error() string { return fmt.Sprintf("relay returned HTTP %d", int(e)) }

func (r *Relay) emit(ev RelayEvent) {
	if r.Events != nil {
		r.Events(ev)
	}
}

func (r *Relay) client() *http.Client {
	c := &http.Client{}
	if r.Client != nil {
		*c = *r.Client
	}
	// Credentials must never follow redirects to a different service (or downgrade).
	c.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return c
}
func (r *Relay) request(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		reader = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(r.BaseURL, "/")+path, reader)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := r.client().Do(req)
	if e != nil {
		return nil, fmt.Errorf("relay connection failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, statusError(resp.StatusCode)
	}
	return resp, nil
}
func (r *Relay) Run(ctx context.Context, announce func(Publication)) error {
	u, e := url.Parse(r.BaseURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("relay requires an HTTPS base URL")
	}
	if r.Key == "" {
		return fmt.Errorf("sign in with /login in interactive coder before starting an MCP tunnel")
	}
	createCtx, stop := context.WithTimeout(ctx, 15*time.Second)
	resp, e := r.request(createCtx, "POST", "/cli/mcp/sessions", r.Key, map[string]any{})
	if e != nil {
		stop()
		return e
	}
	var p Publication
	e = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&p)
	resp.Body.Close()
	stop()
	if e != nil || p.ID == "" || p.RelayToken == "" || p.URL == "" {
		return fmt.Errorf("invalid publication response")
	}
	// IDs returned by the relay cannot introduce paths, queries or alternate hosts.
	if strings.ContainsAny(p.ID, "/\\?#%") {
		return fmt.Errorf("invalid publication id")
	}
	path := "/cli/mcp/sessions/" + p.ID
	gone := false
	defer func() {
		if gone {
			return // nothing left to revoke
		}
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if v, err := r.request(c, "DELETE", path, p.RelayToken, nil); err == nil {
			v.Body.Close()
		}
	}()
	if !publicCapabilityURL.MatchString(p.URL) {
		return fmt.Errorf("relay requires secret-URL MCP access (server 1.1.750+)")
	}
	expiry, _ := time.Parse(time.RFC3339, p.ExpiresAt)

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// Heartbeats run for the session's whole life, independent of the stream,
	// so the lease is renewed while reconnecting. Only 401 ends the session.
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		tick := time.NewTicker(relayHeartbeatEvery)
		defer tick.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
				c, stop := context.WithTimeout(runCtx, 10*time.Second)
				v, err := r.request(c, "POST", path+"/heartbeat", p.RelayToken, map[string]any{})
				stop()
				if err == nil {
					v.Body.Close()
				} else if errors.Is(err, statusError(http.StatusUnauthorized)) {
					cancel(errSessionGone)
					return
				}
			}
		}
	}()
	slots := make(chan struct{}, 8)
	var wg sync.WaitGroup
	defer func() { cancel(nil); <-heartbeatDone; wg.Wait() }()

	announced, attempt, backoff := false, 0, relayBackoffBase
	for {
		err := r.serve(runCtx, path, p.RelayToken, slots, &wg, func() {
			if !announced {
				announced = true
				announce(p)
			} else {
				r.emit(RelayEvent{Kind: RelayReconnected, Attempt: attempt})
			}
			attempt, backoff = 0, relayBackoffBase
		})
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, statusError(http.StatusUnauthorized)) || errors.Is(context.Cause(runCtx), errSessionGone) {
			gone = true
			return errSessionGone
		}
		if !expiry.IsZero() && !time.Now().Before(expiry) {
			return fmt.Errorf("tunnel reached its one-hour maximum; start a new tunnel for a new link")
		}
		attempt++
		r.emit(RelayEvent{Kind: RelayReconnecting, Attempt: attempt, Err: err})
		select {
		case <-runCtx.Done():
			if ctx.Err() != nil {
				return nil
			}
			gone = true
			return errSessionGone
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > relayBackoffMax {
			backoff = relayBackoffMax
		}
	}
}

// serve attaches one SSE stream (replacing any half-open one server-side) and
// dispatches requests until it ends. Tool calls use the session context, not
// the stream's, so a stream swap does not cancel local work.
func (r *Relay) serve(ctx context.Context, path, token string, slots chan struct{}, wg *sync.WaitGroup, connected func()) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, e := r.request(streamCtx, "GET", path+"/connect", token, nil)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	connected()
	// A half-open TCP stream (sleep, network switch) produces no error; the
	// server's 10s keepalive comments let us detect silence and redial.
	idle := time.AfterFunc(relayStreamIdle, cancel)
	defer idle.Stop()
	scan := bufio.NewScanner(resp.Body)
	scan.Buffer(make([]byte, 4096), MaxMessage+4096)
	for scan.Scan() {
		idle.Reset(relayStreamIdle)
		line := scan.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var msg struct {
			RequestID  string          `json:"requestId"`
			ConsumerID string          `json:"consumerId"`
			Body       json.RawMessage `json:"body"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &msg) != nil || msg.RequestID == "" || strings.ContainsAny(msg.RequestID, "/\\?#%") {
			return fmt.Errorf("invalid relay message")
		}
		if opaqueConsumer.MatchString(msg.ConsumerID) {
			r.emit(RelayEvent{Kind: RelayConsumer, ConsumerID: msg.ConsumerID})
		}
		select {
		case slots <- struct{}{}:
		case <-streamCtx.Done():
			return fmt.Errorf("relay stream ended")
		}
		wg.Add(1)
		go func(id string, body json.RawMessage) {
			defer wg.Done()
			defer func() { <-slots }()
			c, stop := context.WithTimeout(ctx, 115*time.Second)
			defer stop()
			b := r.Server.Handle(c, body)
			if b == nil {
				b = json.RawMessage(`{}`)
			}
			v, err := r.request(c, "POST", path+"/responses/"+id, token, json.RawMessage(b))
			if err == nil {
				v.Body.Close()
			}
		}(msg.RequestID, msg.Body)
	}
	return fmt.Errorf("relay stream ended")
}
