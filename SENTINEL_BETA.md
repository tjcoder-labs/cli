# SENTINEL_BETA

A short spec for the next cycle of `coder-cli` and the Sentinel dashboard. Scope is kept tight: local-first data, with a single bridge to `ai.tjcoder.com` for model offloading. Everything below is sized to land in one or two focused PRs.

## 1. Goals

- Make the TUI a *useful working surface* for the kinds of dev work we actually do: rapid iteration, token-friendly prompting, and a visible plan.
- Give the agent structured memory within a session (tasks, reminders) without dragging in a database.
- Keep `coder-cli`'s data local. Sync with the hosted dashboard is a later concern.
- Reach beta users through the existing tenant-scoped API key mechanism rather than a separate identity system.

## 2. Environment context injection

Every assistant turn prepends a short, machine-readable block to the system prompt:

```
[environment]
cwd = <absolute path>
os  = <runtime.GOOS>/<runtime.GOARCH>
host = <hostname, short>
git_branch = <branch or "detached: <sha>">
git_dirty  = true|false
session_id = <stable hash of workspaceRoot + session start>
timestamp  = <RFC3339, UTC>
```

Implementation:

- New helper in `internal/tooling` (e.g. `BuildEnvironmentContext(workspaceRoot) string`).
- Called once per `runner.Run` invocation and concatenated onto `agentCfg.Prompt` after the AGENTS.md injection.
- Cheap to compute (no shell-out for git; use `git rev-parse` + `git status --porcelain` only when `git` is on PATH and the workspace is a repo).
- Replaces the current "[context] Injected AGENTS.md" line in the reasoning stream with a single consolidated environment block so we stop paying for two separate reasoning emissions.

## 3. Tasks system

### 3.1 Data model

Stored in `session.json` alongside history, refs, transcript, reasoning, activity. Modelled after the `Tasks` shape used in `../ergo/ergo-chat-dashboard` (id, title, status, owner, createdAt, updatedAt, meta). Compact JSON form:

```json
{
  "tasks": [
    {
      "id": "tsk_01J...",
      "title": "Fix user-message bubble padding",
      "status": "todo",
      "owner": "agent",
      "created_at": "2026-07-08T13:40:00Z",
      "updated_at": "2026-07-08T13:40:00Z",
      "meta": { "priority": "high", "tags": ["tui", "polish"] }
    }
  ]
}
```

- `id` is a 26-char ULID (`internal/tasks/id.go`), not a random hex, so they're sortable and human-greppable.
- `status ∈ {todo, doing, blocked, done, cancelled}`. No custom free-form states.
- `meta` is a free-form `map[string]any` for tool-specific fields (priority, due, tags, links). Kept small.
- `owner ∈ {user, agent, system}`. `system` covers auto-generated housekeeping items.

### 3.2 Storage

- Add `Tasks []Task` to `persistedSession` in `internal/tui/session.go` *and* `cmd/coder/main.go`'s `persistedSession` (the headless ask mode has its own copy — they need to merge into one type, see §6).
- Persistence path: `<workspaceRoot>/.ergo-cli-go/session.json`, same as today. Tasks are written on every mutation and on session save.
- No new on-disk files. Reminders and tasks live in the same JSON.

### 3.3 TUI surface

A third panel between COGNITION and ACTIVITY. New layout:

```
+------------------+----------------+
| Conversation     | COGNITION      |
|                  +----------------+
|                  | TASKS          |   <-- new
|                  +----------------+
|                  | ACTIVITY       |
+------------------+----------------+
```

- Title `TASKS`, in the same purple as the other panel labels.
- One row per task. Each row: status glyph (`○ ◐ ● ✕ ⊘`) + title, dim timestamp, meta tags in `hexDim` if present.
- Selected row: `bgSelect` background, `lavender` text.
- `Tab` cycles focus: input → TASKS → ACTIVITY → input. When TASKS has focus, `j/k` move selection, `Space` toggles status (todo→doing→done), `d` deletes with confirmation, `Enter` opens a small form for edit.
- Inactive state (no panel focus): same row format, no selection highlight.

### 3.4 Agent-facing tools

A single `manage_tasks` tool with an `action` discriminator. Fewer tools, fewer schema descriptions, and the action is auditable in the activity feed:

| action   | required args                | effect                                       |
|----------|------------------------------|----------------------------------------------|
| `list`   | (filter optional)            | returns current task list to the model       |
| `create` | `title`, `meta?`             | appends a `todo` task                        |
| `update` | `id`, `status?`, `title?`, `meta?` | merges fields                       |
| `delete` | `id`                         | removes the task                             |
| `clear`  | `status?` (default `done|cancelled`) | bulk delete                          |

All actions are reflected back to the model as a short JSON summary so it can confirm the change in its next turn.

### 3.5 Auto-injection

Before each `runner.Run`, the system prompt gains a `[tasks]` block summarizing the open task list:

```
[tasks]
- [todo]    Fix user-message bubble padding          (priority=high)
- [doing]   Write SENTINEL_BETA.md                   (owner=agent)
- [blocked] Reach parity with ergo-chat-dashboard   (reason=needs_sentinel_ts)
```

Capped at 20 lines; older `done` tasks omitted unless `verbose=true` is passed (a headless flag, not a tool arg). Same block is rendered in the TASKS panel, but the prompt version is a flat text dump for cheap parsing.

## 4. `/article` command

A local, offline-first counterpart to the article endpoint that `ergo-ai-server` exposes via `ergo-chat-dashboard`. Shape:

- Mirrors `/task`, `/reminder`: slash opens a modal with a form; the modal calls into the same persistence layer.
- Storage: an `articles` array in `session.json`, same file as tasks. Each entry has `id`, `title`, `body`, `tags`, `created_at`, `updated_at`, `source` (`local` | `ai.tjcoder.com`).
- The modal has three modes:
  1. **List** — pick an existing article to view/edit.
  2. **New** — title + body, with an optional `Generate from prompt` button that calls the model through the current provider and writes the result into the body.
  3. **Fetch** — paste a slug or URL the hosted server exposes; on success, the article is downloaded and cached locally. Offline (no host reachable) is a clear error, not a hang.
- The article list is also exposed to the agent via a `read_article(id)` tool so the model can pull prior context into a turn.

This is the piece that will eventually sync with the hosted dashboard's article store; the data shape is chosen so that future sync is a 1:1 upsert.

## 5. Auth (token path)

- Reuse the existing tenant-scoped API key on `ai.tjcoder.com`. No new identity provider.
- CLI stores the key in `~/.config/ergo/auth.json` with `0600` perms. Override via `--token` flag or `ERGO_TOKEN` env var.
- The provider factory (`internal/client/factory.go`) gets a new `cloud` provider kind that uses the token against `https://ai.tjcoder.com/v1/...` and surfaces a clean error if the token is missing or expired.
- Default Ollama/Gemini paths stay exactly as they are; `cloud` is opt-in.

## 6. Schema unification

`internal/tui/session.go` and `cmd/coder/main.go` each define their own `persistedSession`. They drifted: the TUI version uses `json.RawMessage` for the transcript; the headless one uses a plain `string`. Before adding tasks/articles/auth fields, hoist the type into a new `internal/session` package and have both call sites consume it. This is a refactor prerequisite, not a nice-to-have.

## 7. TUI polish (small, parallel)

Independent of the data work, the rendering passes:

- **User-message bubble** (`renderUserMessage`): exactly 1 row of padding above the first line and 1 row below the last. The background extends to the right edge of the conversation pane with a small inset margin (re-use `marginX`) so the bubble visually "fills" the panel on long lines. Short lines stay compact; long lines get a wide bar.
- **Header trim**: drop the agent slug/title and the redundant `tools:N` line from the topmost toolbar. Keep product, version, provider, model, and a one-line status.
- **COGNITION chips**: 1 line of top padding, then a small `provider` caption (`hexDim`), then a model-name chip. The chip is solid `bgPurple` background, `hexMain` foreground, padded (1 row top/bottom, 2 columns left/right) with an outer margin of the same dimension so it sits in the pane like a real component.
- **Monochromatic highlights**: extend `highlightTranscriptText` with a small keyword set (`TODO`, `FIXME`, `error`, `note`, `thought`, file extensions) and tint using the existing `hexPurple`/`hexLavender`/`hexViolet`/`hexOrchid` ramp. No new colors.
- **Right panel**: kill the `panelGutter` between TASKS and ACTIVITY so the three read as a single column with consistent rhythm.
- **Activity timestamps**: hard-code `hexDim` for *every* entry; the spinner glyph is allowed to be `hexPurple`.

## 8. Sentinel dashboard (`../ergo/ergo-chat-dashboard`)

Out of scope for this repo but mirrored here for coordination:

- Add spacer below dashboard heading.
- Make the status bar translucent (no fill color, `backdrop-filter: blur`).
- Add a top-of-card gradient glare on every stat card; match the Connection/Update cards' background.
- Reuse the existing tenant-scoped API key for the CLI login route; do not introduce a new login flow.
- After every assistant turn, the dashboard invokes `coder -p` with the latest transcript to determine whether the model should immediately follow up (e.g. on a partial error). This is a "should-I-keep-talking?" check, not a new chat.

## 9. Out of scope

- Cloud sync of tasks/articles. Local only for now; the schemas are designed to be sync-friendly later.
- Multi-user collaboration on a single session.
- Web UI. This spec is the TUI + dashboard only.
- A real scheduler that can do "in 10 minutes." Cron is not the answer; we need a small in-process timer or `at` integration. Tracked separately.

## 10. Rollout

1. Schema unification (§6) and redecl fix.
2. `SENTINEL_BETA.md` lands with this doc.
3. Tasks system end-to-end: model + storage + panel + tool + injection.
4. `/article` command + `read_article` tool.
5. Environment context injection (§2).
6. TUI polish pass (§7) in one PR.
7. Token path + `cloud` provider (§5).
8. Sentinel dashboard work in the other repo.
