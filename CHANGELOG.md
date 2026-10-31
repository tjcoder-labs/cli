# Changelog

<!-- Generated from release-notes/releases.json by `go run ./cmd/releasenotes`. Do not edit by hand. -->

All notable changes to Coder CLI. The source of truth is [release-notes/releases.json](release-notes/releases.json).

## 0.9.226 — 2026-10-31

**README: the experimental TJ Coder models.** Both READMEs now document the three experimental TJ Coder models, how to sign in to the tjcoder provider, and how to use them from the TUI and headless runs.

### Added

- New README section on the experimental TJ Coder models: tjcoder/ergo-flash:cloud, tjcoder/ergo-pro:cloud (default) and tjcoder/ergo-genius:cloud.
  Covers signing in (/providers, then tjcoder, then Login, or /login), picking a model (/model or --model), listing models, and non-interactive use with coder --provider tjcoder --model tjcoder/ergo-pro:cloud -p. Added to the root README and the npm README.

### Fixed

- Stale README claims: model ids, provider name and providers.json entry now say tjcoder, and the /model picker is described as it works today.
  The command table lists /providers, /login, /logout and /balance. The provider-priority note no longer promises a headless fallback to local Ollama; scripts are told to pass --provider.

## 0.9.225 — 2026-10-31

**Release builds report crashes to Sentry.** Release archives built by scripts/build-release.sh now embed the same Sentry DSN as make build when SENTRY_DSN is unset.

### Fixed

- scripts/build-release.sh defaults SENTRY_DSN to the Makefile's DSN when it is unset, so release binaries report errors to Sentry like make builds.
  Before, release archives were built with an empty DSN and sent nothing. The script prints only a masked DSN. Set SENTRY_DSN= (empty) to build without Sentry.

## 0.9.224 — 2026-10-31

**npm package names the right default provider.** The npm package description and README now say coder uses the TJCoder ergo gateway by default, with local Ollama as an option.

### Fixed

- The @tjcoder/cli description and npm README name the TJCoder ergo gateway as the default provider instead of local Ollama.
  Sign in with /login; local Ollama, Ollama Cloud and Gemini are listed as options. The root README already described the ergo gateway as the default and is unchanged.

## 0.9.223 — 2026-10-31

**Empty replies fail headless runs.** A headless run (coder -p or coder ask) whose final reply is empty now exits with an error instead of succeeding silently.

### Fixed

- coder -p and coder ask exit with status 1 and explain on stderr when the model's final reply is empty, so scripts and CI no longer mistake no answer for success.
  Before, an empty or whitespace-only reply printed nothing and exited 0. The message is shown even with --quiet.

## 0.9.222 — 2026-10-31

**Cloud release: workspaces, wallet and the tjcoder models.** Everything from feat/cloud since 0.9.192 reaches main: multiple local and SSH workspaces, the tjcoder model catalogue, wallet balance and top-ups, clearer error and tool-budget cards, and steadier question cards.

### Added

- The tjcoder model catalogue (ergo-flash, ergo-pro and ergo-genius) is available from the ergo provider.
- See your wallet balance in the status bar and top up from the /balance pane, choosing an amount by number or with the arrow keys. (`/balance`)
- A failed reply shows a red error card with a Retry option, and running out of tool steps shows a Continue card that grants the configured number of extra steps.
- Work across several local or SSH folders in one session with /add, /dirs and /use, and run slash commands while the agent is still working. (`/add`, `/dirs`, `/use`)
  Covered in detail in 0.9.201.
- The shell installer supports 32-bit ARM (armv7l and armv6l) machines.
- Errors from ergo chat requests, including 400 responses, are reported to Sentry, and coder models shows each model's tier, reasoning, vision and best use.

### Changed

- The status bar always shows agent · provider/model, and the working folder is shortened later so the project name stays visible.
- The npm package's version now matches the coder release it downloads.

### Fixed

- Esc closes the wallet pane.
- Messages you send while the agent is working appear in the transcript right away.
- A saved legacy ergo-gateway provider no longer stops the TUI from starting; it is renamed to tjcoder.
- Chat requests close their connection and drain the response stream when they finish.

### Known limits

- Publishing to npm needs the GitHub release v0.9.222 with its five prebuilt archives first: the npm package downloads its binary from there during install.

## 0.9.221 — 2026-10-31

**Question cards answer reliably.** Picking an option on a question card no longer sometimes does nothing, and a card left under another one can be answered again.

### Fixed

- Choosing an option with the arrow keys and Enter always answers the card, even when the keys arrive together over ssh, tmux or a busy machine.
  Enter right after arrow keys was mistaken for a pasted line break and ignored. A pasted line break still never answers a card.
- When two question cards are open at once, such as an SSH password card from /add while Coder is asking you something, answering one gives the keyboard back to the other.
  Before, the remaining card stayed on screen but could not be selected, so its question waited until it timed out and the reply looked frozen.

## 0.9.220 — 2026-10-31

**Assistant label colour fix.** The assistant's label now always starts on the normal background, and four long-failing TUI tests pass again.

### Fixed

- The assistant's "Coder is thinking..." label always opens with the normal background, so it can never pick up the purple from your message bubble.
- Repaired four long-failing TUI tests (task cursor, transcript saving, label colour, timestamps).
  Three tests had fallen behind intended behaviour; the fourth exposed the label colour bug above.

## 0.9.219 — 2026-10-31

**Product name update.** The TUI header now shows @tjcoder as the program name.

### Changed

- The program name in the TUI header is now @tjcoder.

## 0.9.202 — 2026-10-30

**Structured release notes.** Release notes now live in one machine-readable file that ships inside coder, so a future update can show you what changed.

### Added

- Release notes are kept in release-notes/releases.json, and CHANGELOG.md is generated from it.
  coder carries its own copy of the notes. The updater does not show them yet.

## 0.9.196 – 0.9.201 — 2026-10-30

**Multiple workspaces, SSH folders and slash commands mid-turn.** Work across several local or SSH folders in one session, run slash commands while the agent is still working, and open a reminders pane.

### Added

- Add more project folders to a session with /add &lt;folder&gt;. The agent's tools can work in all of them. (`/add`)
- Add a folder on another machine with /add ssh &lt;host&gt;[:&lt;path&gt;]. (`/add`)
  Hosts come from ~/.ssh/config. Without a path you start in the remote home folder.
- List workspaces with /dirs (or /workspaces), remove one with /dirs rm &lt;name&gt;, and pick the active one with /use &lt;name&gt;. (`/dirs`, `/workspaces`, `/use`)
- Typing /add shows hints as you type: a folder path or ssh &lt;host&gt;, your SSH hosts, and folder completion for local and connected remote folders. (`/add`)
- When you have more than one workspace, the status bar shows the active one, like ⌂ name · 2.
- The @ file picker also lists files in added local folders, as name:path.
- Workspaces are saved with the session and come back when you resume it.
- New /reminders pane lists your reminders and when each runs next. (`/reminders`)

### Changed

- Slash commands typed while the agent is working now run instead of being sent to the agent as text. (`/model`, `/compact`, `/login`, `/logout`, `/update`, `/providers`, `/mcp`, `/clear`)
  Most commands, such as /model, run right away and apply from the next request. /compact, /login, /logout, /update and changes to providers or MCP servers wait until the turn ends. /clear and Ctrl-L are refused until the turn ends.
- Running a pane command again closes that pane, and Esc closes an open side pane. (`/tasks`, `/reminders`, `/canvas`)
- Side panes now get two thirds of the right column's height.

### Security

- SSH passwords, key passphrases and one-time codes are asked for in the secure input card and are never saved or logged.
- A new SSH host key needs your approval in a Yes/No card that defaults to No and is never answered automatically.
- sudo on an SSH workspace uses the same approval card as local sudo.

### Known limits

- SSH support has been tested against a simulated ssh, not yet against a live server.
- Remote login shells must be bash, zsh or sh. fish and csh are not supported.
- Remote sudo asks for the password for each command unless the remote account has NOPASSWD set.
- The @ file picker does not list files in SSH workspaces yet.

## 0.9.193 – 0.9.195 — 2026-10-30

**Paste into question and secret cards.** You can now paste into the agent's question cards and into password or token cards, safely.

### Added

- Paste into question cards and secret (password or token) cards.

### Fixed

- A pasted secret no longer keeps its trailing line break.
  Line breaks pasted into a typed answer become spaces, so "one" and "two" on two lines reads "one two".
- Very long pasted secrets show at most 32 dots and a character count, so the card stays on screen.

### Security

- In terminals without bracketed paste, a line break inside a fast paste never submits a card.
  A paste that starts in the first 0.3 seconds after a card appears is ignored with "paste ignored · try again", and leftover pasted text is not typed into the chat input.
- The hidden buffer for a secret is wiped as it grows, so no stray copy stays in memory.

## 0.9.192 — 2026-10-30

**Question cards are never answered for you.** Questions and approvals from the agent always wait for your answer, and focus returns to an open card after menus.

### Fixed

- A card ignores keys other than Esc for 0.3 seconds after it appears, so text you were typing cannot answer it by accident.
- Focus goes back to an open card after you close a menu, form or pane. Tab from the input reaches the card, and Ctrl-U clears the card's text.

### Security

- The agent's questions are never answered automatically from a default.
  When nobody can answer, such as in a headless run, the agent is told no answer is available, and approval questions count as not approved. A default only pre-selects a choice.
- sudo and update prompts need an exact choice. An empty answer means no.

## 0.9.171 – 0.9.191 — 2026-10-29

**ergo by default, updates, reminders and a new look.** Everything since 0.9.170: the ergo provider is the default, coder can update itself, and the TUI gained question cards, a file picker, reminders and refreshed colours.

### Added

- ergo is now the default provider, ahead of local Ollama and Ollama Cloud. Sign in with /login, sign out with /logout, and check your wallet with /balance. (`/login`, `/logout`, `/balance`)
  Sign-in uses a device code. Your token is kept in the system keychain or a private file. Running out of balance shows a clear message.
- ergo models come in flash, pro and genius tiers, and the /model picker remembers your choice for each provider. (`/model`)
- New Ollama Cloud provider, using OLLAMA_API_KEY. (`/providers`)
- coder checks for updates in the background. Install them with /update or coder update, or turn on automatic installs with coder update auto on. (`/update`)
- The agent can ask you questions in a card in the transcript, and sudo commands need your approval first.
- Attach files to a message with the @ file picker.
- The reminder form has a schedule dropdown, date and time fields, a live preview of the schedule, and one-time reminders. (`/reminder`)
- New coder reminders and coder tasks commands for the terminal.
- Anonymous usage statistics and crash reports, controlled with /telemetry on, off or status. (`/telemetry`)
- The /compact hint shows which model compacts and how full its context window is. (`/compact`)

### Changed

- Colours follow your colour scheme, Markdown and code blocks render better, and the canvas uses the full height.

### Fixed

- Provider selection and API version compatibility problems.
- Slash commands and plain messages could freeze the UI.
- The context-usage indicator is accurate.
- Esc closes modal forms, and Ctrl-U deletes back to the start of the line.
- /config keeps its Save and Cancel buttons visible, and the /about intro uses the normal pane width. (`/config`, `/about`)
