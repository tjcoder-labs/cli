# Coder Browser Bridge 0.9.254 (P0), CLI 0.9.256+

Download: https://api.tjcoder.com/coder/browser/releases/download.html
Metadata/checksum: https://api.tjcoder.com/coder/browser/releases/version.json

1. Build/install Coder CLI:
   ```sh
   git clone https://github.com/tjcoder-labs/cli.git
   cd cli
   make install
   coder
   ```
   `make install` registers the installed **coder executable itself** as
   `com.tjcoder.browser`; no second binary or manual daemon is required.
   Each interactive TUI startup refreshes registration and starts the relay.
   Chrome launches the same executable with its exact extension-origin argv as
   a Native Messaging stdio shim. Keep coder at its installed path. Optional
   manual commands: `coder browser-host register` / `unregister`.
   Headless `coder -p`, `coder ask`, and other subcommands do not start a relay.
2. Unzip the download into a permanent folder. In `chrome://extensions`, enable
   Developer mode, choose **Load unpacked**, and select that folder.
3. Requires Chrome 135+. Enable **Allow User Scripts** in this extension's
   details for evaluate (Chrome 135–137 uses the Developer mode switch instead).
   Enable **Allow in incognito** if you want private windows. Click the extension
   toolbar icon to reconnect if you installed the native host after the extension.
4. Ask Coder to call `browser_bridge` with `{"action":"chrome_status","transport":"stealth"}`, then
   `{"action":"new_tab","url":"https://example.com","transport":"stealth"}`. Claim pre-existing tabs
   before operating on them. Use `new_tab` with `incognito:true` for a new private
   window. `inspect` is an alias of `get_dom`.

Stealth is opt-in via `transport:"stealth"` (CDP remains the compatibility default): no debug port, webdriver flag, or debugger attachment.
Navigate, reload, history, DOM inspection, tabs, private windows, evaluate and
visible-tab JPEG screenshots are implemented. Screenshots focus the requested
window and activate the tab (visible UI side effect). Chrome internal/store pages
and restricted URLs cannot be scripted. Evaluate uses Chrome's USER_SCRIPT
isolated world: DOM access works, page-global JS variables are not shared.
DOM inspection uses ordinary scripting in the ISOLATED world.

## Local-only security and lifetime

The interactive process owns Unix sockets in
`~/.local/share/coder/browser-relay/` (directory 0700, sockets 0600).
`browser.sock` accepts command RPC; `browser.sock.native` is a separate framed
extension attachment channel. Private per-session backend sockets handle normal
and split-mode incognito workers. Both the Chrome shim and relay require exactly
`chrome-extension://fjiihpaeanbchmcedkeeofnadiakfcpk/`. Command greetings cannot
turn into extension attachments. Only one interactive session can own the relay;
others warn without taking it over. Socket paths are never replaced on startup.

Normal TUI return and handled INT/TERM/HUP close listeners, extension connections,
and backend sockets. SIGKILL/power loss cannot run cleanup: if a stale fixed socket
blocks startup, first ensure no coder relay is running, then remove only the stale
`browser.sock` / `browser.sock.native` files. Never delete a live session's sockets.
Chrome reconnects via the toolbar button or the extension's periodic retry.
No extension commands succeed until its worker connects; disconnected requests
fail clearly. Chrome and the extension are not automatically installed/launched.

The extension can navigate/create/close/inspect/script permitted pages and take
visible-tab screenshots (which focus/activate tabs). It has broad page access;
only install a trusted copy. User Scripts and incognito remain explicit Chrome
permissions. Native Messaging executes as your OS user, not root. No sandbox is
added around that user account. **Owner-only sockets exclude other OS users, not
malicious processes already running as you.** Origin argv validation checks the
extension launch context, not individual same-user socket clients, which can
impersonate the shim. Treat every process under your account as trusted.

Auto-starting makes the relay reachable to your account for the TUI's lifetime,
instead of only after Chrome launches a host. It does not broaden the OS-user
boundary or grant browser control without a connected extension. There is no TCP
listener (public or loopback), remote bind, host telemetry, browsing persistence,
or skipped origin check. The CLI's existing telemetry setting is unchanged.
CDP remains the default and needs no extension/relay. Stealth never silently
falls back to CDP. Debugger-only operations require explicit CDP and its debug
port; the extension itself does not attach a debugger.

P1 cookies/storage/trusted input/emulate/interception and P2 shadow-piercing
wait/ownership enhancements are not implemented in the native tier: unsupported
commands fail clearly. Omitting transport or setting `transport:"cdp"` preserves the
existing raw CDP path (port defaults to 9222); `start_chrome` remains CDP-only. The native debugger tier is deferred. Windows registration is not yet supported.

Updating: download/unzip the new zip over the same folder, update coder if
needed, and click Reload on the extension card. The manifest public key pins its
ID across installations: `fjiihpaeanbchmcedkeeofnadiakfcpk`.
