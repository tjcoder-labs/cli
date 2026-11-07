package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Publication struct {
	ID              string `json:"id"`
	URL             string `json:"url"`
	ExpiresAt       string `json:"expiresAt"`
	RelayToken      string `json:"relayToken"`
	InvocationToken string `json:"invocationToken"`
}

// Relay opens one outbound HTTPS SSE stream; results and heartbeats go back via
// bounded POSTs. No local network listener or inbound firewall access is needed.
type Relay struct {
	BaseURL string
	Key     string
	Server  *Server
	Client  *http.Client
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
		return nil, fmt.Errorf("relay returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}
func (r *Relay) Run(ctx context.Context, announce func(Publication)) error {
	u, e := url.Parse(r.BaseURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("relay requires an HTTPS base URL")
	}
	if r.Key == "" {
		return fmt.Errorf("run coder login first")
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
	if e != nil || p.ID == "" || p.RelayToken == "" || p.InvocationToken == "" {
		return fmt.Errorf("invalid publication response")
	}
	// IDs returned by the relay cannot introduce paths, queries or alternate hosts.
	if strings.ContainsAny(p.ID, "/\\?#%") {
		return fmt.Errorf("invalid publication id")
	}
	path := "/cli/mcp/sessions/" + p.ID
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if v, err := r.request(c, "DELETE", path, p.RelayToken, nil); err == nil {
			v.Body.Close()
		}
	}()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, e = r.request(runCtx, "GET", path+"/connect", p.RelayToken, nil)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	announce(p)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
				c, stop := context.WithTimeout(runCtx, 10*time.Second)
				v, err := r.request(c, "POST", path+"/heartbeat", p.RelayToken, map[string]any{})
				if err == nil {
					v.Body.Close()
				}
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()
	scan := bufio.NewScanner(resp.Body)
	scan.Buffer(make([]byte, 4096), MaxMessage+4096)
	// Bound outstanding goroutines even if a compromised relay floods the host.
	slots := make(chan struct{}, 8)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	for scan.Scan() {
		line := scan.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var msg struct {
			RequestID string          `json:"requestId"`
			Body      json.RawMessage `json:"body"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &msg) != nil || msg.RequestID == "" || strings.ContainsAny(msg.RequestID, "/\\?#%") {
			return fmt.Errorf("invalid relay message")
		}
		select {
		case slots <- struct{}{}:
		case <-runCtx.Done():
			return runCtx.Err()
		}
		wg.Add(1)
		go func(id string, body json.RawMessage) {
			defer wg.Done()
			defer func() { <-slots }()
			c, stop := context.WithTimeout(runCtx, 115*time.Second)
			defer stop()
			b := r.Server.Handle(c, body)
			if b == nil {
				b = json.RawMessage(`{}`)
			}
			v, err := r.request(c, "POST", path+"/responses/"+id, p.RelayToken, json.RawMessage(b))
			if err == nil {
				v.Body.Close()
			}
		}(msg.RequestID, msg.Body)
	}
	if ctx.Err() != nil {
		return nil
	}
	if scan.Err() != nil {
		return fmt.Errorf("relay stream ended")
	}
	return fmt.Errorf("relay disconnected; session revoked")
}
