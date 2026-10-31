# Changelog

<!-- Generated from release-notes/releases.json by `go run ./cmd/releasenotes`. Do not edit by hand. -->

All notable changes to Coder CLI. The source of truth is [release-notes/releases.json](release-notes/releases.json).

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
