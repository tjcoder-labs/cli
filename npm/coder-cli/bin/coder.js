#!/usr/bin/env node
// coder.js - thin Node shim that exec()s the native `coder` binary.
//
// This file is registered as the `coder` bin in package.json so that
// `npm install -g @tj/coder-cli` puts `coder` on the user's PATH.
// The actual implementation is a Go binary that is downloaded by
// ./lib/fetch-binary.js during `postinstall`.

const { spawn } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");
const os = require("node:os");

// Resolve the platform-specific binary name. Windows gets a .exe suffix.
const ext = process.platform === "win32" ? ".exe" : "";
const binName = `coder${ext}`;

// The binary is downloaded next to this file under lib/<bin>.
const binPath = path.join(__dirname, "..", "lib", binName);

if (!fs.existsSync(binPath)) {
  console.error(
    [
      `coder: native binary not found at ${binPath}`,
      ``,
      `This usually means the postinstall hook was skipped (e.g. when`,
      `installing with --ignore-scripts). To fix:`,
      ``,
      `  npm rebuild @tj/coder-cli`,
      `  # or`,
      `  npm install -g @tj/coder-cli --foreground-scripts`,
      ``,
      `If the problem persists, install via the Go-based installer instead:`,
      `  curl -fsSL https://raw.githubusercontent.com/tcoder915/ergo-cli-go/main/install.sh | bash`,
    ].join("\n"),
  );
  process.exit(1);
}

// Spawn the binary, inheriting stdio so the TUI / streaming output works
// exactly as if the user had run `coder` directly. Forward signals so
// Ctrl+C cleanly tears down the child.
const child = spawn(binPath, process.argv.slice(2), {
  stdio: "inherit",
  env: process.env,
  windowsHide: true,
});

const forward = (sig) => () => {
  if (!child.killed) child.kill(sig);
};
process.on("SIGINT", forward("SIGINT"));
process.on("SIGTERM", forward("SIGTERM"));
process.on("SIGHUP", forward("SIGHUP"));

child.on("exit", (code, signal) => {
  if (signal) {
    // Mirror the conventional shell behavior: 128 + signal number.
    const map = { SIGHUP: 1, SIGINT: 2, SIGTERM: 15 };
    process.exit(128 + (map[signal] || 15));
  }
  process.exit(code ?? 0);
});

child.on("error", (err) => {
  console.error(`coder: failed to exec ${binPath}: ${err.message}`);
  process.exit(1);
});
