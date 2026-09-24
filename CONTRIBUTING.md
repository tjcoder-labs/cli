# Contributing to Coder CLI

Thank you for your interest in contributing to Coder CLI! We welcome bug reports, documentation improvements, and code contributions.

## How to contribute

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/your-feature`
3. Make your changes and run the test suite: `bash TESTS.sh`
4. Commit with a descriptive message and push to your fork
5. Open a pull request against the `main` branch

## Coding style

- Go code should be formatted with `gofmt` (run `make fmt`) before committing.
- Keep commits focused and atomic.
- Follow existing patterns in the codebase — see `CODER.md` for agent behavior directives and `AGENTS.md` for the architecture overview.

## Development setup

```bash
git clone https://github.com/tjcoder-labs/cli.git
cd cli
make build          # produces ./bin/coder
make run            # build and run the TUI
go test ./...       # run unit tests
bash TESTS.sh       # end-to-end harness (requires a live Ollama or Gemini provider)
```

## Reporting bugs

Open an issue describing the problem with steps to reproduce, logs, and environment details (OS, terminal, model, provider).

## Security

If you discover a security vulnerability, please email [tj@tjcoder.com](mailto:tj@tjcoder.com) instead of opening a public issue.