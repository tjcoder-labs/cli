# Coder CLI

A modern, AI-powered terminal coding assistant for developers. Coder CLI combines a conversational interface, intelligent code analysis, and integrated development tools in a fast, responsive TUI.

![Coder CLI in Action](./cli.png)

## Features

- **AI-Powered Assistance**: Built on large language models (LLM) to understand code, debug issues, and generate solutions. We recommend the `minimax-m3:cloud` model for the best balance of code understanding, tool use, and reasoning.
- **Interactive TUI**: Responsive terminal interface with real-time conversation, cognition panel, and activity tracking
- **Multi-Provider Support**: Works with Ollama, Gemini, and other LLM providers
- **Task Management**: Integrated task tracking system for managing development workflows
- **Code-Aware Context**: Injects repository context, file metadata, and Git information for smarter suggestions
- **Tool Integration**: Direct access to filesystem operations, Git commands, and shell execution
- **Session Persistence**: Maintains conversation history and project context across sessions

## Quick Start

### Installation

```bash
# Clone the repository
git clone https://github.com/tjcoder-labs/cli.git
cd cli

# Build the binary
make build

# Install globally (optional)
make install
```

### Usage

```bash
# Start Coder CLI in the current directory
coder

# Specify a custom model provider
coder --provider gemini --model gemini-2.5-flash

# Connect to a remote LLM provider
coder --host https://your-llm-endpoint.com
```

### Configuration

Coder CLI uses environment variables for configuration:

```bash
# Ollama (default) — we recommend the minimax-m3:cloud model
export CODER_HOST="http://localhost:11434"
export CODER_PROVIDER="ollama"
export CODER_MODEL="minimax-m3:cloud"

# Gemini
export CODER_PROVIDER="gemini"
export CODER_MODEL="gemini-2.5-flash"
```

## Commands

In the Coder CLI interface, use these commands:

- `/agent` - Launch a specialized coding agent
- `/tools` - List available tools
- `/model` - Switch AI models
- `/scroll` - Navigate conversation history
- `/clear` - Clear the conversation
- `/quit` - Exit Coder CLI

## How It Works

Coder CLI operates as a conversational agent with several key components:

1. **Context Injection**: Automatically injects relevant file paths, repository structure, and Git context
2. **Tool Invocation**: Parses and executes developer tools (file operations, git, shell commands)
3. **Agent Processing**: Sends contextualized messages to the LLM for analysis and code generation
4. **Session Management**: Persists conversations and task state across sessions
5. **Interactive Output**: Displays results in an organized, navigable TUI

## Architecture

```
cli/
├── cmd/coder/           # Entry point
├── internal/
│   ├── agent/           # AI agent orchestration
│   ├── client/          # LLM provider clients
│   ├── context/         # Context injection utilities
│   ├── session/         # Session management
│   ├── tools/           # Tool implementations
│   ├── tooling/         # Tool parser and registry
│   ├── tracking/        # Task and item tracking
│   └── tui/             # Terminal UI components
├── test/                # Test fixtures and cases
└── go.mod              # Module definition
```

## Development

### Building

```bash
# Build binary
make build

# Run tests
make test

# Format code
make fmt
```

### Testing

Deterministic testing is available via the test harness:

```bash
# Run test suite
bash TESTS.sh

# View test cases
cat TESTS.md
```

### Agent Directives

See [CODER.md](./CODER.md) for the agent behavior specification and directives that all agents must follow when running in the Coder CLI environment.

## API Provider Support

### Ollama (Default)

Works with any Ollama-compatible endpoint. Perfect for local development. For best results we recommend pulling and using the `minimax-m3:cloud` model:

```bash
coder --provider ollama --host http://localhost:11434 --model minimax-m3:cloud
```

### Gemini

Requires API key:

```bash
export GEMINI_API_KEY="your-api-key"
coder --provider gemini --model gemini-2.5-flash
```

### Custom Providers

Add support for additional LLM providers in `internal/client/`.

## Requirements

- Go 1.25.0+
- An LLM provider (Ollama for local development, or API credentials for cloud providers)

## Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/your-feature`)
3. Commit your changes with clear messages
4. Push to the branch and open a pull request

## License

MIT License - see LICENSE file for details

## Attribution

**Coder CLI** is developed and maintained by [TJ Coder](mailto:tj@tjcoder.com) at [tjcoder-labs](https://github.com/tjcoder-labs).

Part of the TJ Coder platform ecosystem, offering modern AI-powered tooling for developers.

---

