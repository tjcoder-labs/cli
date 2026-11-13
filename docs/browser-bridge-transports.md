# Browser Bridge: CDP + Extension Transports

## Goal

Let `browser_bridge` drive a Chrome browser through **either** of two
transports, selected per call:

1. **`cdp`** (default) — existing Chrome DevTools Protocol path over
   `--remote-debugging-port`. Full control of a locally launched Chrome.
2. **`extension`** — the Ergo Chrome extension over Native Messaging, with
   the **coder binary acting as the native host** (no Express relay hop).
   Targets the user's own Chrome profile; works over SSH/remote where CDP
   is unreachable.

## Design decisions (confirmed)

- New `transport` param on `browser_bridge`: `cdp` (default) | `extension`.
- New CLI subcommand **`coder native-host`** speaks NM over stdio.
- Tab resolution: explicit `tab_id` when passed; else the extension's active
  tab (matches current `background.js`).
- `background.js` action set is **unchanged**.

## Architecture

```
                    ┌────────────┐   NM stdio    ┌───────────────┐
 browser_bridge     │  coder     │◄─────────────►│  Chrome       │
 (transport=        │ native-host│  length-       │  extension    │
  extension) ──────►│ (subcmd)   │  prefixed JSON│  background.js│
                    └────────────┘               └──────┬────────┘
                                                        │ chrome.scripting
                                                        │ chrome.tabs
                                                  ┌─────▼──────┐
                                                  │ target tab │
                                                  └────────────┘
```

## Components

### 1. `coder native-host` subcommand (`cmd/coder/`)

- Detected in `main()` before telemetry/provider (like `askpass`/`reminders`).
- Reads 4-byte LE length-prefixed JSON frames on **stdin**; writes same on
  **stdout**. Logs to stderr/file (stdout is reserved for the protocol).
- Inbound messages FROM the extension:
  - `NativeHostReady` / `ping` → ack, track liveness.
  - `{commandId, status, result, error}` → resolve the matching pending
    request (map keyed by `commandId`).
- Outbound commands TO the extension: `navigate`, `extractSession`,
  `executeScript`, `screenshot`, `getTabInfo`, `setContent`, `listTabs`,
  `closeTab`, `ping`.
- New package `internal/browser/nativehost/`:
  - `NMReader` — incremental frame parser (port of `NativeMessagingReader`).
  - `writeNativeMessage` — frame writer.
  - `Host` — request/response correlation (pending map + timeout), command
    dispatch to the action set.
- Reuses `internal/browser`'s existing action helpers where semantics match
  (e.g. `Navigate`, `Evaluate`), but the actual tab execution happens
  **inside the extension** via `chrome.scripting`/`chrome.tabs`.

### 2. `browser_bridge` tool (`internal/tools/browser_bridge.go`)

- Add `transport` string param (`"cdp"` default, `"extension"` opt-in).
- Dispatch at the top of `Execute`:
  - `cdp` → existing `getBridge`/CDP switch (unchanged).
  - `extension` → new `extBridge` path.
- New `internal/browser/extension.go` — `ExtClient` that dials the local
  `native-host` process? **No.** Two sub-options:

  **A. In-process (recommended):** the extension transport is only usable
  when `browser_bridge` runs inside `coder native-host` itself. In the normal
  TUI/headless process the extension transport is unavailable (returns a clear
  "extension transport requires coder native-host running as the NM host"
  error) unless the host is a **separate long-running** `coder native-host`
  daemon that the CLI talks to over a local socket.

  **B. Daemon + socket:** `coder native-host --serve` also opens a local
  unix socket (e.g. `$XDG_RUNTIME_DIR/coder-native-host.sock`). `browser_bridge
  transport=extension` connects to that socket and issues the same JSON
  commands. This is the recommended shape: one daemon, many CLI sessions.

  **Decision needed:** recommend **B** (daemon + local socket), with
  `browser_bridge` falling back to an error if no socket is listening.

- Extension action set maps to the existing tool actions as:

  | browser_bridge action | extension action | notes |
  |---|---|---|
  | `navigate` | `navigate` | `{url, tabId, newTab}` |
  | `evaluate` | `executeScript` | `{script, tabId}` |
  | `click`/`type`/`press_key` | not natively supported | return "unsupported on extension transport; use evaluate" |
  | `wait_for` | (client-side poll via `executeScript`) | possible, optional |
  | `get_dom` | `executeScript` | `document.documentElement.outerHTML` |
  | `screenshot` | `screenshot` | returns `dataUrl` |
  | `list_tabs` | `listTabs` | |
  | `close_tab` | `closeTab` | |
  | `get_cookies`/`set_cookie`/`delete_cookie`/`clear_site_data`/`storage_*` | `extractSession` (read-only) | writes unsupported on extension transport |
  | `getTabInfo` | `getTabInfo` | |
  | `setContent` | `setContent` | |
  | `chrome_status` | `ping` | liveness only |

### 3. NM manifest (`chrome-extension/com.tjcoder.chromebridge.json`)

- Change `path` from `native-host.js` to the **coder binary** (e.g.
  `/usr/local/bin/coder` or wherever it is installed) with an argv entry for
  `native-host`. Native Messaging manifests support an `"path"` plus the host
  is invoked with the extension ID as argv[1]; an `"allowed_origins"` entry
  matching the extension ID.
- Keep `allowed_origins` = `chrome-extension://lkomhjeiejkceemmcgjjkafdacdllpmm/`
  (or the machine's assigned ID).
- `background.js` unchanged.

## Open questions

1. **Daemon model:** Option A (host = the invoking process, extension
   transport only inside `native-host`) vs Option B (daemon + local socket,
   recommended). Confirm B.
2. **Socket path/location:** `$XDG_RUNTIME_DIR/coder-native-host.sock` vs
   `~/.local/share/coder/native-host.sock`. Recommend the latter (stable,
   no XDG dependency).
3. **Write-path parity:** extension transport is read-heavy (no trusted
   click/type, no cookie/storage writes). Accept the reduced surface with
   explicit "unsupported" errors, or invest in `executeScript`-based
   click/type emulation later?
4. **Ownership registry:** extension tabs have numeric Chrome tab IDs, not
   CDP targetIDs. Reuse the same registry keyed by `"ext:<tabId>"`, or a
   separate registry file?

## Suggested task order

1. `internal/browser/nativehost/` — NM reader/writer + host correlation
   (pure, unit-testable).
2. `coder native-host` subcommand wiring + `--serve` socket daemon.
3. `internal/browser/extension.go` — ExtClient (socket dial + JSON commands).
4. `browser_bridge` `transport` param + action mapping + unsupported errors.
5. Update NM manifest `path` → coder binary.
6. End-to-end test round-trip.
7. Release-notes entry + `package.json` bump (required on version change).
