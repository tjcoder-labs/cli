# MCP (Model Context Protocol) Integration Specification

## Overview
The `coder` CLI will implement a client-side integration of the Model Context Protocol (MCP). This allows the AI agent to dynamically extend its capabilities by connecting to external MCP servers, discovering their available tools, and invoking them as if they were native CLI tools.

## Architecture

### 1. MCP Client
The CLI will include an `MCPClient` responsible for:
- **Transport Management**: Supporting `stdio` (for local binaries) and `SSE` (for remote HTTP servers).
- **Lifecycle Management**: Handling connection, initialization, and teardown of MCP sessions.
- **Tool Discovery**: Querying the server for its list of available tools and their JSON Schema definitions.
- **Execution**: Routing tool calls from the agent to the MCP server and returning the formatted result.

### 2. Integration with Tool Registry
To maintain seamless agent interaction, discovered MCP tools will be dynamically registered into the `internal/tools.Registry`. 
- **Namespace**: MCP tools will be prefixed (e.g., `mcp_{server_name}_{tool_name}`) to avoid collisions with native tools.
- **Dynamic Loading**: When an MCP server is added via `/mcp`, the client will fetch the tool definitions and call `Registry.RegisterTool()`.

### 3. The `/mcp` Slash Command
A new TUI command `/mcp` will allow users and the agent to manage integrations.

**Supported Actions:**
- `/mcp add <name> <url|path>`: Adds a new MCP server.
- `/mcp list`: Shows all active MCP integrations and their exposed tools.
- `/mcp remove <name>`: Disconnects and unregisters a server.
- `/mcp status <name>`: Checks connection health.

### 4. Configuration & Persistence
MCP server configurations will be stored in the global configuration file (e.g., `.coder/config.json` or similar), allowing persistence across sessions.

**Configuration Schema:**
```json
"mcp_servers": {
  "tavily": {
    "type": "sse",
    "url": "https://mcp.tavily.com/mcp/?tavilyApiKey=...",
    "enabled": true
  },
  "local-shell": {
    "type": "stdio",
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-everything"],
    "enabled": true
  }
}
```

## Workflow Example: Tavily Integration
1. Agent/User invokes `/mcp add tavily https://mcp.tavily.com/mcp/?tavilyApiKey=...`
2. `MCPClient` establishes an SSE connection.
3. Client calls `list_tools` on the Tavily server.
4. Tavily returns a tool `search` with a schema requiring a `query` string.
5. The CLI registers `mcp_tavily_search` into the `Registry`.
6. The agent now sees `mcp_tavily_search` in its available toolset and can call it to perform web searches.

## Success Criteria
- Successfully connect to the provided Tavily MCP URL.
- Dynamically populate the agent's tool list with Tavily's search tools.
- Execute a search and receive a response within the TUI conversation.
- Persist the integration so it is available upon restart.
