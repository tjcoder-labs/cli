# Changelog

<!-- Generated from release-notes/releases.json by `go run ./cmd/releasenotes`. Do not edit by hand. -->

All notable changes to Coder CLI. The source of truth is [release-notes/releases.json](release-notes/releases.json).

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
