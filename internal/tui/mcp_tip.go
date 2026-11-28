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
		subcmd := ""
		if len(parts) > 2 {
			subcmd = strings.ToLower(parts[2])
		}
		if a.mcpTunnel != nil {
			if a.mcpTunnel.stopping {
				return "MCP tunnel is stopping; revoking the secret link and cancelling tool calls."
			}
			if !a.mcpTunnel.ready {
				return "MCP tunnel is connecting; /mcp tunnel stop cancels it. The link will be the credential."
			}
			switch subcmd {
			case "show":
				return "/mcp tunnel show — reveal the secret link in the bar (same as F7). /mcp tunnel hide masks it; /mcp tunnel copy copies it."
			case "hide":
				return "/mcp tunnel hide — mask the secret link in the bar (same as F7). /mcp tunnel show reveals it."
			case "copy":
				return "/mcp tunnel copy — copy the revealed link (same as F8). Reveal it first with /mcp tunnel show or F7."
			case "stop":
				return "/mcp tunnel stop — revoke the tunnel link and cancel its tool calls."
			case "save":
				return "/mcp tunnel save — persist the token so a later /mcp tunnel reattaches to the same link."
			}
			return "/mcp tunnel show, hide, and copy work without function keys (or F7/F8). /mcp tunnel save keeps this link for reattach · /mcp tunnel stop revokes it. Sudo stays user-gated."
		}
		switch subcmd {
		case "stop":
			return "/mcp tunnel stop — no tunnel is running."
		case "save":
			return "/mcp tunnel save — needs a running tunnel; start one with /mcp tunnel."
		case "show", "hide", "copy":
			return "/mcp tunnel " + subcmd + " — needs a running tunnel; start one with /mcp tunnel."
		}
		if auth.ResolveErgoToken() == "" {
			return "Run /login first, then /mcp tunnel to open a secret capability URL. Sudo requires your TUI approval."
		}
		return "/mcp tunnel opens a secret link · show/hide/copy work without function keys · save reattaches later · stop revokes. F7 or /mcp tunnel show, F8 or /mcp tunnel copy; sudo needs your TUI approval."
	}
	if len(names) > 0 {
		return "/mcp list: tools · status: health · /mcp remove " + names[0] + ": disconnect · add: connect · tunnel: secret link."
	}
	if a.mcpTunnel != nil {
		return "No client servers configured. /mcp tunnel stop revokes the tunnel; add <name> <url|command> connects a server; list/status inspect it; remove forgets it."
	}
	return "No MCP servers or tunnel. /mcp add <name> <url|command> [args...] · list: tools · status: health · remove <name> · tunnel: secret link."
}
