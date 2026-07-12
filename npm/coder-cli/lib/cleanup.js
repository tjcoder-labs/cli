#!/usr/bin/env node
// cleanup.js - remove the platform-specific `coder` binary on uninstall.
//
// Invoked by the package.json `preuninstall` script. Best-effort: never
// throws, so a cleanup failure can't block `npm rm`.

const fs = require("node:fs");
const path = require("node:path");

const ext = process.platform === "win32" ? ".exe" : "";
const candidates = [
  path.join(__dirname, `coder${ext}`),
  path.join(__dirname, "coder"),
  path.join(__dirname, "coder.exe"),
  path.join(__dirname, "coder.sha256"),
];

for (const p of candidates) {
  try {
    if (fs.existsSync(p)) fs.unlinkSync(p);
  } catch {
    // ignore — best effort
  }
}
