# Coder CLI — Development Guide

This file guides agents and contributors working on the Coder CLI codebase.

## Project Overview

Coder CLI is an open-source, AI-powered terminal coding assistant. It pairs a conversational agent with real developer tools in a keyboard-driven TUI, with a live cognition pane for streaming model reasoning. It defaults to local Ollama models for privacy and optionally supports cloud providers (Google Gemini).

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
| `internal/agent/` | Agent definitions and orchestration |
| `internal/browser/` | CDP client, tab registry, Chrome launcher |
| `internal/client/` | LLM provider clients (ollama, gemini) |
| `internal/context/` | Runtime context + environment templating |
| `internal/highlight/` | Syntax/markdown highlighting |
| `internal/session/` | Session + preference persistence |
| `internal/tooling/` | Runner, tool parser, and registry |
| `internal/tools/` | Tool implementations |
| `internal/tui/` | Terminal UI |
| `npm/coder-cli/` | npm distribution wrapper |

## Development

```bash
make build     # build ./bin/coder
make run       # build and run the TUI
make fmt       # gofmt the tree
go test ./...  # run the unit test suite
bash TESTS.sh  # end-to-end harness against a live provider
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/your-feature`)
3. Commit with clear messages and `gofmt`-clean code
4. Push and open a pull request against `main`

See [CONTRIBUTING.md](./CONTRIBUTING.md) for details.

## Agent Behavior Directives

Agents running inside the TUI follow directives in [CODER.md](./CODER.md). These cover presentation control, task tracking, and interaction patterns.