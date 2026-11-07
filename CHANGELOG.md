# Changelog

<!-- Generated from release-notes/releases.json by `go run ./cmd/releasenotes`. Do not edit by hand. -->

All notable changes to Coder CLI. The source of truth is [release-notes/releases.json](release-notes/releases.json).

## 0.9.260 — 2026-11-07

**Ledger count skips expired entries.** manage_ledger count now totals every live ledger entry and ignores expired ones, instead of asking ListRecent for zero rows.

### Fixed

- Ledger count walks the full recent list, skips entries whose TTL has elapsed, and no longer calls ListRecent(0), which returned an empty set.

## 0.9.259 — 2026-11-07

**One secret link for remote MCP access.** The ephemeral MCP URL itself now grants access; consuming agents no longer need a separate Authorization header.

### Changed

- coder mcp prints one secret capability URL plus expiry. A consuming agent can initialize, list and call the local tools using that link alone.

### Security

- The URL contains a random 256-bit capability, hashed server-side and bound to session expiry/revocation. Relay credentials remain separate; capability guesses are rate-limited.

### Known limits

- Treat the entire URL as a secret. Do not post it publicly; proxy access logs and browser referrers can expose it. Anyone holding it can use the published tools until exit or expiry.
- Requires api.tjcoder.com host-session server 1.1.750 or newer. No Slack integration.

## 0.9.258 — 2026-11-07

**Ephemeral public MCP host sessions.** Publish Coder’s local interactive toolset to a remote MCP consumer with a short-lived, authenticated outbound relay.

### Added

- coder mcp publishes the default interactive tool registry, including files, shell, browser, tasks and memories. coder mcp --stdio serves local newline-delimited JSON-RPC without a relay.
- Public sessions use an outbound HTTPS stream, distinct host and invocation capabilities, 10-second heartbeats, 30-second leases and a one-hour hard expiry.

### Security

- Session-bound Bearer credentials, no public local listener or credential persistence, bounded messages/concurrency and disconnect revocation. Existing workspace rules and sudo denial remain.

### Known limits

- No Slack integration or re-export of Ergo server tools. Publishing deliberately delegates local tool use to the capability holder; this is not an OS sandbox.
- Requires the new api.tjcoder.com host-session routes. Resources, prompts, sampling, TUI attachment, imported MCP tool re-export, OAuth discovery and newer MCP revisions are not included.

## 0.9.257 — 2026-11-07

**Local MCP servers and connection visibility.** Complete the MCP client with stdio support, persistent argv configuration, tool listings and connection status.

### Added

- Connect local MCP servers with /mcp add &lt;name&gt; &lt;command&gt; [args...], restore them from config and stop their processes on removal or shutdown. (`/mcp`)
- /mcp list includes discovered tools and descriptions; /mcp status reports transport, target, last-observed connection state, tool count and errors.

### Fixed

- MCP initialization reports the running CLI version and initialized notifications omit the JSON-RPC id.

### Security

- Stdio uses newline-delimited JSON-RPC and direct argv execution with a restricted inherited environment; no shell or implicit credential forwarding.

### Known limits

- MCP_SPEC.md documents a proposed authenticated, ephemeral coder mcp gateway through api.tjcoder.com, based on ergo-ai-server tunnel/auth code. The tunnel, new routes and coder mcp subcommand are not implemented.

## 0.9.256 — 2026-11-06

**Interactive browser relay built into Coder.** Interactive Coder registers its own executable and keeps the local browser relay running until the TUI exits.

### Added

- Interactive native browser relay and coder browser-host register/unregister; no separate host binary required.

### Security

- Exact extension origin validation, separate owner-only Unix command and extension sockets, and session-bound cleanup; CDP remains unchanged.

### Known limits

- Load the Coder Browser Bridge extension and reconnect after starting interactive coder. No automatic browser launch, public listener, or stealth-to-CDP fallback.
- One interactive browser relay per OS user. Same-user processes remain trusted; Windows native registration is unsupported.

## 0.9.255 — 2026-11-06

**Fail closed for private browser tabs.** Private-window requests must explicitly use the stealth extension transport.

### Security

- Reject incognito requests on raw CDP instead of risking opening a private URL in a normal tab.

### Known limits

- Coder Browser Bridge extension remains version 0.9.254 and is compatible with CLI 0.9.255.

## 0.9.254 — 2026-11-06

**Local stealth browser extension bridge.** Opt into a local native messaging bridge while preserving the default CDP path.

### Added

- MV3 Coder Browser Bridge, native host registration, private Unix-socket transport, tabs, navigation, isolated evaluation, DOM inspection and screenshots.
- Stable extension ID, reproducible downloadable zip and install guide.

### Security

- Exact extension-origin allowlist and owner-only local sockets; no telemetry or public listener.

### Known limits

- Native cookies, storage, trusted input and advanced CDP are deferred. Chrome 135+ and Allow User Scripts are required for evaluate.

## 0.9.253 — 2026-11-03

**Sudo-elevated binary updates and more engineering tools.** Automatic and manual binary updates can now escalate to sudo when the install directory is root-owned, and the software-engineer agent gains the highlight_code, adb_shell, manage_ledger, manage_model, research_note, and interaction_log tools.

### Added

- Binary updates that fail with a permission error now offer sudo elevation and retry the install, so root-owned install directories (e.g. /usr/local/bin on macOS) update without reinstalling. (`/update`)
- The software-engineer agent now has access to the highlight_code, adb_shell, manage_ledger, manage_model, research_note, and interaction_log tools.

## 0.9.252 — 2026-11-03

**Generalized event ledger tool.** A new manage_ledger tool replaces the social-only interaction log with a schema-free event ledger for tracking arbitrary events and enforcing deduplication, plus a --session flag for coder ask so separate conversations can target their own session files.

### Added

- New manage_ledger tool records, checks, and lists arbitrary events keyed by free-form namespace:key with optional JSON payloads and TTL.
- coder ask now accepts --session &lt;path&gt; to load and save history from an explicit session file, enabling subsessions.

## 0.9.251 — 2026-11-03

**Wallet top-up flow, reminders, and TUI polish.** This release adds a Stripe top-up path directly from the out-of-credits error card and a wallet pane with live balance polling, plus reminder support in manage_items, git-branch context, and a batch of TUI rendering fixes.

### Added

- Insufficient-credits (402) error cards now offer a "Top up now (Stripe)" option that opens the wallet pane.
- The wallet pane polls your balance every few seconds while open, so a completed payment shows up without reopening.
- manage_items now supports reminders (list, get, create, update, delete), backed by the on-disk reminders store.
- The execution-context block now names the current git branch next to the working directory.

### Fixed

- Deleting an installed reminder also strips its crontab entry instead of leaving a stale cron line behind.
- Canvas code wrapping, user-bubble colors, and input/context spacing all render cleanly.

## 0.9.250 — 2026-11-02

**TUI header shows @tjcoder/cli.** The TUI header (and version/product identity) now reads @tjcoder/cli instead of @tjcoder, matching the published npm package name.

### Changed

- TUI header title now shows @tjcoder/cli instead of @tjcoder.

## 0.9.249 — 2026-11-02

**Out-of-credits error card offers Stripe top-up.** When the ergo gateway returns a 402 (insufficient credits), the error card now includes a second numbered option: "Top up now (Stripe)". Selecting it opens the existing wallet pane (balance + numbered Stripe packs) so the user can top up and retry without leaving the conversation.

### Added

- Insufficient-credits error card adds a "Top up now (Stripe)" option that launches the wallet pane.

## 0.9.248 — 2026-11-02

**manage_items can list, delete and reference reminders.** The reminder tracker is now registered in both the TUI and headless modes, so manage_items can list, reference and delete reminders uniformly with tasks and memories. Deleting a reminder now also removes its crontab line when it was installed.

### Added

- manage_items now supports the reminder type (list, get, create, update, delete), backed by the on-disk reminders.json.

### Fixed

- Deleting a reminder now also strips its crontab line when the entry was installed, instead of leaving a stale cron entry behind.
- Fixed two TUI build errors (a palette-field typo and an out-of-scope timezone reference) that prevented go build ./... from completing.

## 0.9.240 – 0.9.247 — 2026-11-02

**Formatter-style canvas wrapping; activity no longer bleeds into it.** Canvas lines now wrap the way a formatter would: breaks land at argument commas and operators instead of the last space, continued rows hang-indent under the original leading whitespace, and the line-number gutter is capped so long files keep their columns. Separately, the activity stream no longer writes into the canvas or get saved as file contents, and prompt cards close with a single blank padded row instead of two.

### Added

- Context blocks name the active provider next to the model (e.g. Provider: ollama (local)), so the agent knows where it is running.
- Auto-compact: /config can set a context-window percentage that triggers automatic compaction after a turn.
- Ctrl+E focuses the right-hand panel (canvas/activity/tasks) for scrolling; Esc returns focus to input.
- Context bar now shows the current git branch next to the cwd (e.g. /path [main]).

### Changed

- Canvas code wraps at commas and operators with a hanging indent, so narrow panes keep code readable instead of breaking mid-string.
- Think mode is forwarded to the ergo gateway via the X-Ergo-Think header (auto/on/off).

### Fixed

- Activity output no longer runs into the canvas; switching back to the activity panel restores the log.
- Saving a session while the canvas is open no longer stores the displayed file as the activity log.
- The canvas no longer washes a highlighted line range in purple; code keeps its syntax colours and only the gutter marker shows the range.
- Prompt cards (ask_user, sudo, password, tool budget, error/retry) close with a single blank padded row at the bottom, so the accent rule keeps one line of padding and no longer doubles up.
- The focused (live) prompt card now matches its answered copy's spacing: two blank rows of padding above and below, instead of one above and three below.
- User message bubbles now use pure white text on the purple fill instead of the off-white body tint.
- The chat input surface now shows a single blank row between the typed text and the context strip below it, instead of two.
- The chat input surface now shows a single blank row below the context strip, instead of two.
- The [execution-context] block now names the current git branch (e.g. Git: main clean) with a {{git}} token available to custom /environment templates.
- A model chosen on the tjcoder gateway now persists across restarts; it was saved under one prefs key and read from another.
- User message bubbles now hug short text with two cells of padding on each side, and fill the full pane width when the message wraps.

## 0.9.234 — 2026-11-01

**manage_model validates against the three ergo tiers.** For the tjcoder gateway, manage_model now validates an explicit model against exactly tjcoder/ergo-flash:cloud, tjcoder/ergo-pro:cloud, and tjcoder/ergo-genius:cloud, rejecting anything else.

### Changed

- manage_model accepts only the three ergo tiers (flash/pro/genius) on the tjcoder gateway.

## 0.9.233 — 2026-11-01

**manage_model validates against the full catalog.** manage_model now checks an explicit model against the full provider catalog (ergo's unified ListAllModels, or the provider's /tags) and rejects unknown models. The TUI reflects the change via /model and persists it like a manual pick.

### Changed

- manage_model rejects model names not served by the provider instead of switching to a non-existent model.
- manage_model enumerates ergo's full unified catalog (tiers plus xai/, ollama/, google/, …) when validating a model.

## 0.9.232 — 2026-11-01

**manage_model works with any provider + /capture screenshot shorthand.** manage_model now switches models on any provider (Ollama local, Ollama Cloud, Gemini, ergo tiers, OpenAI-compatible gateways) via an explicit model name or ergo tier direction. Added /capture screenshot as a shorthand for /screenshot and a first-pass live frame sampler for /capture start|stop.

### Added

- manage_model switches models on any provider (not just ergo tiers), via an explicit model name.
- /capture screenshot shorthand renders a single frame like /screenshot.
- /capture start|stop records live frames to a numbered PNG sequence (video encoding is a follow-up).

### Changed

- manage_model persists think mode and auto-tier alongside the model choice.

## 0.9.231 — 2026-11-01

**Console screenshots: /screenshot and /capture.** Added /screenshot to render a high-resolution console screenshot to PNG via an embedded Go font, plus a /capture start|stop stub for recording. The output directory is configurable (default ~/Downloads).

### Added

- New /screenshot command renders the console to a high-resolution PNG using an embedded Go font.
- New /capture start|stop command (status-marker stub) for recording console frames.
- Screenshot output directory configurable via /config (captureDir, default ~/Downloads).

## 0.9.230 — 2026-11-01

**Background job status in prompt + terminal notifications.** Background jobs are now injected into the system prompt as a status table, and terminal states (completed/failed) append a system message so the model is informed without a tool call.

### Added

- Background job status block injected into system prompt on every turn.
- System message appended to history when a background job completes or fails.

## 0.9.229 — 2026-10-31

**manage_model tool: dynamic ergo tier switching.** Added /manage_model tool for on-the-fly switching between tjcoder/ergo-flash, -pro, and -genius tiers, plus thinking mode control. Also included client identification headers for improved telemetry.

### Added

- New manage_model tool lets agents and users switch ergo tiers (flash↔pro↔genius) and control reasoning (think on/off/auto) without restarting. Persists choice across sessions.
- ergo gateway requests now include X-Ergo-Client, X-Ergo-Client-Version, and X-Ergo-Interface headers for better telemetry and debugging.

### Changed

- Updated provider priority in factory: ergo is now first in ProviderOrder (['ergo', 'ollama', 'ollama-cloud']).
  Matches the documented default provider order and resolves unknown-provider guidance accordingly.

### Fixed

- Corrected NormalizeErgoModel to properly map legacy ids like ergo-lite → tjcoder/ergo-flash:cloud and handle tjcoder/* prefixes consistently.
  Fixes model resolution for saved legacy aliases and explicit tjcoder/* inputs.

## 0.9.227 — 2026-10-31

**Transcript auto-scrolls to bottom.** The conversation transcript now automatically scrolls to the latest content on load, when new messages arrive, and during streaming replies.

### Added

- Transcript scrolls to end on startup after loading persisted messages from the previous session.
  Previously the transcript stayed at the top on reload, requiring manual scroll to see the latest messages.
- User messages, assistant replies, steered messages, and streaming commentary all keep the transcript at the bottom automatically.
