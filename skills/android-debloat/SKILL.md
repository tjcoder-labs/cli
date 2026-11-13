# Proposal: stored, reusable agent skills

## Summary

Add **skills**, not an Android-specific debloat tool. A skill is a versioned
Markdown playbook with a small metadata header, discovered by the CLI and loaded
into an agent only when relevant. The first skill is Android debloat; its
operational knowledge lives in the debloat workspace's `DEBLOAT.md`.

The CLI already supports stored workspace agents in
`.ergo-cli-go/agents.json` and auto-injects workspace `AGENTS.md`/`CODER.md`.
Those mechanisms are useful precedents but are not skill support: an agent is a
persona/tool selection, and repository instructions are always loaded and capped
at 12,000 characters. Do not put the whole device-specific playbook in the global
Android assistant prompt.

## Skill format and storage

Use a conventional directory with one required Markdown file per skill:

```text
~/.config/tjcoder/coder-cli/skills/
  android-debloat/
    SKILL.md
    references/
      motorola-nevada-tracfone.md
```

Workspace-local skills can live under `.ergo-cli-go/skills/` and override or
supplement user skills by name. The global location follows `appdir.Dir()` rather
than introducing another configuration root. Initially ship documentation and a
loader; optionally ship `android-debloat` as a built-in embedded skill.

`SKILL.md` starts with simple YAML front matter:

```yaml
---
name: android-debloat
description: Audit and plan conservative ADB debloating; use for bloatware,
  preinstalled apps, carrier bundles, package removal, disable, or rollback.
version: 1
---
```

The body contains only portable procedure, decision criteria, safety rules, and
links to references. Device inventories and personal observations are separate
reference documents, not automatically injected on every Android conversation.
`DEBLOAT.md` can be split into `SKILL.md` plus device references, or copied into
the skill with attribution and a clear source/version. Prefer one maintained
source over silently diverging copies.

Keep the format deliberately small: Markdown body, front matter, optional
references. No scripts, executable plugins, package manager, remote fetching,
automatic shell evaluation, or Android-specific Go tool. A skill never grants new
tools or permission by itself.

## Discovery and activation

1. At turn setup, discover user and workspace skills, parse metadata, and inject
   only each skill's name, short description, version, and storage identifier.
2. Activate a skill when the user names it explicitly or the request clearly
   matches its description. The agent can read its stored Markdown through the
   normal scoped file tools; alternatively add a read-only `use_skill` tool that
   returns the selected body. Prefer the latter for predictable behavior.
3. Inject an activated skill for that turn, rather than all skill bodies. Include
   the source and version in the context for traceability. Cache parsed metadata,
   but do not silently cache stale file contents across edits.
4. Read device/reference files only when that device or question is relevant.
   Do not infer consent to execute a workflow merely from loading its skill.

Example prompt addition:

```text
Available skills:
- android-debloat (v1): conservative ADB audit and debloat planning.
Load a skill when explicitly requested or clearly relevant. Skills are
instructions, not authorization to mutate a device.
```

The existing `android-assistant` prompt should reference the skill workflow:
default to read-only discovery, distinguish observations from recommendations,
obtain approval before mutations, use small batches, and record rollback steps.
Other agents with file access can use the same skill; do not duplicate the entire
playbook into every persona.

## Minimal implementation

- `internal/skills/`: front-matter parser, path validation, discovery, workspace
  precedence, size limits, and versioned content loading.
- `internal/tooling/runner.go`: inject metadata catalog and activated skill body
  alongside existing agent/repository context.
- `internal/tools/`: optional read-only `use_skill` action/tool; no execution.
- `internal/agent/agent.go`: concise Android-assistant guidance and necessary
  file-reading tools; no embedded device inventory.
- `cmd/coder/`: optional `skill list` and `skill show NAME`; a `/skills` picker
  can come later. No new mutating ADB operation is required.
- Document user/workspace paths, precedence, front matter, and trusted-source
  expectations in `README.md`.

Treat skill files as trusted local instructions: workspace skills can affect the
model just like `AGENTS.md`. Display their origin, bound description/body sizes,
reject malformed names/path traversal, and do not execute embedded commands while
loading. User approval rules for `ask_user`, device mutations, and other tools
remain unchanged.

## Android debloat skill content

The portable playbook should preserve the useful parts of `DEBLOAT.md`:

- read-only inventory and device/user confirmation;
- installer evidence rather than universal package blacklists;
- owner review of personal apps and uncertainty;
- disable/enable preferred over destructive operations;
- no root, bootloader, reset, or bulk scripts by default;
- exact change log, state-based verification, and rollback;
- protected telephony, provisioning, security, accessibility, and core services.

The Motorola `nevada`/Tracfone snapshot belongs in a separate reference, with an
audit date and instruction to re-inventory. It must never become a generic removal
list for all Android phones.

## Testing and acceptance

Unit-test front matter, missing/malformed skills, duplicate names and precedence,
path containment, size caps, UTF-8 content, and stale-cache behavior. Runner tests
should prove that inactive skills do not inject their bodies, explicit/relevant
activation loads the correct version, and loading a skill grants no approval.
Existing `go test ./...` must pass. No Android device is needed for skill tests.

## Deferred

Skill marketplaces/downloads, executable hooks, automatic activation confidence
scores, new Android tools, and a dedicated UI editor are out of initial scope.
Start with a conventional stored Markdown library, catalog injection, and lazy
loading. This is reusable across Android, cloud, release, and other workflows.
