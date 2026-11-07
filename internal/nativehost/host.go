// Package nativehost implements Chrome native messaging and a private local RPC relay.
package nativehost

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const MaxFrame = 1024 * 1024

func SocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "coder", "browser.sock")
}

func ReadFrame(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, err
	}
	if n == 0 || n > MaxFrame {
		return nil, fmt.Errorf("invalid native frame length %d", n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	if err == nil && !json.Valid(b) {
		err = fmt.Errorf("invalid JSON frame")
	}
	return b, err
}
func WriteFrame(w io.Writer, b []byte) error {
	if len(b) == 0 || len(b) > MaxFrame || !json.Valid(b) {
		return fmt.Errorf("invalid native frame")
	}
	frame := make([]byte, 4+len(b))
	binary.LittleEndian.PutUint32(frame, uint32(len(b)))
	copy(frame[4:], b)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

type request struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Transport string          `json:"transport,omitempty"`
}

// Serve validates Chrome's origin argument before opening any listener. The socket's
// parent directory and socket are owner-only. A live host is never replaced.
func Serve(origin string, input io.Reader, output io.Writer, path string) error {
	if origin != "chrome-extension://"+ExtensionID+"/" {
		return fmt.Errorf("untrusted native messaging origin")
	}
	hello, e := ReadFrame(input)
	if e != nil {
		return e
	}
	var greeting struct {
		Method string `json:"method"`
		Params struct {
			Incognito bool `json:"incognito"`
		} `json:"params"`
	}
	if json.Unmarshal(hello, &greeting) != nil || greeting.Method != "Bridge.hello" {
		return fmt.Errorf("missing extension greeting")
	}
	if path == "" {
		path = SocketPath()
	}
	if greeting.Params.Incognito {
		path += ".incognito"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to replace non-socket %s", path)
		}
		c, e := net.DialTimeout("unix", path, 250*time.Millisecond)
		if e == nil {
			c.Close()
			return fmt.Errorf("browser host already running")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(path)
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	var writeMu, pendingMu sync.Mutex
	pending := map[int64]chan json.RawMessage{}
	var seq atomic.Int64
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); listener.Close() }) }
	defer stop()
	go func() {
		defer stop()
		for {
			b, e := ReadFrame(input)
			if e != nil {
				return
			}
			var m request
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			pendingMu.Lock()
			ch := pending[m.ID]
			pendingMu.Unlock()
			if ch != nil {
				select {
				case ch <- b:
				default:
				}
			}
		}
	}()
	for {
		conn, e := listener.Accept()
		if e != nil {
			select {
			case <-done:
				return nil
			default:
				return e
			}
		}
		go func(c net.Conn) {
			defer c.Close()
			scan := bufio.NewScanner(c)
			scan.Buffer(make([]byte, 4096), MaxFrame)
			for scan.Scan() {
				var req request
				if json.Unmarshal(scan.Bytes(), &req) != nil {
					return
				}
				original := req.ID
				req.ID = seq.Add(1)
				ch := make(chan json.RawMessage, 1)
				pendingMu.Lock()
				pending[req.ID] = ch
				pendingMu.Unlock()
				b, _ := json.Marshal(req)
				var response json.RawMessage
				var e error
				var target struct {
					TargetID string `json:"targetId"`
				}
				json.Unmarshal(req.Params, &target)
				private := strings.HasPrefix(req.SessionID, "incognito:") || strings.HasPrefix(target.TargetID, "incognito:")
				if private && !greeting.Params.Incognito {
					response, e = forwardPrivateReady(path+".incognito", b)
				} else {
					writeMu.Lock()
					e = WriteFrame(output, b)
					writeMu.Unlock()
				}
				if e == nil && response == nil {
					timer := time.NewTimer(45 * time.Second)
					select {
					case response = <-ch:
					case <-done:
						e = io.EOF
					case <-timer.C:
						e = fmt.Errorf("extension response timed out")
					}
					timer.Stop()
				}
				pendingMu.Lock()
				delete(pending, req.ID)
				pendingMu.Unlock()
				var fields map[string]json.RawMessage
				if e != nil {
					fields = map[string]json.RawMessage{}
					v, _ := json.Marshal(map[string]any{"code": -32000, "message": e.Error()})
					fields["error"] = v
				} else if json.Unmarshal(response, &fields) != nil {
					return
				}
				if req.Method == "Target.getTargets" && !greeting.Params.Incognito && fields["error"] == nil {
					if other, e := forwardPrivate(path+".incognito", b); e == nil {
						var extra struct {
							Result struct {
								Targets []json.RawMessage `json:"targetInfos"`
							} `json:"result"`
						}
						var result struct {
							Targets []json.RawMessage `json:"targetInfos"`
						}
						if json.Unmarshal(other, &extra) == nil && json.Unmarshal(fields["result"], &result) == nil {
							result.Targets = append(result.Targets, extra.Result.Targets...)
							fields["result"], _ = json.Marshal(result)
						}
					}
				}
				fields["id"], _ = json.Marshal(original)
				b, _ = json.Marshal(fields)
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, e = c.Write(append(b, '\n')); e != nil {
					return
				}
			}
		}(conn)
	}
}

// Register installs exact-origin host manifests. Chrome launches this absolute
// binary path; move/rebuild it in place rather than deleting it after registration.
func Register(binaryPath string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	binaryPath, err = filepath.Abs(binaryPath)
	if err != nil {
		return nil, err
	}
	dirs := []string{filepath.Join(home, ".config/google-chrome/NativeMessagingHosts"), filepath.Join(home, ".config/chromium/NativeMessagingHosts")}
	if runtime.GOOS == "darwin" {
		dirs = []string{filepath.Join(home, "Library/Application Support/Google/Chrome/NativeMessagingHosts"), filepath.Join(home, "Library/Application Support/Chromium/NativeMessagingHosts")}
	}
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("Windows native host registration is not supported yet")
	}
	b, _ := json.MarshalIndent(map[string]any{"name": HostID, "description": "Coder local browser bridge", "path": binaryPath, "type": "stdio", "allowed_origins": []string{"chrome-extension://" + ExtensionID + "/"}}, "", "  ")
	var paths []string
	for _, dir := range dirs {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		p := filepath.Join(dir, HostID+".json")
		if err = os.WriteFile(p, append(b, '\n'), 0600); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func forwardPrivate(path string, b []byte) (json.RawMessage, error) {
	c, e := net.DialTimeout("unix", path, 250*time.Millisecond)
	if e != nil {
		return nil, fmt.Errorf("private browser host unavailable: %w", e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(45 * time.Second))
	if _, e = c.Write(append(b, '\n')); e != nil {
		return nil, e
	}
	scan := bufio.NewScanner(c)
	scan.Buffer(make([]byte, 4096), MaxFrame)
	if !scan.Scan() {
		return nil, fmt.Errorf("private host response lost")
	}
	return append(json.RawMessage(nil), scan.Bytes()...), nil
}

// A newly created private window starts its split worker asynchronously.
func forwardPrivateReady(path string, b []byte) (json.RawMessage, error) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return forwardPrivate(path, b)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("private worker unavailable: enable Allow in incognito and reload extension")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
