# MCP integration

## Implemented client (0.9.257)

`coder` is an outbound MCP **client**. It is not yet an MCP server and there is
no `coder mcp` subcommand or public tunnel.

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

## Proposed `coder mcp` ephemeral gateway (design only)

**Nothing in this section is implemented in this release.** No new CLI command,
public listener, tunnel transport, server route or connector registration ships.

### Evidence from ergo-ai-server

Inspected `tjcoder-labs/ergo-ai-server` main at
`ce2dfcb3bea69d9b946163a7b5d4c20bb6ca0427`:

1. `scripts/ensure-api-tunnel.sh` runs foreground `ssh -N -R
   1986:localhost:1984 tj@tjcoder.com`, with `ExitOnForwardFailure`, 60-second
   keepalives and three missed probes. The script manager owns restart/lifetime.
   `deploy/tunnels.sh` is a different, fixed MongoDB **local** forwarding script,
   not a per-user MCP publication API. Existing SSH forwarding is infrastructure,
   not an authenticated user-scoped gateway session.
2. `src/services/proxyActivator.ts` enables a development reverse tunnel through
   `POST /configure/proxy` (or remote localhost curl via SSH) and disables it at
   shutdown. `src/routes/configure.ts` authorizes configured API keys and calls
   the singleton `src/utils/proxyManager.ts`. Critically,
   `src/middleware/proxy.ts` currently forwards **only Slack paths**, not `/mcp`.
   This global dev switch must not be reused for user MCP sessions or change
   production Slack routing/cron behavior.
3. `src/services/nginxRouter.ts` delegates subdomain add/remove to
   `/home/tj/scripts/nginx-update-subdomain.sh` and
   `/home/tj/scripts/nginx-remove-subdomain.sh`. Their implementations and any
   generic dynamic-port tunnel allocator are not present in the inspected tree;
   their authorization/lifetime guarantees are unknown. Do not assume a hidden
   API exists or automatically invoke those privileged scripts.
4. `src/routes/auth/tunnel.ts` provides `GET /auth/tunnel/verify` for nginx
   cookie authentication and `POST /auth/tunnel/session` for **JWT** exchange.
   Verification also checks the primary-auth-email policy. `src/utils/tunnelAuth.ts`
   signs a `tunnel_session` cookie with a 30-day age limit. That browser cookie
   lifetime is not suitable as a short-lived MCP capability.
5. The tracked nginx layout differs from legacy dynamic-port comments:
   `deploy/nginx/conf.d/ergo-workspaces.conf` points `ergo_api` to loopback 1989;
   `deploy/nginx/sites-available/api.tjcoder.com.conf` forwards API traffic to it,
   leaves authentication to Express, disables proxy buffering and has a
   3600-second read timeout. `deploy/nginx/snippets/ergo-auth-gate.conf` gates
   browser hosts via `/auth/dashboard/verify`, not `/auth/tunnel/verify`.
   Deployment-specific routing must be verified before rollout; do not hardcode
   the legacy 1986 destination or apply browser-cookie redirects to MCP clients.
6. `src/routes/cli/auth.ts` implements `POST /cli/auth/start`, `/poll`, `/confirm`
   and `/logout`, with browser verification/approval routes. Login mints an
   `ergo-cli` scoped key (default 180 days); the device challenge defaults to
   15 minutes. It does **not** return the JWT accepted by tunnel-cookie exchange.
   `src/routes/cli/share.ts` explicitly documents the narrow CLI-key surface:
   `/v1`, CLI logout, and file-share creation. A CLI key cannot be assumed to
   authorize `/mcp`, Socket.IO or new tunnel endpoints without server changes.
7. `src/index.ts` mounts `mcpLimiter` and `src/routes/mcp.ts` at `/mcp`. That
   existing GET/POST endpoint executes the **server's** tools, accepts legacy
   configured keys or scoped MongoDB `mcp` keys (with allowed-IP checks), and
   does not reverse-proxy arbitrary CLI hosts. Leave existing Ergo connector
   behavior unchanged.

### Proposed flow and boundaries

1. After `coder login`, explicit `coder mcp` opts into exposing an approved
   snapshot of the **local in-process** tool registry. This is the inverse of
   today's outbound client: the CLI implements the MCP server handshake,
   `tools/list`, and `tools/call`, dispatching through the same execution policy,
   workspace boundary, approval gates and result/error handling as the TUI.
   Default exposure should be an allowlist; destructive tools must not gain
   unattended approval. Browser-bridge tools require a separate opt-in and the
   same extension-origin/owner checks. Avoid automatic re-export of connected
   upstream MCP tools and proxy loops.
2. Introduce a narrowly scoped, owner-authenticated **control plane** on
   api.tjcoder.com. Illustrative **new, proposed** routes:
   `POST /cli/mcp/sessions`, `DELETE /cli/mcp/sessions/:id`, and an authenticated
   outbound relay upgrade under `/cli/mcp/sessions/:id/connect`. Extend CLI-key
   middleware only for these routes; derive owner/workspace/key identity from
   validated server-side records, never caller-provided identity headers.
3. Creation returns a random session ID, short expiry, a session-bound relay
   credential and an invocation capability; keep relay and caller credentials
   distinct and hashed at rest. Proposed public MCP URL:
   `https://api.tjcoder.com/mcp/hosts/:sessionId`. This is a **new** gateway route,
   not an existing API. Its middleware must be mounted/ordered separately from
   the current `/mcp` legacy-key handler. The gateway authenticates **every**
   caller before dispatch, checking capability audience, owner, workspace,
   allowed tools, session binding, expiry and revocation. An existing broad
   Ergo connector key alone must not grant access to every published host.
4. Prefer an outbound TLS relay from the CLI to the gateway (authenticated
   WebSocket or equivalently multiplexed stream). Reuse the infrastructure
   principle of reverse forwarding and foreground process ownership, **not**
   shared infrastructure SSH accounts, arbitrary user-supplied upstream URLs,
   singleton proxy configuration or global nginx changes. No inbound host
   firewall changes or public local TCP listener. The relay carries bounded
   JSON-RPC frames with per-call IDs, per-client MCP sessions, backpressure,
   deadlines and cancellation. Do not forward gateway credentials into tool
   arguments or raw local requests. If a local adapter socket is needed, bind
   an owner-only Unix socket in an owner-only directory (or equivalent ACL);
   loopback TCP alone is not owner isolation. Never fall back to `0.0.0.0`, an
   unauthenticated listener, or an open SSH forward on relay failure.
5. Proposed heartbeat every 10 seconds, 30-second renewable lease and a hard
   maximum session age (initially one hour); constants require server review.
   Foreground `coder mcp` or an explicitly enabled TUI-owned session holds the
   relay. Exit/disconnect immediately makes the endpoint unusable; best-effort
   DELETE revokes it, and lease expiry handles crashes/network loss. Kill
   in-flight calls where possible and deny new requests after revocation.
   No automatic durable endpoint or restart that silently republishes it.
   Reconnect requires a fresh session/capability. Server restart invalidates
   sessions unless a shared, correctly revoked lease store is deliberately added.
6. An agent anywhere on the internet can reach the TLS endpoint **only with the
   scoped invocation credential** issued by the owner. Show endpoint and expiry
   locally and provide connector configuration with a Bearer header, not query
   tokens or shared browser cookies. In the Ergo connector environment, add a
   temporary, separately authenticated MCP host integration using that URL and
   credential; do not replace the existing Ergo V2 `/mcp` server or borrow its
   privileged key. Connector provisioning/secret-storage support is not established
   by the inspected server sources and requires a separate integration decision.

### Implementation gates and tests for the future change

- Agree on new gateway routes, owner/workspace policy and narrow CLI-key scopes;
  verify deployed nginx/upgrade wiring rather than assuming legacy SSH scripts.
- Bound request sizes/concurrency, validate Host/Origin where relevant, strip
  spoofed identity headers, reject arbitrary target URLs (SSRF), and log only
  session/tool metadata, not secrets or tool payloads. Rate-limit create and call.
- Test unauthorized/wrong-owner/expired/revoked calls, lease loss, SIGTERM/crash,
  concurrent MCP clients, duplicate/replayed frames, cancellation/backpressure,
  gateway restart and browser-bridge opt-in. There must be no usable route after
  shutdown, no cross-owner tool visibility, and no approval bypass.
- Keep resources/prompts/sampling, unattended all-tools access and permanent
  tunnels out of the initial scope. This release is client implementation plus
  this design only.
