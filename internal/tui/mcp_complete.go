package tui

import (
	"sort"
	"strings"
)

// mcpArgHints feeds "/mcp …" into the same argument palette as /add: filtered,
// highlighted suggestions as you type; Tab inserts, Shift+Tab cycles. ok is
// false once the input is free-form (e.g. /mcp add arguments), where the
// contextual usage tip takes over.
func (a *App) mcpArgHints(text string) (string, []wsArg, bool) {
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return "", nil, false
	}
	body := strings.TrimPrefix(text, "/")
	i := strings.IndexAny(body, " \t")
	if i == -1 || !strings.EqualFold(body[:i], "mcp") {
		return "", nil, false
	}
	rest := strings.TrimLeft(body[i+1:], " \t")
	if r, found := cutWord(rest, "remove"); found {
		return a.mcpServerArgs(r, "remove ")
	}
	if r, found := cutWord(rest, "rm"); found {
		return a.mcpServerArgs(r, "rm ")
	}
	if r, found := cutWord(rest, "tunnel"); found {
		if strings.ContainsAny(r, " \t") {
			return "", nil, false
		}
		var items []wsArg
		if strings.HasPrefix("stop", strings.ToLower(r)) {
			items = append(items, wsArg{label: "stop", insert: "tunnel stop", desc: a.mcpTunnelStopDesc()})
		}
		return "tunnel:", items, true
	}
	if strings.ContainsAny(rest, " \t") {
		return "", nil, false
	}
	q := strings.ToLower(rest)
	var items []wsArg
	for _, it := range a.mcpSubcommandArgs() {
		word := strings.Fields(it.insert)[0]
		if q == "" || strings.HasPrefix(strings.TrimSpace(it.insert), q) || (q != "" && strings.HasPrefix("rm", q) && word == "remove") {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return "", nil, false
	}
	return "mcp:", items, true
}

func (a *App) mcpSubcommandArgs() []wsArg {
	tunnel := wsArg{label: "tunnel", insert: "tunnel", desc: "open a secret capability URL for this session; sudo stays user-gated"}
	if a.mcpTunnel != nil {
		tunnel.desc = "a tunnel is running; use tunnel stop to revoke it"
	}
	return []wsArg{
		{label: "list", insert: "list", desc: "configured servers, their tools and descriptions"},
		{label: "status", insert: "status", desc: "transport, connection state, tool counts, last errors"},
		{label: "add <name> <url|command>", insert: "add ", desc: "connect an HTTP server or local process"},
		{label: "remove <name>", insert: "remove ", desc: "disconnect and forget a server (alias: rm)"},
		tunnel,
		{label: "tunnel stop", insert: "tunnel stop", desc: a.mcpTunnelStopDesc()},
	}
}

func (a *App) mcpTunnelStopDesc() string {
	if a.mcpTunnel == nil {
		return "revoke the tunnel link (none running)"
	}
	return "revoke the tunnel link and cancel its tool calls"
}

func (a *App) mcpServerArgs(prefix, insertPrefix string) (string, []wsArg, bool) {
	if strings.ContainsAny(prefix, " \t") {
		return "", nil, false
	}
	names := make([]string, 0, len(a.config.MCPServers))
	for name := range a.config.MCPServers {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	items := make([]wsArg, 0, len(names))
	for _, n := range names {
		items = append(items, wsArg{label: n, insert: insertPrefix + n, desc: "disconnect and forget " + n})
		if len(items) >= wsArgMaxItems {
			break
		}
	}
	return "remove server:", items, true
}
