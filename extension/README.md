# Coder Browser Bridge 0.9.254 (P0)

Download: https://api.tjcoder.com/coder/browser/releases/download.html
Metadata/checksum: https://api.tjcoder.com/coder/browser/releases/version.json

1. Build/install Coder CLI and its host from the same source revision:
   ```sh
   git clone https://github.com/tjcoder-labs/cli.git
   cd cli
   make install
   mkdir -p "$HOME/.local/bin"
   go build -o "$HOME/.local/bin/coder-browser-host" ./cmd/coder-browser-host
   "$HOME/.local/bin/coder-browser-host" register
   ```
   Keep that binary in place. The registration command writes exact-origin
   native-messaging manifests for Google Chrome and Chromium (Linux/macOS).
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

The host uses owner-only Unix sockets at `~/.local/share/coder/browser.sock` and
`browser.sock.incognito` for split-mode private tabs. One Chrome profile per OS
user may own the bridge at a time; later profiles fail closed rather than taking
over the socket. Incognito targets are prefixed `incognito:`. No browsing data,
URLs or messages are persisted by the host. No telemetry or remote bridge.

P1 cookies/storage/trusted input/emulate/interception and P2 shadow-piercing
wait/ownership enhancements are not implemented in the native tier: unsupported
commands fail clearly. Omitting transport or setting `transport:"cdp"` preserves the
existing raw CDP path (port defaults to 9222); `start_chrome` remains CDP-only. The native debugger tier is deferred. Windows registration is not yet supported.

Updating: download/unzip the new zip over the same folder, rebuild the host if
needed, and click Reload on the extension card. The manifest public key pins its
ID across installations: `fjiihpaeanbchmcedkeeofnadiakfcpk`.
