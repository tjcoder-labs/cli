# @tj/coder-cli

> Terminal-first coding CLI by TJ Coder / AI Labs.

`coder` is a Go TUI that talks to local Ollama models (and Google Gemini
when configured). This package is the npm distribution: it downloads the
right prebuilt binary for your platform on `npm install`, so you don't
need a Go toolchain.

## Install

```bash
npm install -g @tj/coder-cli
```

That's it. After install, `coder` is on your `PATH`:

```bash
coder --version
```

### Prerequisites

`coder` itself is a self-contained Go binary — the only requirement is
`node >= 18` (to run the postinstall downloader). To actually use the
TUI you'll also need:

- An [Ollama](https://ollama.com) server running locally
  (default: `http://localhost:11434`)
- At least one model pulled (`ollama pull qwen2.5:32b-ctx32k` or
  similar)

## Updating

```bash
npm update -g @tj/coder-cli
```

The `postinstall` hook is idempotent and won't redownload a binary
that's already present.

## Uninstalling

```bash
npm uninstall -g @tj/coder-cli
```

The `preuninstall` hook removes the cached binary.

## Usage

```bash
coder                              # launch the TUI
coder --version                    # print version and exit
coder --provider ollama --host http://localhost:11434
coder --provider gemini --gemini-api-key $GEMINI_API_KEY
coder --provider gemini models     # list available Gemini models
coder --workspace-root ~/code/foo
coder --model qwen2.5:32b-ctx32k
coder --timeout 30m
```

### Environment variables

| Var | Default | Purpose |
|---|---|---|
| `GEMINI_API_KEY` | _(required for gemini)_ | API key for Google's Gemini API |
| `ERGO_HTTP_TIMEOUT` | `5m` | HTTP client timeout (`time.ParseDuration`) |
| `CODER_CLI_REPO` | `tcoder915/ergo-cli-go` | Override the GitHub repo used by `postinstall` |
| `CODER_CLI_VERSION` | _(matches this package)_ | Pin a specific `coder` binary version |
| `CODER_CLI_REQUIRE_DOWNLOAD` | _(unset)_ | Set to `1` to make a failed download fatal |

## Pinning a version

```bash
# install a specific coder version regardless of the @tj/coder-cli wrapper version
CODER_CLI_VERSION=v0.1.4 npm install -g @tj/coder-cli
```

## Troubleshooting

**`coder: native binary not found at .../lib/coder`**

The postinstall hook was skipped. Re-run with scripts enabled:

```bash
npm rebuild @tj/coder-cli
# or
npm install -g @tj/coder-cli --foreground-scripts
```

**`coder: download failed: 404 ...`**

There is no published release for your platform/arch, or the version
you pinned doesn't exist. Try a different version, or fall back to the
Go-based installer:

```bash
curl -fsSL https://raw.githubusercontent.com/tcoder915/ergo-cli-go/main/install.sh | bash
```

## License

UNLICENSED — proprietary to TJ Coder / AI Labs.
