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

## Implemented MCP tunnels (0.9.261)

The product goal is to let a remote consuming agent or service call the same
tools an interactive `coder` session already has. **No Slack integration**, no
Slack proxy, and no re-export of ergo-ai-server's own `/mcp` tools.

### Usage

Sign in in interactive `coder` with `/login`, then run in a terminal:

```sh
coder mcp tunnel --workspace-root /path/to/project
```

Bare `coder mcp` on a terminal shows usage; it never silently opens a public
session. `coder mcp tunnel --help` describes the explicit public command and
local `--stdio` alternative. Standalone tunnels deny sudo.

#### Interactive tunnel

From an already-running interactive session:

```text
/login
/mcp tunnel
/mcp tunnel stop
```

The TUI opens the same outbound HTTPS relay without a local network listener.
It exposes **the running session's registry and workspace set**, including
connected MCP tools, rather than creating a second independent session. Calls
reserve the same busy slot as an agent turn; if the session is busy, consumers
receive a tool error asking them to retry. Registry discovery and lifecycle
updates are marshalled through the UI event loop. `/mcp tunnel stop` works even
while a tool is waiting for approval, cancels outstanding calls and best-effort
revokes the session. Exiting the TUI also cancels and revokes it. A failed DELETE
falls back to relay lease expiry; cancellation is not command rollback.

Since 0.9.263 the secret capability URL stays in a dedicated, themed bar pinned
immediately above the chat input, **not** an overlay/modal. Its URL-only rows
span the terminal width, without conversation or side-panel text behind them.
Since 0.9.264 the banner is housed in its own input-style surface: it shares the
chat input's themed background, focus-sensitive left accent beam, symmetric edge
padding and text insets, with top/bottom breathing room. A root-colored gutter
separates the sibling surfaces. URL wrapping accounts for the padded content
width; F8 still copies the full link independently of wrapping.
The stable prefix is visible and the secret path token is masked by default.
The bar also shows expiry and keyboard hints:

- **F7** toggles show/hide of the token without changing chat input or focus.
- **F8**, after revealing the token, requests copying the exact full URL through
  the terminal clipboard facility (tcell / OSC 52 where supported). No label,
  wrapping newline, expiry, or chat text is included. Copying while masked is
  refused with a hint to reveal first. Use Fn+F7/F8 on media-key keyboards.
- If the terminal blocks clipboard requests, reveal the token and select the
  URL-only row(s) with normal terminal selection instead. No overlay can
  contaminate that selection; expand a narrow terminal to keep it on one row.

Since 0.9.265 the banner hint also shows live status: the number of unique
consumer connections (opaque server-assigned IDs; a consumer idle for 5 minutes
drops off), the tool currently in flight (`→ run_command`, `+N` when several are
queued), and `reconnecting…` while the relay redials. Activity gets one line when
a drop starts and one when the same link is live again; the URL is never logged.

The link is held only in tunnel-owned memory/the dedicated view, **not** activity
history, conversation, input history, or saved configuration. Toggling and copying
do not log it. Stop immediately hides/scrubs the bar and requests revocation;
TUI exit also scrubs it. The bar remains pinned through resize/fullscreen while
the tunnel is live. Clipboard managers, recordings and screenshots may retain a
revealed/copied URL, so treat it as a password. A second `/mcp tunnel` reports
that one is already running; use `stop` before restarting.

Interactive tools use exactly the runner's `ExecEnv`, including its Prompter
and SudoPolicy. A consuming service calling `run_command` with sudo must pass
the **existing TUI sudo gate**: the owner chooses Allow once, Allow sudo for this
session, or Deny. When required, only the owner types the password into the
masked card; validation uses `sudo -S -v` privately. The password never goes to
the consumer, model, logs, transcript or disk, and the PATH shim uses `sudo -n`.
The consumer receives only command output or a denial/error, not the approval
choice. Explicit session-wide approval has the same scope as in interactive
agent turns; the URL itself does not grant unattended root. No `--allow-sudo`
flag is passed or implied. Standalone `coder mcp tunnel` has no TUI prompter and
continues to deny sudo.

The `/mcp` hint line describes add/list/status/remove/tunnel and adapts to
configured names, the selected subcommand, login requirements, and whether the
tunnel is connecting, active, or stopping. Add explains URL versus command/args;
remove suggests configured names (escaped for UI markup); tunnel explains the
secret-link credential and the user-controlled sudo gate.

#### Standalone tunnel

This loads `tools.NewRegistry`, the same default registry used by the TUI,
plus its task/memory/reminder `manage_items` bridge and workspace session state.
No model loop is involved: consumers call those in-process tools directly.
The browser bridge uses the existing private native relay and extension owner/
origin validation. UI-only tools have their existing headless behavior; there
is no TUI prompter attached. Sudo remains denied (no `--allow-sudo`).
Existing per-tool workspace checks and result/error handling apply. Shell and
other system tools retain their existing power as the current OS user: **this
is not a sandbox**. Running the command explicitly delegates published tool use
to anyone holding the secret capability URL. Only publish to trusted consumers.

Since 0.9.262 the standalone public host also prints consumer tool activity to
terminal **stderr**, using the same `theme.Current()` roles as the TUI:
Deep `→ tool` for starts, Bright bold `✓ tool` / `✗ tool` for outcomes, Text
for summaries and Dim for timestamps. For example (shown here without color):

```text
1:49 AM  → read_file path="README.md" · start_line=1
1:49 AM  ✓ read_file completed · 820 bytes · 12ms
1:49 AM  → run_command command=git (arguments hidden)
1:49 AM  ✗ run_command failed · command exit code 1 · 8ms
```

This is not a JSON-RPC dump. The bounded argument summary only shows paths,
a few numeric/boolean controls, and known executable names. Credential-like
fields are redacted; arbitrary values, full command arguments, URLs, editing
bodies and tool result contents/previews are withheld. Outcomes show byte count
and elapsed time or a fixed-vocabulary error category, never raw error text
that could echo secrets. Terminal control characters are not accepted in shown
metadata. These activity lines are not saved to history or any log file by the
CLI; terminal recording remains the operator's responsibility. `NO_COLOR` or
`TERM=dumb` disables ANSI, not the readable start/outcome lines. The initial
secret link display below is unchanged; invocation activity never repeats it.
Local `--stdio` and piped modes do not attach the activity printer, so their
stdout remains exclusively MCP protocol messages. Standalone sudo stays denied.

The foreground process prints **one secret URL plus local-time expiry** to its
terminal; it does not persist the URL secret. The link alone is sufficient for
MCP initialize, tools/list and tools/call; no Authorization header is needed.
This is capability-URL / connector-style access. A consumer uses:

```json
{
  "mcpServers": {
    "my-local-system": {
      "transport": "http",
      "url": "https://api.tjcoder.com/mcp/hosts/SECRET_CAPABILITY"
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
silently write the secret URL to a file. Configure a local MCP client with
command `coder`, args
`["mcp", "--stdio", "--workspace-root", "/path/to/project"]`. Stdout contains
only protocol messages; login is not required for local stdio (provider-backed
tools may still need it). No local HTTP port is opened in either mode. An
internal authenticated HTTP handler is available/tested for loopback adapters,
but is not exposed as a CLI listener. Even `coder mcp tunnel` falls back to
local stdio if stdin or stdout is piped; no public session is created.

### Minimal relay architecture

One in-memory session store and two isolated Express routers on api.tjcoder.com:

- `POST /cli/mcp/sessions`: validates an active `ergo-cli` device-login key via
  existing `cliKeyAuth`; owner is derived from validated tenant/key records.
  Returns an internal session ID, expiry, a separate 256-bit relay credential,
  and the public MCP URL containing an independent random 256-bit capability.
  No caller-provided identity or upstream URL is trusted.
- `GET /cli/mcp/sessions/:id/connect`: outbound authenticated HTTPS SSE from
  the CLI, using only the relay credential. Server-to-host frames carry a
  random correlation ID and JSON-RPC body; no HTTP caller headers are forwarded.
- `POST /cli/mcp/sessions/:id/responses/:requestId`: bounded host responses,
  also using the relay credential. Single-use correlation, not an upstream proxy.
- `GET .../connect` again (same session ID and relay credential) replaces any
  existing or half-open stream; it never returns 409. Frames also carry an
  opaque `consumerId` (the consumer's `Mcp-Session-Id`), never caller IP/UA.
- `POST /cli/mcp/sessions/:id/heartbeat`: host heartbeat every 10 seconds; works
  with or without an attached stream. A new session holds a 30-second lease;
  after the first connect, renewals and stream drops extend the lease to
  `min(expiresAt, max(leaseUntil, now + 10 minutes))`, never beyond one hour.
- `DELETE /cli/mcp/sessions/:id`: relay credential revokes this session only.
- `POST /mcp/hosts/:capability`: public MCP JSON responses and 202 for notifications;
  the 43-character unpadded base64url path secret authenticates **every** request.
  An internal session ID or broad connector key is never a substitute.

These routes mount before legacy `/mcp`, global large body parsers and payload
logging. Existing legacy MCP authentication/tools and CLI login are unchanged.
Relay and URL capabilities are distinct and SHA-256 hashed in memory. The raw
URL capability is never used as a store key: a digest indexes the internal
session, then expiry/revocation are checked. A broad Ergo connector key, CLI
login key, query token or cookie cannot substitute for the secret URL path.
Browser-origin requests are rejected; no CORS permission is
granted. Only JSON-RPC bodies cross to the host, not spoofed owner headers.
No arbitrary target URLs, SSH forwards, tunnel cookies, nginx changes or Slack
middleware are involved. The CLI requires HTTPS and refuses redirects.

Limits: 1 MiB request/response messages, eight outstanding calls per session,
120 calls/minute per session, ten creates/minute per IP, 240 consumer requests/
guesses per minute per IP, three active sessions per validated owner key,
1,000 sessions globally. Execution is serialized to
match the interactive session's mutable tool state. Calls have a 115-second
host context and a 120-second relay deadline; tools must honor cancellation.

Persistence (0.9.265, server 1.1.752+): the **same capability URL survives
network changes, sleep and dropouts**. When the SSE stream ends or goes silent
for 35 seconds (server keepalives arrive every 10s), the relay backs off
(1s doubling to 30s) and GETs `connect` again with the same session ID and relay
credential. It never POSTs a new session or mints a new URL silently.
Heartbeats continue throughout. The server keeps the session for a 10-minute
reconnect grace after a drop; consumers receive 503 "host not connected" in the
meantime, and calls that were in flight at the drop fail rather than replay.
The relay DELETEs only on Ctrl-C/SIGTERM, `/mcp tunnel stop` or TUI exit. A 401
(session revoked/expired, e.g. server restart) or reaching the one-hour hard
maximum ends the tunnel with a clear message; start a new tunnel for a new link.
New calls authenticate against revocation/expiry before dispatch.

**The entire URL is a credential.** Anyone who has the link can call the toolset
until the session ends; do not paste it publicly. Browser Referer headers,
clipboard histories and reverse-proxy/access logs can leak capability URLs. The
app sends `Referrer-Policy: no-referrer`, rejects browser Origin requests and
bypasses payload/URL logging for these routes; it cannot control every upstream
proxy or a consuming client's referrer/logging behavior. Share only with trusted
consumers, and revoke by exiting the foreground command. Server 1.1.750+ is
required for this link-only flow; 0.9.258 used a separate Bearer credential.

### Deliberate v1 limits / future work

- Protocol `2024-11-05` tools-only JSON POST subset, not full modern Streamable
  HTTP. No MCP session headers, server-initiated consumer SSE, resources, prompts,
  sampling, OAuth discovery or per-consumer identities. Multiple consumers share
  the delegated session. Remote cancellation notifications are not implemented;
  disconnect/lease/deadline cancellation is best effort, not command rollback.
- The standalone command creates a separate foreground tool session and does
  not restore saved `/mcp add` clients. Interactive `/mcp tunnel` instead attaches
  to the live registry, including connected client tools. Do not import a tunnel
  back into its own session: recursive tool proxies are not a supported setup.
- In-memory, single-server store: a restart intentionally drops all publications.
  Multi-replica routing/shared leases and replay of in-flight calls are deferred.
- Lifetime and quotas are code constants initially. No persistent or unattended
  publications, no per-tool capability narrowing, no new OS sandbox.
