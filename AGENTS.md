# Coder CLI — Development Guide

## ⚠️ REQUIRED: Release notes on every change

**Every commit that bumps `package.json`'s version MUST add or update the matching release-notes entry in the same commit.** `go test ./...` fails otherwise.

- **Source of truth:** [`release-notes/releases.json`](release-notes/releases.json) — one file, newest entry first. Format: [`release-notes/schema.json`](release-notes/schema.json) (JSON Schema).
- **Entry:** `version` (semver, no `v`, equal to `package.json`), `date` (`YYYY-MM-DD`, America/Denver), `title`, `summary`, and `changes` grouped by `added`, `changed`, `deprecated`, `removed`, `fixed`, `security`. Each item has `text` (one plain-language line for users) and optional `details`, `commands` (slash commands touched, e.g. `"/add"`), `breaking: true`, `highlight: true`. Optional per entry: `covers_from` (first version of a batch), `min_upgrade_from`, `requires_restart`, `notes` (known limits).
- **New version:** add an entry at the top. Bumping again before a release? Change the top entry's `version` and set `covers_from` to the earlier version.
- **Regenerate the changelog:** `go run ./cmd/releasenotes` (or `make release-notes`) rewrites `CHANGELOG.md`. Never edit `CHANGELOG.md` by hand. `go run ./cmd/releasenotes -check` verifies it.
- **Enforced by:** `go test ./internal/releasenotes` (`enforce_test.go`). It checks that `package.json`'s version is the newest entry, that entries are sorted, unique and valid against the schema, that `schema.json` matches the Go types, and that `CHANGELOG.md` is current.
- **Machine-readable on purpose:** the update system will read this file, both the copy embedded in the binary and the copy on `main`, to show "what's new". Keep it valid JSON. Make **only additive** schema changes: never rename, remove or retype a field or category. When you add one, update `schema.json`, the Go types in `internal/releasenotes`, and bump `schema_version`.

---

This file guides agents and contributors working on the Coder CLI codebase.

## Project Overview

Coder CLI is an open-source, AI-powered terminal coding assistant. It pairs a conversational agent with real developer tools in a keyboard-driven TUI, with a live cognition pane for streaming model reasoning. Its default provider is the ergo gateway (sign in with `/login`). Without a chosen provider it tries ergo first, then local Ollama, then Ollama Cloud. Google Gemini is also supported.

## Architecture

- **Context injection** — `internal/context/` composes runtime environment, repository instructions, and memories into the system prompt.
- **Tool invocation** — `internal/tooling/` implements the runner, tool parser (native + fenced-JSON fallback), and registry. `internal/tools/` contains the 31+ tool implementations.
- **Agent loop** — the runner iterates tool calls up to a budget, then produces a checkpoint response.
- **Session management** — `internal/session/` persists conversation, tasks, and memories per workspace under `.ergo-cli-go/`.
- **TUI** — `internal/tui/` implements the terminal UI: conversation transcript, cognition pane, activity log, slash commands, markdown table rendering, and scheduler.
- **Browser bridge** — `internal/browser/` provides CDP client, tab registry, Chrome launcher, and interaction ledger.

## Key Directories

| Path | Purpose |
|---|---|
| `cmd/coder/` | CLI entry point (interactive + headless) |
| `cmd/releasenotes/` | Validates release notes and regenerates `CHANGELOG.md` |
| `internal/agent/` | Agent definitions and orchestration |
| `internal/browser/` | CDP client, tab registry, Chrome launcher |
| `internal/client/` | LLM provider clients (ergo — the default, ollama, ollama-cloud, gemini) |
| `internal/context/` | Runtime context + environment templating |
| `internal/highlight/` | Syntax/markdown highlighting |
| `internal/releasenotes/` | Parses the embedded release notes (`Latest`, `ForVersion`, `Since`) and enforces the rules above |
| `internal/session/` | Session + preference persistence |
| `internal/tooling/` | Runner, tool parser, and registry |
| `internal/tools/` | Tool implementations |
| `internal/tui/` | Terminal UI |
| `internal/update/` | Update check, `/update` and `coder update` |
| `npm/coder-cli/` | npm distribution wrapper |
| `release-notes/` | **Release notes source of truth** (`releases.json`, `schema.json`) |

## Development

```bash
make build     # build ./bin/coder
make run       # build and run the TUI
make fmt       # gofmt the tree
go test ./...  # run the unit test suite
make release-notes  # validate release notes and regenerate CHANGELOG.md
bash TESTS.sh  # end-to-end harness against a live provider
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/your-feature`)
3. Commit with clear messages and `gofmt`-clean code, bumping `package.json` and adding the release-notes entry (see the top of this file)
4. Push and open a pull request against `main`

See [CONTRIBUTING.md](./CONTRIBUTING.md) for details.

## Agent Behavior Directives

Agents running inside the TUI follow directives in [CODER.md](./CODER.md). These cover presentation control, task tracking, and interaction patterns.