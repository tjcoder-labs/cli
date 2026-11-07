package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// StdioTransport speaks newline-delimited JSON-RPC, never LSP framing.
// A dedicated reader correlates responses; notifications cannot consume a call.
type StdioTransport struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	version   string
	initOnce  sync.Once
	initErr   error
	closeOnce sync.Once
	writeMu   sync.Mutex
	mu        sync.Mutex
	nextID    uint64
	pending   map[string]chan JSONRPCResponse
	err       error
	stopped   chan struct{}
	exited    chan struct{}
}

// NewStdioTransport executes argv directly, without a shell. Only a small
// portability allowlist and explicitly configured environment reach the child.
func NewStdioTransport(command string, args []string, env map[string]string, versions ...string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = stdioEnvironment(env)
	configureStdioProcess(cmd)
	cmd.Stderr = io.Discard // drain logs; stderr is never protocol input
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	t := &StdioTransport{cmd: cmd, stdin: stdin, version: clientVersion(versions), pending: make(map[string]chan JSONRPCResponse), stopped: make(chan struct{}), exited: make(chan struct{})}
	go t.read(stdout)
	go func() {
		err := cmd.Wait()
		if err == nil {
			err = io.EOF
		}
		t.fail(fmt.Errorf("stdio process exited: %w", err))
		close(t.exited)
	}()
	return t, nil
}

func stdioEnvironment(extra map[string]string) []string {
	values := make(map[string]string)
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TEMP", "TMP", "SYSTEMROOT", "WINDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}
	for key, value := range extra {
		values[key] = value
	}
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func (t *StdioTransport) fail(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err == nil {
		t.err = err
		close(t.stopped)
	}
}
func (t *StdioTransport) Err() error { t.mu.Lock(); defer t.mu.Unlock(); return t.err }

func (t *StdioTransport) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *JSONRPCError   `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			t.fail(fmt.Errorf("invalid stdio JSON-RPC: %w", err))
			return
		}
		if envelope.Method != "" {
			if len(envelope.ID) != 0 { // We advertise no client capabilities.
				go func(id json.RawMessage) {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					_ = t.write(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "error": JSONRPCError{Code: -32601, Message: "Client method not supported"}})
				}(envelope.ID)
			}
			continue
		}
		t.mu.Lock()
		ch := t.pending[string(envelope.ID)]
		t.mu.Unlock()
		if ch != nil {
			select {
			case ch <- JSONRPCResponse{Result: envelope.Result, Error: envelope.Error}:
			default:
			}
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	t.fail(fmt.Errorf("stdio read: %w", err))
}

func (t *StdioTransport) write(ctx context.Context, message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if strings.ContainsAny(string(payload), "\r\n") {
		return fmt.Errorf("stdio message contains a literal newline")
	}
	done := make(chan error, 1)
	go func() {
		t.writeMu.Lock()
		defer t.writeMu.Unlock()
		if err := t.Err(); err != nil {
			done <- err
			return
		}
		_, err := t.stdin.Write(append(payload, '\n'))
		if err != nil {
			t.fail(err)
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = t.Close()
		return ctx.Err()
	case <-t.stopped:
		return t.Err()
	}
}

func (t *StdioTransport) request(ctx context.Context, method string, params any, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	key := fmt.Sprint(id)
	ch := make(chan JSONRPCResponse, 1)
	t.pending[key] = ch
	t.mu.Unlock()
	defer func() { t.mu.Lock(); delete(t.pending, key); t.mu.Unlock() }()
	if err := t.write(ctx, JSONRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: encodeParams(params)}); err != nil {
		return err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		return json.Unmarshal(resp.Result, target)
	case <-ctx.Done():
		return ctx.Err()
	case <-t.stopped:
		return t.Err()
	}
}

func (t *StdioTransport) SendRequest(ctx context.Context, method string, params any, target any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	t.initOnce.Do(func() {
		var result json.RawMessage
		if err := t.request(ctx, "initialize", initializeParams(t.version), &result); err != nil {
			t.initErr = fmt.Errorf("initialize handshake failed: %w", err)
			return
		}
		t.initErr = t.write(ctx, JSONRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})
	})
	if t.initErr != nil {
		return t.initErr
	}
	return t.request(ctx, method, params, target)
}

func (t *StdioTransport) Close() error {
	t.closeOnce.Do(func() {
		t.fail(fmt.Errorf("stdio transport closed"))
		_ = t.stdin.Close()
		terminateStdioProcess(t.cmd)
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-t.exited:
		case <-timer.C:
			killStdioProcess(t.cmd)
			<-t.exited
		}
		// Kill any descendants still holding inherited pipes in the process group.
		killStdioProcess(t.cmd)
	})
	return nil
}
