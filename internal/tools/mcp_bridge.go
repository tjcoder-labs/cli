package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/mcp"
)

// mcpTool is a bridge that allows an MCP tool to satisfy the internal Tool interface.
type mcpTool struct {
	client     *mcp.Client
	serverName string
	toolName   string
	def        mcp.ToolDefinition
}

func (t *mcpTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Function: client.FunctionDefinition{
			Name:        fmt.Sprintf("mcp_%s_%s", t.serverName, t.toolName),
			Description: t.def.Description,
			Parameters:  t.def.InputSchema,
		},
	}
}

func (t *mcpTool) Execute(ctx context.Context, args json.RawMessage, env ExecEnv) (Result, error) {
	content, err := t.client.CallTool(ctx, t.serverName, t.toolName, args)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Content: content,
		Preview: preview(content),
	}, nil
}

// RegisterMCPClient dynamically adds all tools from an MCP client to the registry.
func RegisterMCPClient(r *Registry, c *mcp.Client) {
	servers := c.GetServers()
	for serverName, s := range servers {
		if !s.IsEnabled {
			continue
		}
		tools := c.GetToolsForServer(serverName)
		for toolName, def := range tools {
			tool := &mcpTool{
				client:     c,
				serverName: serverName,
				toolName:   toolName,
				def:        def,
			}
			r.RegisterTool(tool)
		}
	}
}
