# Local-Only Browser Extension Bridge (design)

Goal: let the `browser_bridge` tool drive Chrome through a local MV3
extension instead of requiring Chrome to be launched with
`--remote-debugging-port`. Two transport tiers cover the full action
surface — navigate, inspect, evaluate, new tabs, new private (incognito),
screenshots, cookies, storage, emulate, and network interception — all
loopback only.

## Stealth-first, debugger-optional

The default transport is **stealth**: `chrome.scripting` + `chrome.tabs`
+ `chrome.windows` + `chrome.cookies` + `chrome.tabs.captureVisibleTab`.
These drive *real* Chrome with **no `navigator.webdriver` flag and no
"automated test software" infobar**, because no Debugger API is ever
attached. This is the right default for automation, scraping, research,
and fine-tuning data collection — sites don't get served a bot-detection
wall.

The **`cdp`** transport (`chrome.debugger`) is strictly opt-in and
reserved for the few actions that need trusted synthetic input or raw
CDP: `Input.dispatch*`, `Network` interception, and `Emulation`. The
extension attaches per-command and detaches immediately after, keeping
the webdriver exposure window minimal.

- **Stealth (default)** — `transport: "stealth"` (or omitted): no
  debugger, no webdriver flag, no infobar.
- **CDP (opt-in)** — `transport: "cdp"`: full CDP parity via
  `chrome.debugger.sendCommand`, but detectable.

## Why an extension can't host the connection

An MV3 extension cannot listen on a socket, so the CLI can't dial it
directly over WebSocket the way it dials `ws://127.0.0.1:9222`. Instead we
insert a small Go **native messaging host** as the loopback bridge:

```
CLI (browser_bridge tool)
   │  JSON-RPC over loopback (127.0.0.1 unix socket / localhost TCP)
   ▼
coder-browser-host  (Go, native messaging host)
   │  chrome.runtime.connectNative  (stdio, length-prefixed JSON)
   ▼
MV3 extension  ── chrome.debugger.sendCommand(...) ──▶ Chrome tabs
```

`chrome.debugger` is literally CDP, so every command the existing
`Client.Call`/`CallSession` already knows maps 1:1 onto
`chrome.debugger.sendCommand({tabId}, method, params)`.

## Components to add

1. **Extension** (`extension/` — manifest v3)
   - Permissions: `debugger`, `tabs`, `windows`, `nativeMessaging`,
     `cookies`, `storage`, `scripting`, `activeTab`, `webNavigation`.
   - `"incognito": "split"` so it can create/drive private windows.
   - Background service worker: receives host messages, dispatches to
     `chrome.scripting` / `chrome.tabs` / `chrome.windows` /
     `chrome.cookies` (stealth) and, only when requested,
     `chrome.debugger` (CDP), posting results back over the native port.
   - Native messaging host ID: `com.tjcoder.browser`.

2. **Native messaging host** (`cmd/coder-browser-host/`)
   - Registers a host manifest at
     `~/.config/google-chrome/NativeMessagingHosts/com.tjcoder.browser.json`
     (and Chromium equivalent), pointing at the built binary, allowlisting
     the extension ID.
   - Loopback listener (unix socket at `~/.local/share/coder/browser.sock`,
     or `127.0.0.1:<port>`). ACL: loopback only, no external binds.
   - Relays JSON-RPC between the CLI socket and the extension stdio port.
   - Validates the peer origin (extension ID) before forwarding.

3. **CLI transport** (`internal/browser/`)
   - New `Transport` interface behind the existing `Client` so
     `DialBridge` can connect to either the raw CDP WebSocket **or** the
     host socket. `Client.Call`/`CallSession`/`Attach` stay unchanged.
   - `browser_bridge` gains a `transport: "stealth" | "cdp"` param
     (default `stealth`, preserving no-webdriver behavior). `start_chrome`
     stays CDP-only; the stealth path needs no Chrome launch at all.

## Action → extension mapping (CDP parity)

Each row notes its transport: **stealth** (default, no webdriver) or
**cdp** (opt-in `chrome.debugger`).

| browser_bridge action | Extension mechanism | Transport |
|---|---|---|
| `list_tabs` | `chrome.tabs.query({})` (+ `chrome.windows.getAll` for incognito) | stealth |
| `new_tab` | `chrome.tabs.create({url, windowId})` | stealth |
| `new private` | `chrome.windows.create({incognito:true})` → `chrome.tabs.create` | stealth |
| `close_tab` | `chrome.tabs.remove(tabId)` | stealth |
| `navigate/reload/back/forward` | `chrome.tabs.update/reload` | stealth |
| `evaluate` | `chrome.scripting.executeScript` (isolated world) | stealth |
| `inspect` / `get_dom` | `chrome.scripting.executeScript` (serialize DOM) | stealth |
| `click/type` (script) | `chrome.scripting.executeScript` | stealth |
| `click/type/press_key` (trusted) | `chrome.debugger` `Input.dispatchMouseEvent`/`dispatchKeyEvent` | cdp |
| `wait_for` | `chrome.scripting` poll (shadow-piercing via injected selector) | stealth |
| `screenshot` | `chrome.tabs.captureVisibleTab` | stealth |
| `get/set/delete_cookie`, `clear_site_data` | `chrome.cookies` API | stealth |
| `storage_get/set/keys` | `chrome.scripting` `localStorage`/`sessionStorage` | stealth |
| `emulate` | `chrome.debugger` `Emulation.setDeviceMetricsOverride`/`setUserAgentOverride` | cdp |
| `intercept_enable/disable` | `chrome.debugger` `Network.enable`/`setRequestInterception` | cdp |
| `chrome_status` | host ↔ extension liveness ping | either |

## Security invariants

- Host binds loopback only; never a public interface.
- Host manifest allowlists the exact extension ID (one app owns the bridge).
- Native port is one-way-per-extension; no third-party origin can connect.
- No telemetry, no network egress beyond the local socket.

## Phasing

1. **P0** — Host + extension covering the stealth surface: navigate /
   evaluate / inspect / tabs / new private / screenshot (the core ask).
2. **P1** — cookies, storage, and the opt-in `cdp` tier (trusted input,
   emulate, network interception).
3. **P2** — shadow-piercing `wait_for` parity + ownership registry reuse.
