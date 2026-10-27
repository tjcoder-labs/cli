# Proposal: unified /model list + ergo tier prompts & client-side tool exposure

## Goal

The `/model` picker must enumerate **both** remote ergo tiers **and** the
available local Ollama models, so the user can always keep working against a
local model. When an ergo tier is selected, the TUI must send that tier's
**system prompt** in the completion payload and expose the correct **client-side
tool set** for the tier. When a local Ollama model is selected, everything
behaves exactly as today.

## Verified facts (from `ergo-ai-server` @ `src/routes/v1.ts`, `ergoTierPins.ts`)

- `GET /v1/models` returns exactly three public tiers: `ergo`, `ergo-lite`,
  `ergo-genius` (plus per-1M-token pricing). No Ollama models are served here.
- `POST /v1/chat/completions` routes the tier id to an xAI pin:
  - `ergo`        → `xai/grok-4.3` (fallback `xai/grok-4.20-0309-non-reasoning`)
  - `ergo-lite`   → `xai/grok-4.20-0309-non-reasoning` (fallback `xai/grok-build-0.1`)
  - `ergo-genius` → `xai/grok-4.7` (fallback `xai/grok-4.3`)
- The server does **not** inject any system prompt on `/v1`; it forwards the
  OpenAI `messages[]` verbatim. The tier prompts (`ergo.md`, `ergo.lite.md`,
  `ergo.genius.md`) are server-side agent templates, **not** exposed over `/v1`.
- Unknown models → `404 model_not_found` with the available list in the message
  (already handled by `client.ErgoModelNotFoundError`).

## Implications

1. **Prompt ownership is client-side.** The CLI must carry (or fetch) the ergo
   tier prompt and prepend it as a `system` message. The server will not do it.
2. **Tool exposure is client-side.** The ergo tiers' `toolNames`
   (`manage_task`, `manage_engram`, `run_agent`, …) are server-side tools; the
   CLI's equivalents are the TUI registry tools. The TUI decides which registry
   tools each ergo tier may invoke, and sends those as `tools[]` in the payload.
3. **`/model` needs a combined source**, not the single `a.provider.ListModels()`.

## Proposed design

### A. Combined model enumeration

Introduce a `client.ModelCatalog` (or an `App`-level aggregator) that produces a
single, deduplicated, origin-tagged list:

```
[]client.ModelInfo  // existing shape, extended with Origin + Tier metadata
```

- ergo tiers come from `Ergo.ListModels()` (or `ErgoFallbackModels()` when
  unauthenticated), tagged `Origin: "ergo"`.
- local Ollama models come from a **separate local `Ollama` client**
  (`http://localhost:11434`, reachable probe via existing `ollamaReachable`),
  tagged `Origin: "ollama"`.
- `/model` renders a grouped list: an "ergo" section (ergo / ergo-lite /
  ergo-genius with descriptions) followed by a "local ollama" section.

Selection is recorded per-origin so the prefs stay coherent:
- selecting an ergo tier sets the ergo provider + tier (and its prompt/tools);
- selecting a local Ollama tag switches the active provider back to local ollama.

### B. Per-tier prompt & tool mapping (client-side)

A new `internal/ergo` package (or extension of `internal/client`) holds, per
tier id, a small mapping:

```go
type ErgoTier struct {
    ID          string   // "ergo" | "ergo-lite" | "ergo-genius"
    Label       string
    Description string
    SystemPrompt string  // client-side copy of the tier's prompt
    ToolNames   []string // client registry tools this tier may invoke
}
```

- The prompt is sent as a `system` message in `ChatRequest.Messages` (already
  supported — `Ergo.Chat` maps `system` through to the wire).
- `ToolNames` feeds the existing tool-registry filter (`Runner.Registry.Definitions`).
- The ergo agent (default `software-engineer`) prompt is **replaced** by the tier
  prompt when an ergo tier is active; repository `AGENTS.md`/`CODER.md` injection
  continues to be appended as today.

### C. Where the prompt text comes from (decision needed)

Three options, in order of preference:

1. **Bundle a curated copy in the CLI** (fastest, no server change). We author a
   lean ergo-tier prompt per tier (a CLI-appropriate adaptation of `ergo.md`,
   `ergo.lite.md`, `ergo.genius.md`), not the full server templates which assume
   server-side `{{collaboration}}`/`{{tasks}}` rendering and `run_agent` tooling.
2. **Add `GET /v1/models/{id}` (or a `prompt` field on `/v1/models`)** so the CLI
   fetches the live prompt. More correct long-term, requires an ergo-side PR.
3. **Hybrid**: bundle a fallback prompt now; upgrade to server-fetched prompts
   once the endpoint exists.

Recommend **option 3** (bundle now, fetch later behind a feature flag).

### D. Provider switching semantics

Today the active provider is fixed at startup. The new flow makes `/model` the
primary switch: choosing a local Ollama tag swaps `a.provider` to a local
`Ollama` client and re-loads models; choosing an ergo tier swaps to `Ergo` and
sets the tier prompt/tools. `--provider` on the command line continues to pin
the *default* section shown first, but no longer locks the user out of the other
origin.

## Open questions to confirm before implementation

1. Prompt source: bundle now + fetch later (recommended), or wait for a server
   `/v1/models/{id}` prompt endpoint first?
2. Tool mapping: which client registry tools should each ergo tier expose?
   (Proposal: ergo = full current set; ergo-lite = read/search only, no writes;
   ergo-genius = full set + extra analysis tools. Confirm.)
3. Should selecting a local model while signed in to ergo still keep the ergo
   wallet available for `/balance`, or fully detach? (Recommend: keep balance
   viewable; only the chat provider swaps.)
