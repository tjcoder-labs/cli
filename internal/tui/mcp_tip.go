package tui

import (
	"github.com/tjcoder-labs/cli/internal/auth"
	"sort"
	"strings"
)

// Plain text: rendering escapes the whole tip, including configured names.
func (a *App) mcpTip(text string) string {
	names := make([]string, 0, len(a.config.MCPServers))
	for name := range a.config.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := strings.Fields(text)
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "add":
		return "/mcp add <name> <url|command> [args...] — connect HTTP or a local process; quote arguments with spaces."
	case "remove":
		if len(names) == 0 {
			return "/mcp remove <name> — no servers configured; use /mcp add first."
		}
		return "/mcp remove <name> — disconnect and forget a server: " + strings.Join(names, ", ")
	case "list":
		return "/mcp list — show configured servers, their tools and short descriptions."
	case "status":
		return "/mcp status — show transport, connection state, tool counts and last errors."
	case "tunnel":
		if a.mcpTunnel != nil {
			if a.mcpTunnel.stopping {
				return "MCP tunnel is stopping; revoking the secret link and cancelling tool calls."
			}
			if !a.mcpTunnel.ready {
				return "MCP tunnel is connecting; /mcp tunnel stop cancels it. The link will be the credential."
			}
			return "/mcp tunnel stop — revoke access. The link is the credential; sudo remains user-gated in this TUI."
		}
		if len(parts) > 2 && parts[2] == "stop" {
			return "/mcp tunnel stop — no tunnel is running."
		}
		if auth.ResolveErgoToken() == "" {
			return "Run /login first, then /mcp tunnel to open a secret capability URL. Sudo requires your TUI approval."
		}
		return "/mcp tunnel — open a secret capability URL for this session; sudo requires your TUI approval. /mcp tunnel stop revokes it."
	}
	if len(names) > 0 {
		return "/mcp list: tools · status: health · /mcp remove " + names[0] + ": disconnect · add: connect · tunnel: secret link."
	}
	if a.mcpTunnel != nil {
		return "No client servers configured. /mcp tunnel stop revokes the tunnel; add <name> <url|command> connects a server; list/status inspect it; remove forgets it."
	}
	return "No MCP servers or tunnel. /mcp add <name> <url|command> [args...] · list: tools · status: health · remove <name> · tunnel: secret link."
}
