# TJ Coder AI Models — TUI roadmap

Tracking document for where the TUI is heading. None of this is current work.

**Gate:** these items wait until the new tenant-isolation model is fully in place. They belong on a separate branch and a separate session from the `tjcoder` model cutover.

## 1. Bring your own key

Support user-supplied keys for many providers. First set:

| Provider | Priority |
|---|---|
| `tjcoder` | First. Default, and the one the TUI is built around. |
| `ollama` | Local template. |
| `ollama-cloud` | Hosted Ollama template. |
| others | After the three above are solid. |

`tjcoder` stays the priority integration. Other providers are optional keys, not a second catalogue inside the tjcoder provider. The status bar and `/model` picker keep the rule already in place: show the provider only when the model id does not already name it.

## 2. Platform commands

Tighter integration with the ergo-ai-server TJ Coder / Ergo AI platform. The TUI already has local tasks, reminders, and panes; these commands should talk to the platform instead of only the local session.

- `/tasks` — platform tasks, not only the local session list
- `/reminders` — platform reminders
- `/documents` — platform documents
- `/articles` — platform articles

Each command is a thin client of the existing server routes, scoped to the signed-in tenant once isolation is in place.

## 3. `/share`

Instant sharing of source written in the TUI with people authenticated to TJ Coder AI Labs.

Build on the existing share framework:

- `src/models/Share.ts`
- `src/routes/share.ts`
- `src/routes/cli/share.ts`

`/share` publishes the current file, selection, or session artifact through that framework. Recipients are authenticated TJ Coder AI Labs users. No new sharing model.

## 4. `/invite`

Multi-human collaboration on a TUI session.

```
/invite <email>
```

Invites N collaborators into the conversation the developer is hosting. The session is hosted by ergo-ai-server. The full transcript lives centrally and is access-controlled the same way the Ergo chat dashboard hosts a conversation: owner, invited participants, accept-by-email, revoke.

Reuse the dashboard's participant and invite flow (`invitedBy`, invite token, accept, revoke) rather than inventing a second one. The TUI is another client of that session, not a separate collaboration product.

## Order

1. Tenant isolation lands.
2. BYOK, `tjcoder` first.
3. Platform commands (`/tasks`, `/reminders`, `/documents`, `/articles`).
4. `/share` on the existing share framework.
5. `/invite` on the existing chat-dashboard participant model.
