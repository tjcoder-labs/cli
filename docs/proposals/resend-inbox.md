# Proposal: `resend` inbox check actions (`inbox`, `inbox_get`)

## Summary

Add two read-only actions to the `resend` tool so agents can check inbound
email, not just outbound. Today the tool exposes `auth | send | list | get`,
where `list`/`get` only cover **sent** emails (`GET /emails`). Resend's
"Receiving Emails" API (per `https://resend.com/openapi.json`, tag
"Receiving Emails") provides the inbound counterparts:

- `GET /emails/receiving` — list received emails
- `GET /emails/receiving/{email_id}` — retrieve a single received email
  (includes `html`, `text`, `headers`)
- `GET /emails/receiving/{email_id}/attachments` — list attachments
- `GET /emails/receiving/{email_id}/attachments/{attachment_id}` —
  retrieve a single attachment (metadata)

## Proposed tool surface

```
resend(action=inbox, limit?, after?, before?)          -> list inbound emails
resend(action=inbox_get, id)                          -> full inbound email
resend(action=inbox_attachments, id)                  -> list attachments
resend(action=inbox_attachment, id, attachment_id)    -> single attachment
```

New args: `attachment_id`, `before`.

### Arg schema changes

| Arg | Type | Used by | Notes |
|---|---|---|---|
| `action` | string | all | add `inbox \| inbox_get \| inbox_attachments \| inbox_attachment` |
| `before` | string | inbox | pagination cursor (email id); mutually exclusive with `after` |
| `attachment_id` | string | inbox_attachment | id from the email's `attachments[]` |
| `id` | string | inbox_get, inbox_attachments | received email id (UUID) |
| `limit` | number | inbox, inbox_attachments | default 20, max 100 |

## Implementation sketch (internal/tools/resend.go)

1. Extend `resendArgs` with `Before string \`json:"before"\`` and
   `AttachmentID string \`json:"attachment_id"\``.
2. Extend the `Definition()` description ACTIONS block and parameter map.
3. Add four cases to the action switch, all read-only calls via the existing
   `resendCall(ctx, token, "GET", ...)` helper:

```go
case "inbox":            // GET /emails/receiving?limit&after&before
case "inbox_get":        // GET /emails/receiving/{id}
case "inbox_attachments":// GET /emails/receiving/{id}/attachments
case "inbox_attachment": // GET /emails/receiving/{id}/attachments/{attachment_id}
```

4. Query-string building mirrors `resendList`: `limit` (default 20), optional
   `after`/`before` (reject both together).

## Safety notes

- All four actions are **read-only** (safe), consistent with the existing
  SAFETY line: "list/get are safe."
- Inbound email may contain sensitive content; the tool already returns raw
  API JSON and never logs the API key. No secrets are printed.
- No write/destructive actions added; no confirmation gating required.
- Suggest documenting in the tool description that inbox requires the
  Resend **Inbound** feature enabled on a domain (route configured) — API
  returns an empty list otherwise.

## Tests

- Unit tests in `internal/tools/resend_test.go` (create if absent):
  - arg decoding: `before` + `after` together → error on `inbox`
  - `inbox_attachment` without `id` or `attachment_id` → error
  - action dispatch: verify path construction (`/emails/receiving`,
    `/emails/receiving/{id}`, nested attachment path) via httptest server
    asserting the request URL and Authorization header.

## Alternatives considered

- **Polling via webhooks** (`/webhooks`): rejected — requires a publicly
  reachable endpoint, not suitable for a CLI agent.
- **Single `inbox` action with `id` overloading**: rejected — ambiguous
  dispatch; four explicit actions keep the parser simple and predictable.
- **IMAP client**: rejected — out of scope for Resend-first tooling; would
  add a heavy dependency.

## Open questions

1. Should `inbox` results be summarized (e.g. unread/flag) or raw JSON like
   `list`? Proposal: raw JSON for consistency.
2. Attachment content download (binary) — out of scope; `inbox_attachment`
  returns metadata only per current Resend spec.