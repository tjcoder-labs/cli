package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Server represents an MCP server connection.
type Server struct {
	Name      string
	URL       string
	IsEnabled bool
	Transport *Transport
	tools     map[string]ToolDefinition
	mu        sync.RWMutex
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
	servers map[string]*Server
	mu      sync.RWMutex
}

func NewClient() *Client {
	return &Client{
		servers: make(map[string]*Server),
	}
}

// AddServer initializes a connection to an MCP server.
func (c *Client) AddServer(name, url string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.servers[name]; ok {
		return fmt.Errorf("server %q already exists", name)
	}

	server := &Server{
		Name:      name,
		URL:       url,
		IsEnabled: true,
		Transport: NewTransport(url),
		tools:     make(map[string]ToolDefinition),
	}

	// Initial discovery
	if err := c.discoverTools(server); err != nil {
		return fmt.Errorf("failed to discover tools for %q: %w", name, err)
	}

	c.servers[name] = server
	return nil
}

// discoverTools fetches the list of tools from the MCP server.
func (c *Client) discoverTools(s *Server) error {
	var resp ListToolsResponse
	err := s.Transport.SendRequest(context.Background(), "tools/list", nil, &resp)
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
