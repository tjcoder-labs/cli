# Coder CLI — Feature Showcase / Demo Guide

This file is a scripted tour of every agent, tool, and slash command in the
Coder CLI. Use it to give a live demo: paste the prompts, watch the cognition
stream, and call out each feature as it appears on screen.

Run the CLI with `./bin/coder` (or `make run`) from a terminal that's at least
~120 columns wide so the right-hand panels render well.

### 🎬 Running this demo hands-free

You don't have to read this file and type the prompts yourself. Just start the
software-engineer agent and say:

```
Execute DEMO.md
```

The agent will read this guide, then walk you through it section by section —
creating tasks as a roadmap, running the demo prompts on your behalf, and
pausing with `ask_user` whenever a decision genuinely needs you (switching
agents, approving a `sudo` command, choosing a demo direction). It drives the
right-hand panels (tasks, canvas, activity) so you can just watch. Answer its
cards, steer it mid-turn if you want to skip ahead, and it keeps going.

---

## 0. First things first — the anatomy of the TUI

Before any prompt, point out the layout:

- **Left — transcript.** The conversation. Every turn streams here.
- **Right — panels.** The context-aware surface: `activity` (default), `tasks`,
  `articles`, `canvas`, `memories`. The agent switches these for you.
- **Bottom — input line.** Type prompts or `/slash commands`.

Quick wins to show (no model involved):

```
/help          → grouped command reference in the activity pane
/about         → welcome screen (ASCII art + intro)
/agent         → the agent switcher (7 personas)
/tools         → toggle individual tools on/off
/config        → JSON config editor with a "Current Defaults" readout
```

Call out that the agent **drives the right panel for you** — when it plans
multi-step work it opens the tasks pane, when it reads a file it opens it on
the canvas. The human never has to touch the panels manually.

---

## 1. software-engineer — the default, full toolset

The default agent has every registered tool: filesystem, git, shell, browser,
email (Resend), DNS (Cloudflare), ledger, model switching, Android, and more.

Demo script:

```
Make me a small Go CLI that counts words in a file. Write it, then build and run it against a test file.
```

Walk through what happens:
- The agent creates tasks (watch the **tasks pane** light up).
- It writes files (watch the **canvas** show each file as it's created).
- It runs `go build` / `go run` (watch the **activity log**).
- It edits files with `edit_file` after showing you the range on the canvas.

Follow-up to show more tools:

```
Now review the code you wrote, find at least one bug or edge case, and fix it.
```

This shows `search_code`, `read_file`, and the inspect-before-edit discipline.

### Git workflow demo

```
Check git status and show me a short log of recent commits.
```

### Long-running / background work

```
Run `sleep 30 && echo done` in the background, then check on it.
```

Shows `run_command` with `background=true` + `background_job` polling.

---

## 2. terminal-specialist — shell & environment expert

Switch with `/agent` → *Terminal Specialist*. Narrow toolset: shell, filesystem,
git read-only, inspect.

```
List this directory, show me the disk usage of the biggest items, and tell me what shell and OS I'm on.
```

Shows `run_command`, `list_directory`, `inspect_project`, and environment probing.

---

## 3. code-reviewer — high-signal review

Switch to *Code Reviewer*. Minimal toolset: search/read/git-status/inspect.

```
Review internal/tools/registry.go and report only real bugs or risky behavior. No style nits.
```

Demonstrates the "no cosmetic changes" discipline and focused findings.

---

## 4. cloud-expert — GCP + Cloudflare + email

Switch to *Cloud Expert*. Tools: `run_command` (gcloud), `cloudflare`, `resend`,
plus tasks/canvas for reports.

Demo (safe, read-only first):

```
Preflight: check gcloud is installed and who I'm authenticated as.
```

Then show the Cloudflare integration:

```
List my Cloudflare zones and DNS records for the first one. (Read-only — don't change anything.)
```

Then Resend:

```
Show me the last few emails I've sent via Resend.
```

Call out the **safety gate**: any mutating command (firewall change, DNS
create, email send) prompts for explicit confirmation first.

---

## 5. android-assistant — ADB device administration

Switch to *Android Assistant*. Tools: `adb_shell`, `run_command`, filesystem.

```
Check for connected devices, then list the packages installed on the first one.
```

Shows the `adb devices` preflight and `adb_shell` remote execution.

---

## 6. social-researcher — browser research & engagement

Switch to *Social Researcher*. Tools: `browser_bridge`, `interaction_log`,
`research_note`, `fetch`, plus a strict dedup ledger.

Demo (research-only, no engagement):

```
List the browser tabs you can see, then read and summarize the first relevant one.
```

Shows `browser_bridge` tab management and DOM reading. Then show the ledger:

```
What have I already interacted with? Show the interaction log.
```

Call out the **three-layer dedup discipline**: `interaction_log` (exact match)
→ `research_note` (semantic trail) → live page state. Every like/comment/DM is
check → act → record.

Optionally show scheduled engagement:

```
Schedule a check of a thread every 10 minutes, capped at 20 likes/day.
```

Shows `set_reminder` as an active, self-reinvoking schedule with a daily cap.

---

## 7. storyteller — interactive narrative

Switch to *Storyteller*. Only two tools: `ui_control` + `manage_items` (used as
story-state ledger).

```
Start a noir detective story set in a rainy 1940s city. I'm the detective.
```

Shows branching, continuity tracking, and the tasks pane repurposed as a story
board. Each turn ends with "What do you do next?"

---

## 8. Secure sudo elevation

`run_command` never lets a model-authored command reach `sudo` on its own.
When a command invokes sudo, a gate kicks in:

1. **Authorization card** — an inline prompt asks you to allow the exact
   command: **Allow once** / **Allow sudo for this session** / **Deny**.
2. **Masked password** — if sudo needs a password, it's collected with a
   hidden prompt and fed to `sudo -S -v` on stdin. The password never reaches
   the model, transcript, history, logs, or disk, and the buffer is zeroed
   immediately after the write.
3. **`sudo -n` shim** — the command runs with a `sudo` shim first on PATH that
   forces non-interactive mode, so sudo can never fall back to prompting on
   the TUI's own terminal.

"Allow for this session" re-uses sudo's timestamp cache; "Allow once" drops the
ticket (`sudo -k`) after the single command. Headless runs deny sudo unless
`--allow-sudo` was passed, and even then never prompt.

Demo:

```
What packages are installed as root-managed system services? Use sudo to check systemctl status of one service.
```

Watch the card appear, then either allow or deny. Esc is always a denial — no
option is ever applied without an explicit choice.

---

## 9. ask_user — decisions that genuinely need the human

`ask_user` is the agent's structured question card: a clarification, an
authorization, or a "what next?" prompt rendered inline in the transcript.
Up to 6 numbered options (pick with a number key or arrows+Enter), plus a
free-text row unless `allow_custom` is false.

Key semantics to call out:
- The **default is never auto-applied** — it only pre-selects a row. Only an
  explicit pick or typed answer counts.
- Only `ANSWERED` is a user decision. `DISMISSED`, `UNAVAILABLE`,
  `INTERRUPTED`, and `CANCELLED` all mean "no decision" — and for an
  `authorization`, they all mean **denied**.
- Headless (no interactive user) returns `UNAVAILABLE` and authorizations are
  never granted.

This is the same card the sudo gate uses. During any demo you can steer the
agent mid-turn (send another message) and its pending question is reported as
`INTERRUPTED` so it follows your new instruction instead.

---

## 10. Panel control — tasks, activity, canvas

The agent drives the right-hand panel for you, but the human can too. The
three to show:

- **tasks** (`/tasks`) — the plan. The agent breaks multi-step work into tasks
  here (create → doing → done). The storyteller repurposes it as a story board.
- **activity** (`/activity`) — the tool log. Every `run_command`, `edit_file`,
  fetch, and panel switch streams here. This is the default panel.
- **canvas** (`/canvas [path] [start-end]`) — content, never commentary. The
  agent opens files, drafts, and data here so you read the artifact alongside
  its explanation.

Show the interplay in one prompt:

```
Plan a three-step change to the repo: find a TODO, fix it, and run the tests. Show me each file as you touch it.
```

The tasks pane fills with the plan, each file flashes onto the canvas as it's
edited, and the activity log records every tool call.

---

## 11. The slash-command tour

Run through the command surface grouped by theme:

**Work**
```
/task            → create a task
/tasks           → toggle tasks pane
/reminder        → create a reminder
/reminders       → toggle reminders pane
/memory          → store a durable memory
/memories        → toggle memories pane
/canvas main.go  → open a file in the canvas
/canvas main.go 1-40  → open a line range
```

**Workspace**
```
/add /path/to/dir       → add a workspace folder
/add user@host          → add an SSH host
/dirs                   → list workspaces
/use <name>             → set active workspace
/dirs rm <name>         → remove a workspace
```

**Gateway**
```
/login       → sign in to the ergo gateway
/logout      → sign out
/balance     → wallet + top-up
/providers   → manage providers (list/add/remove)
```

**Session**
```
/config        → JSON config editor
/environment   → edit the env template
/mcp           → manage MCP integrations (list/add/remove)
/telemetry     → on / off / status
/update        → check for updates
/compact       → free context mid-session
/clear         → wipe the session
```

**Capture**
```
/screenshot          → save screen as PNG
/capture start       → begin recording
/capture stop        → end recording
```

**Subsessions**
```
/ask @name <message>   → message a named subsession
/btw @name <message>   → alias; subsessions auto-create
```

---

## 12. Cross-cutting features to call out

- **Cognition pane** — streaming model reasoning, visible on every turn.
- **Mid-turn steering** — send another message while a turn runs; it lands as
  "you steered" without interrupting the agent.
- **Canvas** — the agent presents code/drafts/data for you to read alongside
  its explanation; never commentary.
- **Model switching** — `/model` (per-provider) and `/agent` (per-persona),
  each with a default model.
- **Provider fallback** — ergo first, then local Ollama, then Ollama Cloud;
  Gemini also supported.
- **MCP bridging** — external MCP servers expose `mcp_{server}_{tool}` tools
  dynamically.
- **Headless mode** — the same agents run non-interactively (CI, cron, scripts).

---

## Suggested demo order (15-minute version)
1. `/about` + `/help` — orient (0:30)
2. software-engineer: build-the-word-counter script (4:00)
3. git status/log + background job (1:30)
4. `/agent` → cloud-expert: gcloud preflight + Cloudflare list (2:30)
5. `/agent` → social-researcher: tab list + interaction log (2:00)
6. `/agent` → storyteller: one branching scene (2:00)
7. Slash-command highlights: `/canvas`, `/memory`, `/task`, `/add` (1:30)
8. Q&A (1:00)

Total: ~15 minutes, touching all 7 agents and most tool families.
