# Specification: Coder CLI TTY and Timeout Augmentation

## 1. Problem Statement
The `run_command` tool in the Coder CLI currently executes shell commands using `exec.CommandContext` with standard output/error capture. This creates two primary limitations for hardware-interacting tasks (e.g., Bluetooth scanning):
1. **Lack of PTY**: Many CLI tools (like `bluetoothctl`) detect they are not running in a real terminal (TTY) and disable real-time output flushing or omit specific data (like RSSI).
2. **Strict Timeouts**: Synchronous tool calls are capped at 20 seconds, making it impossible to run discovery scans that require minutes of sampling.

## 2. Proposed Architecture Changes

### 2.1 Pseudo-Terminal (PTY) Integration
To resolve the TTY detection issue, the `run_command` execution logic should be augmented to optionally wrap the command in a pseudo-terminal.

- **Dependency**: Add `github.com/creacktbx/pty` to `go.mod`.
- **Modification**: Update `internal/tools/shell.go` to include a `use_pty` boolean in the `run_command` parameter schema.
- **Implementation**: 
  - When `use_pty` is true, replace `exec.CommandContext` with `pty.StartCommand`.
  - Implement a reader loop to capture the PTY output stream and pipe it to the result buffer.

### 2.2 Timeout Flexibility
To accommodate long-running diagnostic tasks without relying solely on the "Background Job" (which is detached and harder to monitor in real-time), the timeout logic should be refined.

- **Adjustment**: Increase the `max_timeout` from 300s to 1800s (30 mins) for specific trusted patterns or allow the `timeout_seconds` parameter to be respected more flexibly when the agent is in a "diagnostic mode".

## 3. Implementation Steps for Next Agent

1. **Dependency Management**:
   - Run `go get github.com/creacktbx/pty` from the `Projects/cli` root.
   - Run `go mod tidy` to ensure the dependency graph is clean.

2. **Code Modification (`internal/tools/shell.go`)**:
   - Update `runCommandTool.Definition` to add `use_pty` (bool) to parameters.
   - In `Execute`, if `args.UsePty` is true:
     - Use `pty.StartCommand` instead of `exec.CommandContext`.
     - Use `io.Copy` or a scanner to read from the PTY's output until the process exits or the context timeout is reached.

3. **Build and Validation**:
   - Recompile the binary: `make` or `go build -o bin/coder cmd/coder/main.go`.
   - Validate by running `bluetoothctl scan on` with `use_pty=true` and verifying that RSSI values are captured in the output.

## 4. Success Criteria
- `bluetoothctl scan on` successfully returns raw device discovery events including RSSI.
- Long-running synchronous commands do not get killed by the internal Go context until the specified `timeout_seconds` is reached.
- All changes are compatible with the existing `background_job` infrastructure.
