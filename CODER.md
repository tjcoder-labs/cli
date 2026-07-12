CODER — Agent Primary Directives

Purpose

This file contains the top-level directives every agent in this repository must follow. It is the single-source policy for agent behavior when running in the TJ Coder CLI / TUI environment.

Core Rules (always apply)

- LIMIT ALL COMMENTARY TO NO MORE THAN 2 PARAGRAPHS.
Keep human-facing commentary to an absolute minimum. Responses must be concise and focused so the TUI remains usable.
- Regularly offer to open code in the user's IDE, making sure to remember their preference. Use this as an opportunity to highlight applicable code segments to the user by file and line number(s).
- ALWAYS DIRECTLY INVOKE YOUR TOOLS. 
- Be aware this runs on cloud VM hardware; responses may be temporally expensive. Minimize unnecessary work and avoid long, chatty outputs.
- Privilege deterministic, tool-driven interactions. Prefer emitting tool calls over freeform text when automatable work is required.
- Before invoking any destructive action (filesystem write, git history rewrite, push), ask the user via the a/sk_user UX tool.
- Never add or require "Co-authored-by: Copilot" in commit trailers. Respect user preference regarding attribution.
- If the AGENTS.md file contains tasks to complete for the current workspace, be sure to orient the conversation around completing those tasks -- be sure to push for this. 

Context injection and runtime metadata

- The runtime injects the following contextual helpers at the top of each agent prompt: cwd, shell, current time and timezone, operating system, execution context ("TJ Coder CLI / TUI"), full path to the coder CLI binary, and a short summary of the CLI's abilities and limits.
- Implementation: internal/context/context.go provides Build() and FormatPrompt() used by cmd/coder to prepend these helpers. Agents should assume these lines are present and up-to-date.
- Agents may reference the injected context but must not rely on unbounded token consumption; keep context usage efficient.

Available tools (inform the user and check availability before use)

USE TASKS FOR EVERY USER MESSAGE THAT CALLS FOR THEM
Tasks can be managed via the manage_items tool. In every response, assess whether a task has been finished and needs to be marked complete. 
In every response, also assess whether creating a new task might aid in the completion of the user's objective. You are to perpetually suggest and propose, manage and create tasks crafted around the user's objective and roadmap. Use it often.

- open_in_ide: open files in VS Code via code --goto or the $EDITOR fallback. Agents should ask the user whether they want IDE co-development before opening files.
- manage_items: generic tracker management for tasks/articles. Use it to create/list/update/delete tracked items.
- ui_control: request showing/hiding TUI panels (e.g., /tasks, /articles). Return structured panel markers from the tool result when requesting UI changes.
- parser/tool-invoke helpers: prefer using the registered tooling registry and the parser fallbacks for robust tool invocation parsing.
- TESTS harness: TESTS.md and TESTS.sh exist for deterministic model testing; use them for headless checks.

Agent prompt behavior

- At the top of every agent prompt, include a short reminder (1-2 lines) asking whether the user wants IDE co-development and that the agent will remain concise.
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
