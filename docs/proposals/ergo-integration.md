# Design: Coder CLI ↔ Ergo AI Gateway (paid chat via ergo wallet)

## What already exists on the ergo side (verified in `ergo-ai-server`)

Ergo already implements ~90% of the platform we need. Nothing below is
speculation — file paths are from `/home/tj/ergo/ergo-ai-server`:

- **OpenAI-compatible endpoint** — `src/routes/gateway/openai.ts`
  - `POST /v1/chat/completions`, full OpenAI request/response schema.
  - Flow: auth → `preflightCheck` (402 on insufficient balance) →
    `UnifiedAIClient.chatCompletion()` (routes to Ollama / Azure / OpenAI …)
    → `settleUsage()` with **actual** token counts → reply unchanged.
- **Wallet debiting / pricing** — `src/services/metering.ts`
  - Rate card (`MODEL_RATE_CARD`, context-banded, margin-inclusive),
    tenant-tier multipliers, idempotent settle per `requestId`,
    fire-and-forget settle so metering outages never kill inference.
- **Scoped API keys (the "proven security framework")** —
  `src/models/ApiKey.ts` + `src/middleware/apiKeyAuth.ts`, plus a
  `scopedApiKeyAuth` variant keyed off the Mongo collection:
  - Scopes: `"ergo" | "ergo-chat" | "ergo-cli" | "ergo-chat-admin" | "ergo-sentinel" | "mcp"`.
    A `coder-cli` key would be minted with scope `ergo-cli` (chat completions
    only — no admin surface reachable).
  - Keys stored **SHA-256 hashed** (`keyHash`) with an 8-char `keyPrefix`;
    raw key shown once at creation.
  - Built-in lifecycle: `isActive`, `expiresAt`, `revokedAt/By`,
    `lastUsedAt`, `usageCount`, optional `allowedOrigins`/`allowedIPs`.
  - Dual-auth middleware available (`apiKeyOrJwtAuth`) if we ever want a
    dashboard-JWT user to drive the CLI without an API key.

## The CLI user experience (proposal)

```
$ coder login            # OAuth device flow OR paste an ergo API key
$ coder                  # chat — all completions billed to their ergo wallet
$ coder logout
```

Provider selection: `ergo` is the default (priority: ergo → ollama →
ollama-cloud); when not signed in the TUI prompts for `/login` rather than
falling back to ollama. An explicit `--provider` is saved and always wins. `/model` lists models from the ergo
rate card the tenant's key may use.

### Auth path options (choose one — recommend B first)

**A — OAuth 2.0 Authorization Code + PKCE with device flow.**
CLI opens `login.tjcoder.com` device page, user confirms code, CLI polls for
a token. *Problem:* ergo has no OAuth authorization server today; building one
correctly (clients, consent, scopes, token exchange, refresh rotation) is a
multi-week security-critical effort. Not needed for v1.

**B — ergo-issued scoped API key (recommended v1).** Leverages the existing,
proven framework above:
1. User visits the ergo dashboard (already authenticated there) → "API Keys" →
   creates a key with scope `ergo-cli`, optional IP allowlist / expiry.
2. `coder login` prompts for the key (hidden input), validates it against
   `GET /v1/models` (cheap probe), and stores it in
   `~/.local/share/coder/ergo-token` mode 0600 (same pattern as
   `resendResolveToken` in `internal/tools/resend.go`; generalize into an
   `internal/auth` helper).
3. CLI sends it as `Authorization: Bearer ergo_...` on every completion call.

Later v2 can add OAuth for the "one-click sign-in" polish without breaking B.

## CLI-side integration points (this repo)

1. **New provider client** — `internal/client/ergo.go`: an OpenAI-compatible
   client (ergo speaks the OpenAI schema natively — likely a thin wrapper over
   the existing gemini/ollama client shape, or a shared openai client).
   Base URL `https://api.tjcoder.com` (env override `ERGO_BASE_URL`,
   `ERGO_API_KEY`).
2. **Auth store** — `internal/auth/token.go`: resolve order
   `ERGO_API_KEY` env → persisted token file (0600). Never logged.
3. **Slash commands in TUI** — `/login`, `/logout` (clear token file),
   `/balance` (hit a small ergo `GET /v1/wallet/balance` endpoint — *this
   one endpoint may not exist yet; metering can expose remaining credits*).
4. **Config plumbing** — provider `ergo` in `/config`; model list fetched
   live from `GET /v1/models` if ergo serves it (or hardcode rate-card list).
5. **Error UX** — 402 → friendly "insufficient credits — top up at
   app.tjcoder.com/dashboard" with a `fetch`-able link; 401/403 →
   re-prompt `login`.
6. **Safety** — spend is always the user's own wallet; the CLI should surface
   `X-Ergo-Balance` style headers if metering can add them in responses, so
   the TUI can show a live balance ticker in the status bar.

## Ergo-side gaps to close (small)

- `GET /v1/models` passthrough? (optional; CLI can ship a static list)
- `GET /v1/wallet/balance` for `/balance` + live ticker.
- Confirm `scopedApiKeyAuth` checks Mongo `ApiKey` with scope `ergo-cli`
  on the gateway route (the current route uses legacy env-var API keys
  from `config.api.keys`; switching to `scopedApiKeyAuth` is presumably the
  intended production posture for per-user billing, since env keys have no
  tenantId → no wallet to debit).
- Dashboard UI to mint/revoke `ergo-cli` keys (may already exist — TBD).
- CORS not relevant for CLI (no browser), but `allowedOrigins` harmless.

## Billing flow (per request)

1. CLI sends `POST /v1/chat/completions` + Bearer key.
2. `preflightCheck` estimates cost (`max_tokens`) → 402 if short.
3. `UnifiedAIClient` routes to the real provider.
4. `settleUsage` debits exact prompt+completion tokens at the tenant's rate.
5. User's wallet ledger records the debit with an idempotent `requestId`.

## Risks / open questions

1. Streaming (`stream: true`) — does `UnifiedAIClient` stream token usage
   back? OpenAI's `stream_options.include_usage` must pass through, else
   settle has no actual counts for streamed replies. *Must verify.*
2. Key rotation/revocation UX in CLI (re-login is enough for v1).
3. Rate limiting on the gateway for a scoped key (per-key RPM).
4. Do we want the CLI to default to ergo when logged in (keep `ollama` as
   explicit fallback), or the inverse?

## Suggested first increment (PR-sized)

- `internal/client/ergo.go` (non-streaming), `internal/auth/token.go`,
  `/login`+`/logout` slash commands, 402/401 handling, tests with httptest.
- One ergo PR: add `GET /v1/wallet/balance`; ensure gateway route uses
  Mongo-backed `scopedApiKeyAuth` for scope `ergo-cli`.
EOF
