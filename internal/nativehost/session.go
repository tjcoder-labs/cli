package nativehost

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const Origin = "chrome-extension://" + ExtensionID + "/"

// Session owns the relay for one interactive CLI. Only Unix sockets are used.
// Separate listeners keep command clients out of the framed extension protocol.
// Same-user processes are trusted: origin argv is not an OS authentication token.
type Session struct {
	commands, native net.Listener
	backend          string
	mu               sync.Mutex
	closed           bool
	connections      map[net.Conn]bool
	wg               sync.WaitGroup
	once             sync.Once
}

// StartInteractive registers the running executable, then starts the local relay.
// Nothing here starts Chrome, installs an extension, or changes CDP defaults.
func StartInteractive(binary string) (*Session, error) {
	if _, err := Register(binary); err != nil {
		return nil, err
	}
	return StartSession(SocketPath())
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedByUser(info) || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("browser relay directory must be owned by the current user and mode 0700: %s", path)
	}
	return nil
}

// StartSession refuses to replace an existing socket, including crash leftovers.
// One interactive session owns the bridge at a time. Later sessions warn and
// continue the TUI with a warning, without changing any requested transport.
func StartSession(path string) (*Session, error) {
	if err := privateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	listen := func(p string) (net.Listener, error) {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return nil, fmt.Errorf("browser relay path already exists: %s", p)
		}
		l, err := net.Listen("unix", p)
		if err != nil {
			return nil, err
		}
		if err = os.Chmod(p, 0600); err != nil {
			l.Close()
			return nil, err
		}
		return l, nil
	}
	s := &Session{connections: map[net.Conn]bool{}}
	var err error
	if s.commands, err = listen(path); err != nil {
		return nil, err
	}
	if s.native, err = listen(path + ".native"); err != nil {
		s.commands.Close()
		return nil, err
	}
	// Isolate backend sockets from fixed client endpoints and old native hosts.
	dir, err := os.MkdirTemp(filepath.Dir(path), "session-")
	if err != nil {
		s.commands.Close()
		s.native.Close()
		return nil, err
	}
	s.backend = filepath.Join(dir, "extension.sock")
	s.wg.Add(2)
	go s.accept(s.commands, s.command)
	go s.accept(s.native, s.extension)
	return s, nil
}

func (s *Session) accept(l net.Listener, handle func(net.Conn)) {
	defer s.wg.Done()
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			c.Close()
			return
		}
		s.connections[c] = true
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() { c.Close(); s.mu.Lock(); delete(s.connections, c); s.mu.Unlock() }()
			handle(c)
		}()
	}
}

func (s *Session) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.commands.Close()
		s.native.Close()
		for c := range s.connections {
			c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
		os.RemoveAll(filepath.Dir(s.backend))
	})
	return nil
}

func (s *Session) command(c net.Conn) {
	scan := bufio.NewScanner(c)
	scan.Buffer(make([]byte, 4096), MaxFrame)
	for scan.Scan() {
		var req request
		if json.Unmarshal(scan.Bytes(), &req) != nil || req.Method == "" {
			return
		}
		// Command clients can never become extension peers on this listener.
		var response json.RawMessage
		var err error
		if strings.HasPrefix(req.Method, "Bridge.hello") {
			err = fmt.Errorf("extension greetings are not accepted on the command socket")
		} else {
			response, err = forwardPrivate(s.backend, scan.Bytes())
		}
		if err != nil {
			response, _ = json.Marshal(map[string]any{"id": req.ID, "error": map[string]any{"code": -32000, "message": "browser extension not connected: load/reconnect Coder Browser Bridge while interactive coder is running (" + err.Error() + ")"}})
		}
		c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err = c.Write(append(response, '\n')); err != nil {
			return
		}
	}
}

func (s *Session) extension(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := ReadFrame(c)
	if err != nil {
		return
	}
	var attach struct {
		Origin string `json:"origin"`
	}
	if json.Unmarshal(b, &attach) != nil || attach.Origin != Origin {
		return
	}
	// The shim has independently checked Chrome's argv before forwarding.
	// Owner-only filesystem access is the boundary against other OS users.
	if _, err = c.Write([]byte{1}); err != nil {
		return
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	// serveExtension consumes Bridge.hello before opening the per-profile backend.
	// Clear the initial deadline after hello, but retain frame length validation.
	hello, err := ReadFrame(c)
	if err != nil {
		return
	}
	c.SetDeadline(time.Time{})
	var framed strings.Builder
	// Use an io.MultiReader so the existing backend validates the greeting too.
	if err = WriteFrame(&framed, hello); err != nil {
		return
	}
	_ = serveExtension(attach.Origin, io.MultiReader(strings.NewReader(framed.String()), c), c, s.backend)
}

// Serve is Chrome's stdio shim, not a daemon. Never accept missing/foreign argv.
// Chrome invokes the manifest's coder executable directly with its origin argv.
func Serve(origin string, input io.Reader, output io.Writer, path string) error {
	if origin != Origin {
		return fmt.Errorf("untrusted native messaging origin")
	}
	if path == "" {
		path = SocketPath()
	}
	c, err := net.DialTimeout("unix", path+".native", time.Second)
	if err != nil {
		return fmt.Errorf("interactive coder browser relay unavailable: %w", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	b, _ := json.Marshal(map[string]string{"origin": origin})
	if err = WriteFrame(c, b); err != nil {
		return err
	}
	var ack [1]byte
	if _, err = io.ReadFull(c, ack[:]); err != nil || ack[0] != 1 {
		return fmt.Errorf("browser relay rejected extension attachment")
	}
	c.SetDeadline(time.Time{})
	// The input goroutine may remain blocked until Chrome closes stdin. This
	// shim exits when the relay closes; it never leaves a daemon or listener.
	go func() { _, _ = io.Copy(c, input); c.Close() }()
	_, err = io.Copy(output, c)
	return err
}
