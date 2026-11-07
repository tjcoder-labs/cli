# MCP integration

## Implemented client (0.9.257)

`coder` is an outbound MCP **client** and, since 0.9.258, a local/public MCP
**server** for its interactive toolset (see below).

`internal/mcp.Client` connects to HTTP endpoints (`type: "sse"`, the existing
JSON/SSE POST transport) and local processes (`type: "stdio"`). Both perform
`initialize`, `notifications/initialized`, `tools/list`, and `tools/call`.
The advertised protocol remains `2024-11-05`; the running CLI version is passed
through the TUI to `clientInfo.version` and the HTTP User-Agent. Standalone
library users may pass their version to `NewClient(version)` (default `dev`).

Discovered tools enter `internal/tools.Registry` as
`mcp_{server_name}_{tool_name}`. Remove unregisters tools and closes the transport.
TUI shutdown closes all MCP transports, including child processes.

### Commands

- `/mcp add <name> <http(s)://url>` preserves the remote-server form.
- `/mcp add <name> <command> [args...]` launches a local server. Executable paths
  and individual arguments may be single- or double-quoted, including spaces
  and empty arguments. Quotes only group argv: no shell, interpolation,
  pipelines, globbing or command substitution is performed.
- `/mcp list` shows servers and their discovered tool names and brief descriptions.
- `/mcp status` shows each server's name, transport, target, connected/disconnected
  state, tool count and last error. This is **last-observed** status, not a live
  health probe; HTTP reachability changes are detected on the next call. Local
  child exit/read failures are observed asynchronously. Disabled and failed
  saved configurations remain visible. Failed unsaved add attempts are visible
  until removed or retried, but are not persisted.
- `/mcp remove <name>` removes active, failed or disabled entries, unregisters
  tools, stops a local child if present, and saves the configuration.

### Persistence and process boundary

Configuration is saved in the workspace's `.ergo-cli-go/config.json`. Enabled
HTTP **and stdio** entries reconnect and rediscover tools at TUI startup.
An empty type is treated as `sse` for compatibility.

```json
{
  "mcp_servers": {
    "remote": {
      "type": "sse",
      "url": "https://example.com/mcp",
      "enabled": true
    },
    "local": {
      "type": "stdio",
      "command": "my-mcp-server",
      "args": ["--label", "one argument"],
      "env": {"SERVER_MODE": "read-only"},
      "enabled": true
    }
  }
}
```

The client API supports `AddServer(name, target, args...)` for URL/argv callers
and `AddServerConfig(name, ServerConfig)` for persisted configuration, including
explicit `env`. `Enabled` controls TUI startup loading; an explicit Add call
connects immediately.

Stdio messages are UTF-8, newline-delimited compact JSON-RPC, **not** LSP
`Content-Length` framing. Embedded text newlines are JSON escapes, never literal
line breaks in a message. Notifications have no ID; responses are correlated by
request ID, and stderr is drained/discarded as logging, never parsed as protocol.
Inbound messages are limited to 16 MiB. Requests/discovery have a 30-second bound;
callers can cancel earlier. We advertise no client capabilities and reject
server-to-client requests with method-not-found. Resources, prompts and sampling
remain unsupported.

Children run with an argv vector (`exec.Command`), never a shell. Only PATH,
HOME, USER, LOGNAME, TMPDIR, TEMP, TMP, SYSTEMROOT and WINDIR are inherited;
explicit `env` entries can override/add values. No other parent environment or
ambient API tokens are passed. This is **not a sandbox**: the child still runs
as the current OS user and can access that user's files, including config files.
Only configure trusted programs. Explicit env values and existing URL credentials
are stored in plain JSON; do not commit that file or publish status output.

On POSIX, children have a separate process group. Remove/shutdown closes stdin,
sends SIGTERM, waits up to 500 ms, then SIGKILLs and reaps the child; remaining
process-group descendants are killed as well. Windows uses process termination
(no POSIX process-group guarantee). Discovery failures also close the transport.

HTTP session-header negotiation, full long-lived SSE/cancellation support,
auth-header/credential-store integration and newer MCP revisions remain future
work; this change does not claim modern Streamable HTTP completeness.

## Implemented `coder mcp` host sessions (0.9.258)

The product goal is to let a remote consuming agent or service call the same
tools an interactive `coder` session already has. **No Slack integration**, no
Slack proxy, and no re-export of ergo-ai-server's own `/mcp` tools.

### Usage

Sign in in interactive `coder` with `/login`, then run in a terminal:

```sh
coder mcp --workspace-root /path/to/project
```

This loads `tools.NewRegistry`, the same default registry used by the TUI,
plus its task/memory/reminder `manage_items` bridge and workspace session state.
No model loop is involved: consumers call those in-process tools directly.
The browser bridge uses the existing private native relay and extension owner/
origin validation. UI-only tools have their existing headless behavior; there
is no TUI prompter attached. Sudo remains denied (no `--allow-sudo`).
Existing per-tool workspace checks and result/error handling apply. Shell and
other system tools retain their existing power as the current OS user: **this
is not a sandbox**. Running the command explicitly delegates published tool use
to anyone holding the invocation credential. Only publish to trusted consumers.

The foreground process prints a URL, local-time expiry and the invocation
credential to its terminal; it does not persist these secrets. A consumer uses:

```json
{
  "mcpServers": {
    "my-local-system": {
      "transport": "http",
      "url": "https://api.tjcoder.com/mcp/hosts/SESSION_ID",
      "headers": {"Authorization": "Bearer INVOCATION_TOKEN"}
    }
  }
}
```

A generic remote MCP client (including a service attaching a custom connector)
can initialize, list and call tools at that URL. Adding it to a specific bot is
a separate connector setup step; merely running the command does not attach it.

`coder mcp --stdio` serves local newline-delimited JSON-RPC only. If **either**
stdin or stdout is not a terminal, this mode is selected automatically and no
relay is opened. Thus piping stdout cannot accidentally leak a public token.
Public mode also requires stderr to be a terminal, so redirecting logs cannot
silently write an invocation secret to a file. For example configure a local MCP client with command `coder`, args
`["mcp", "--stdio", "--workspace-root", "/path/to/project"]`. Stdout contains
only protocol messages; login is not required for local stdio (provider-backed
tools may still need it). No local HTTP port is opened in either mode. An
internal authenticated HTTP handler is available/tested for loopback adapters,
but is not exposed as a CLI listener.

### Minimal relay architecture

One in-memory session store and two isolated Express routers on api.tjcoder.com:

- `POST /cli/mcp/sessions`: validates an active `ergo-cli` device-login key via
  existing `cliKeyAuth`; owner is derived from validated tenant/key records.
  Returns session ID, expiry, separate 256-bit relay/invocation capabilities,
  and the public MCP URL. No caller-provided identity or upstream URL is trusted.
- `GET /cli/mcp/sessions/:id/connect`: outbound authenticated HTTPS SSE from
  the CLI, using only the relay credential. Server-to-host frames carry a
  random correlation ID and JSON-RPC body; no HTTP caller headers are forwarded.
- `POST /cli/mcp/sessions/:id/responses/:requestId`: bounded host responses,
  also using the relay credential. Single-use correlation, not an upstream proxy.
- `POST /cli/mcp/sessions/:id/heartbeat`: host heartbeat every 10 seconds;
  renews a 30-second lease while connected, never beyond the one-hour hard age.
- `DELETE /cli/mcp/sessions/:id`: relay credential revokes this session only.
- `POST /mcp/hosts/:id`: public MCP JSON responses and 202 for notifications;
  requires that session's invocation Bearer on **every** request.

These routes mount before legacy `/mcp`, global large body parsers and payload
logging. Existing legacy MCP authentication/tools and CLI login are unchanged.
Capabilities are distinct and SHA-256 hashed in memory. A broad Ergo connector
key, CLI login key, another session's capability, query token or cookie cannot
invoke the host. Browser-origin requests are rejected; no CORS permission is
granted. Only JSON-RPC bodies cross to the host, not spoofed owner headers.
No arbitrary target URLs, SSH forwards, tunnel cookies, nginx changes or Slack
middleware are involved. The CLI requires HTTPS and refuses redirects.

Limits: 1 MiB request/response messages, eight outstanding calls per session,
120 calls/minute per session, ten creates/minute per IP, three active sessions
per validated owner key, 1,000 sessions globally. Execution is serialized to
match the interactive session's mutable tool state. Calls have a 115-second
host context and a 120-second relay deadline; tools must honor cancellation.

Ctrl-C/SIGTERM, observed stream disconnect and server restart revoke sessions;
the CLI also best-effort DELETEs on exit. A crashed/partitioned host loses its
lease within 30 seconds. New calls authenticate against revocation/expiry before
dispatch; in-flight host contexts are cancelled on stream exit where possible.
There is no automatic reconnection/republishing. Restart `coder mcp` for a fresh
URL and credentials. A capability can intentionally be reused during its lease.

### Deliberate v1 limits / future work

- Protocol `2024-11-05` tools-only JSON POST subset, not full modern Streamable
  HTTP. No MCP session headers, server-initiated consumer SSE, resources, prompts,
  sampling, OAuth discovery or per-consumer identities. Multiple consumers share
  the delegated session. Remote cancellation notifications are not implemented;
  disconnect/lease/deadline cancellation is best effort, not command rollback.
- This creates a separate foreground tool session, not an attachment to an
  already-running TUI. Saved `/mcp add` clients are **not** automatically restored
  or re-exported, avoiding trivial recursive proxies in this first release.
- In-memory, single-server store: a restart intentionally drops all publications.
  Multi-replica routing/shared leases and reconnect/resume are deferred.
- Lifetime and quotas are code constants initially. No persistent or unattended
  publications, no per-tool capability narrowing, no new OS sandbox.
