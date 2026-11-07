package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/tjcoder-labs/cli/internal/nativehost"
	"net"
	"sync"
)

// Transport provides framed message IO while Client retains request correlation.
type Transport interface {
	ReadMessage() ([]byte, error)
	WriteJSON(any) error
	Close() error
}
type websocketTransport struct{ *websocket.Conn }

func (w websocketTransport) ReadMessage() ([]byte, error) {
	_, b, e := w.Conn.ReadMessage()
	return b, e
}

type socketTransport struct {
	net.Conn
	scan *bufio.Scanner
	mu   sync.Mutex
}

func (s *socketTransport) ReadMessage() ([]byte, error) {
	if s.scan.Scan() {
		return append([]byte(nil), s.scan.Bytes()...), nil
	}
	e := s.scan.Err()
	if e == nil {
		e = net.ErrClosed
	}
	return nil, e
}
func (s *socketTransport) WriteJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	b = append(b, '\n')
	for len(b) > 0 {
		n, e := s.Write(b)
		if e != nil {
			return e
		}
		b = b[n:]
	}
	return nil
}

// ConnectNative uses a private Unix socket. It never silently falls back to CDP.
func ConnectNative(ctx context.Context, tier string) (*Client, error) {
	conn, e := (&net.Dialer{}).DialContext(ctx, "unix", nativehost.SocketPath())
	if e != nil {
		return nil, e
	}
	scan := bufio.NewScanner(conn)
	scan.Buffer(make([]byte, 4096), nativehost.MaxFrame)
	c := &Client{conn: &socketTransport{Conn: conn, scan: scan}, tier: tier, waiters: map[int64]chan rpcMessage{}, subs: map[string][]chan Event{}, done: make(chan struct{})}
	go c.pump()
	return c, nil
}
