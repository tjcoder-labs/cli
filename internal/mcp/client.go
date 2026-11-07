package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Server represents an MCP server connection.
type Server struct {
	Name      string
	URL       string
	IsEnabled bool
	Transport RPCTransport
	Type      string
	Command   string
	Args      []string
	connected bool
	lastError string
	tools     map[string]ToolDefinition
	mu        sync.RWMutex
}

// RPCTransport is shared by the HTTP and local-process clients.
type RPCTransport interface {
	SendRequest(context.Context, string, any, any) error
	Close() error
	Err() error
}

// ServerConfig is the connection configuration for either transport.
type ServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

func (sc ServerConfig) Target() string {
	if sc.Type == "stdio" {
		return strings.Join(append([]string{sc.Command}, sc.Args...), " ")
	}
	return sc.URL
}

// ServerStatus is a synchronized last-observed status, not a health probe.
type ServerStatus struct {
	Name, Type, Target, LastError string
	Connected                     bool
	ToolCount                     int
}

// ToolDefinition represents an MCP tool's schema.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ListToolsResponse matches the MCP spec for listing tools.
type ListToolsResponse struct {
	Tools []ToolDefinition `json:"tools"`
}

// Client manages multiple MCP servers.
type Client struct {
	servers  map[string]*Server
	mu       sync.RWMutex
	version  string
	failures map[string]ServerStatus
}

func NewClient(versions ...string) *Client {
	return &Client{
		servers:  make(map[string]*Server),
		version:  clientVersion(versions),
		failures: make(map[string]ServerStatus),
	}
}

// AddServer preserves URL callers and also accepts an executable plus argv.
func (c *Client) AddServer(name, target string, args ...string) error {
	sc := ServerConfig{Type: "stdio", Command: target, Args: args, Enabled: true}
	if strings.HasPrefix(strings.ToLower(target), "http://") || strings.HasPrefix(strings.ToLower(target), "https://") {
		if len(args) != 0 {
			return fmt.Errorf("HTTP servers do not accept command arguments")
		}
		sc = ServerConfig{Type: "sse", URL: target, Enabled: true}
	}
	return c.AddServerConfig(name, sc)
}

// AddServerConfig connects either transport. Failures remain visible to Statuses
// but never register tools. Call again to retry a failed connection.
func (c *Client) AddServerConfig(name string, sc ServerConfig) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.servers[name]; ok {
		return fmt.Errorf("server %q already exists", name)
	}
	if sc.Type == "" {
		sc.Type = "sse"
	}
	defer func() {
		if err != nil {
			c.failures[name] = ServerStatus{Name: name, Type: sc.Type, Target: sc.Target(), LastError: err.Error()}
		}
	}()
	if name == "" {
		return fmt.Errorf("missing server name")
	}
	server := &Server{Name: name, Type: sc.Type, URL: sc.URL, Command: sc.Command, Args: append([]string(nil), sc.Args...), IsEnabled: true, tools: make(map[string]ToolDefinition)}
	switch sc.Type {
	case "sse":
		u, parseErr := url.Parse(sc.URL)
		if parseErr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid HTTP(S) MCP URL")
		}
		server.Transport = NewTransport(sc.URL, c.version)
	case "stdio":
		if sc.Command == "" {
			return fmt.Errorf("missing stdio command")
		}
		server.Transport, err = NewStdioTransport(sc.Command, sc.Args, sc.Env, c.version)
		if err != nil {
			return fmt.Errorf("start stdio server %q: %w", name, err)
		}
	default:
		return fmt.Errorf("unsupported MCP transport %q", sc.Type)
	}
	if err = c.discoverTools(server); err != nil {
		_ = server.Transport.Close()
		return fmt.Errorf("failed to discover tools for %q: %w", name, err)
	}
	server.connected = true
	c.servers[name] = server
	delete(c.failures, name)
	return nil
}

// RemoveServer stops its child (if any) and forgets all discovered tools.
// The caller must also unregister the server's tool registry entries.
func (c *Client) RemoveServer(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.servers[name]
	if !ok {
		if _, failed := c.failures[name]; failed {
			delete(c.failures, name)
			return nil
		}
		return fmt.Errorf("server %q not found", name)
	}
	if s.Transport != nil {
		_ = s.Transport.Close()
	}
	s.mu.Lock()
	s.connected = false
	s.tools = make(map[string]ToolDefinition)
	s.mu.Unlock()
	delete(c.servers, name)
	delete(c.failures, name)
	return nil
}

// Close stops all local processes on application shutdown.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.servers {
		if s.Transport != nil {
			_ = s.Transport.Close()
		}
		s.mu.Lock()
		s.connected = false
		s.mu.Unlock()
	}
	return nil
}

func (c *Client) Statuses() map[string]ServerStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]ServerStatus)
	for name, status := range c.failures {
		result[name] = status
	}
	for name, s := range c.servers {
		s.mu.RLock()
		typ := s.Type
		if typ == "" {
			typ = "sse"
		}
		status := ServerStatus{Name: name, Type: typ, Target: (ServerConfig{Type: typ, URL: s.URL, Command: s.Command, Args: s.Args}).Target(), Connected: s.connected, ToolCount: len(s.tools), LastError: s.lastError}
		s.mu.RUnlock()
		if s.Transport != nil {
			if err := s.Transport.Err(); err != nil {
				status.Connected = false
				status.LastError = err.Error()
			}
		}
		result[name] = status
	}
	return result
}

// SetToolsForServer replaces the discovered tool list for a server.
// Intended for tests that need to seed `mcp_{name}_*` registrations
// without performing a real `tools/list` round-trip. It is exported
// so external test packages can drive the client deterministically.
func (c *Client) SetToolsForServer(name string, defs map[string]ToolDefinition) {
	c.mu.RLock()
	s, ok := c.servers[name]
	c.mu.RUnlock()
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = make(map[string]ToolDefinition, len(defs))
	for k, v := range defs {
		s.tools[k] = v
	}
}

// discoverTools fetches the list of tools from the MCP server.
func (c *Client) discoverTools(s *Server) error {
	var resp ListToolsResponse
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := s.Transport.SendRequest(ctx, "tools/list", nil, &resp)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range resp.Tools {
		s.tools[t.Name] = t
	}
	return nil
}

// GetServers returns the internal server map.
func (c *Client) GetServers() map[string]*Server {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.servers
}

// GetToolsForServer safely returns a copy of the tools available on a
// specific server, locking the server's mutex to prevent data races
// during concurrent reads.
func (c *Client) GetToolsForServer(name string) map[string]ToolDefinition {
	c.mu.RLock()
	s, ok := c.servers[name]
	c.mu.RUnlock()

	if !ok {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy so callers cannot mutate our internal state
	// outside of our locking discipline.
	tools := make(map[string]ToolDefinition, len(s.tools))
	for k, v := range s.tools {
		tools[k] = v
	}
	return tools
}

// SetToolsForTest injects a tool definition into a server's tool
// map. It is intended for tests that want to exercise the registry
// bridge without standing up a real MCP server. Not safe to call
// from production code.
func (c *Client) SetToolsForTest(serverName string, defs map[string]ToolDefinition) {
	c.mu.RLock()
	s, ok := c.servers[serverName]
	c.mu.RUnlock()
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = make(map[string]ToolDefinition, len(defs))
	for k, v := range defs {
		s.tools[k] = v
	}
}

// CallTool invokes a tool on a specific MCP server.
func (c *Client) CallTool(ctx context.Context, serverName, toolName string, args json.RawMessage) (string, error) {
	c.mu.RLock()
	s, ok := c.servers[serverName]
	c.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("MCP server %q not found", serverName)
	}

	// MCP tool call params
	type CallParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	params := CallParams{
		Name:      toolName,
		Arguments: args,
	}

	var result json.RawMessage
	err := s.Transport.SendRequest(ctx, "tools/call", params, &result)
	s.mu.Lock()
	var rpcErr *JSONRPCError
	s.connected = err == nil || errors.As(err, &rpcErr)
	s.lastError = ""
	if err != nil {
		s.lastError = err.Error()
	}
	s.mu.Unlock()
	if err != nil {
		return "", err
	}

	// MCP returns a result object containing content (usually text)
	type CallResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	var final CallResult
	if err := json.Unmarshal(result, &final); err != nil {
		return string(result), nil // Fallback to raw JSON
	}

	var combined string
	for _, c := range final.Content {
		if c.Type == "text" {
			combined += c.Text + "\n"
		}
	}

	return combined, nil
}
