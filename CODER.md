CODER — Agent Primary Directives

Purpose

This file contains the top-level directives every agent in this repository must follow. It is the single-source policy for agent behavior when running in the TJ Coder CLI / TUI environment.

Core Rules (always apply)

- LIMIT ALL COMMENTARY TO NO MORE THAN 2 PARAGRAPHS.
Keep human-facing commentary to an absolute minimum. Responses must be concise and focused so the TUI remains usable.
- Use the canvas panel (ui_control panel=canvas with path/start_line/end_line) to present relevant code, documents, drafts, and segments to the user proactively — especially to show changes you intend to make or have made. The canvas presents content, never commentary. open_in_ide is deprecated; only fall back to it if the user explicitly asks for their editor.
- ALWAYS DIRECTLY INVOKE YOUR TOOLS. 
- Be aware this runs on cloud VM hardware; responses may be temporally expensive. Minimize unnecessary work and avoid long, chatty outputs.
- Privilege deterministic, tool-driven interactions. Prefer emitting tool calls over freeform text when automatable work is required.
- Before invoking any destructive action (filesystem write, git history rewrite, push), ask the user via the a/sk_user UX tool.
- Never add or require "Co-authored-by: Copilot" in commit trailers. Respect user preference regarding attribution.
- If the AGENTS.md file contains tasks to complete for the current workspace, be sure to orient the conversation around completing those tasks -- be sure to push for this. 

Presentation control (you drive the right-hand panel)

- You are in charge of what the user sees in the right-hand pane. On every turn, proactively call ui_control (or invoke_cli_command) to surface the panel that best matches what you are doing, and re-evaluate that choice each turn instead of leaving a stale panel up.
- When the user asks about, creates, or updates tasks — or you are planning multi-step work — show the tasks panel (ui_control action=show panel=tasks, or /tasks). Keep it up while the conversation stays task-focused.
- MANDATORY: every manage_items call that creates, updates, completes, or deletes a task must be followed — in the same turn, before you reply — by ui_control (action=show, panel=tasks) so the user immediately sees the refreshed list. Do not wait to be asked.
- When you read, cite, or draft a file, open it on the canvas: ui_control action=show panel=canvas with path and (when relevant) start_line/end_line so the user reads the exact code alongside your explanation. Update the canvas as the focus moves between files or ranges.
- When the user asks about activity, tool output, memory, config, environment, or you are running commands, surface the matching panel (activity by default; /memory, /config, /environment for those).
- Prefer the smallest, most relevant panel. Do not thrash: only switch when the user's focus genuinely changes. When focus returns to plain conversation with nothing to show, fall back to the activity panel.


Context injection and runtime metadata

- The runtime injects the following contextual helpers at the top of each agent prompt: cwd, shell, current time and timezone, operating system, execution context ("TJ Coder CLI / TUI"), full path to the coder CLI binary, and a short summary of the CLI's abilities and limits.
- Implementation: internal/context/context.go provides Build() and FormatPrompt() used by cmd/coder to prepend these helpers. Agents should assume these lines are present and up-to-date.
- Agents may reference the injected context but must not rely on unbounded token consumption; keep context usage efficient.

Available tools (inform the user and check availability before use)

USE TASKS FOR EVERY USER MESSAGE THAT CALLS FOR THEM
Tasks can be managed via the manage_items tool. In every response, assess whether a task has been finished and needs to be marked complete. 
In every response, also assess whether creating a new task might aid in the completion of the user's objective. You are to perpetually suggest and propose, manage and create tasks crafted around the user's objective and roadmap. Use it often.

- open_in_ide: DEPRECATED — prefer the canvas panel. Only open files in VS Code / $EDITOR when the user explicitly requests their editor.
- manage_items: generic tracker management for tasks/articles. Use it to create/list/update/delete tracked items.
- ui_control: request showing/hiding TUI panels. Panels are 'tasks', 'activity', 'articles', and 'canvas' (render a file + optional line range for the user). Return structured panel markers from the tool result when requesting UI changes.
- parser/tool-invoke helpers: prefer using the registered tooling registry and the parser fallbacks for robust tool invocation parsing.
- TESTS harness: TESTS.md and TESTS.sh exist for deterministic model testing; use them for headless checks.

Agent prompt behavior

- At the top of every agent prompt, remind the user (1-2 lines) that you will remain concise and will present relevant content on the canvas as you work.
- When performing multi-step tasks, emit explicit, numbered steps and confirm before executing steps that persist or alter repository history.

Fault tolerance

- Validate tool names against the runtime registry before acting. When unsure about parsing a tool invocation, fall back to asking the user.
- Handle missing tools gracefully: report which tool is unavailable and suggest alternatives.

Maintenance

- Keep this file minimal. For procedural guidance or onboarding, link to internal/context, internal/tools, and internal/tracking packages.

Paths of interest

- Context injection: internal/context/context.go
- Tool registry & parser: internal/tooling/parser.go
- Manage items / trackers: internal/tools/manage_items.go, internal/tracking
- IDE helper: internal/tools/open_in_ide.go
- TUI: internal/tui/app.go

-- End CODER --
