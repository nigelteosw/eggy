# Eggy web chat interface design

**Status:** Approved for implementation planning
**Date:** 2026-07-23

## Context

The web UI currently only edits config (`docs/superpowers/specs/2026-07-22-web-config-ui-design.md`
and its MCP-management extension). This spec adds a second, primary
capability: a real conversation with Eggy in the browser, sharing the same
single owner conversation Telegram already has — say something in Telegram,
see it in the browser and vice versa. It reuses the existing session-cookie
login the config UI already built; no new authentication.

This is independent of, but complementary to, the SQLite memory database
spec (`docs/superpowers/specs/2026-07-23-sqlite-memory-db-design.md`): chat
works without it (conversation still flows through the existing
`State.RecentMessages`), and the memory database's `recall_conversation`
tool becomes usable from whichever channel the model is answering through,
web included, once both ship.

## Goals

- Let the owner have a full conversation with Eggy from a browser, reading
  the same shared conversation Telegram already has.
- Full parity with what Telegram already gets: a typing indicator, live-
  editing progress messages during implementation runs, and in-chat
  Approve/Reject for approvals — not just plain send/receive.
- Reuse the existing agent loop, dispatcher, and event pipeline exactly as
  Telegram does today; the web channel is a new `ports.Channel`
  implementation, not a new way of running the agent.
- Chat is the default view after login; the config UI built previously
  becomes a secondary settings area, reachable but not the landing page.
- Stay stdlib-only, matching the rest of this project (`net/http` +
  Server-Sent Events, no WebSocket library, no new frontend dependency for
  navigation).

## Non-goals

- A new conversation history store. This spec still reads/writes through
  `State.RecentMessages`, exactly like Telegram. Durable, searchable history
  is the separate SQLite memory spec's job.
- File/image attachments, voice, or any input modality beyond text.
- Multi-user chat, presence, or per-tab identity. Single owner, single
  conversation; if two browser tabs are open, both receive every push —
  there is no concept of "which tab sent this."
- A client-side router library. Two views (chat, settings) are a local
  component-state toggle, not routed URLs.
- WebSocket. See Architecture for why SSE is enough.

## Architecture

```text
Browser tab
   |  GET /api/chat/stream (SSE, session-cookie authenticated)
   |  POST /api/chat/send   {text}
   |  POST /api/chat/approve {approval_id, approved}
   v
internal/bootstrap/chat.go (new)
   |
   +-- webchat.Hub: registry of open SSE connections for the owner,
   |   broadcasts to all of them
   v
internal/adapters/channels/webchat (new adapter package)
   implements ports.Channel: Deliver, DeliverApproval, DeliverTrackable,
   EditText, AnswerCallback, SendTyping -- each one pushes an SSE event
   to every connection the Hub currently holds open
   |
   v
multiChannel (new, internal/bootstrap) implements ports.Channel by
fanning every call out to BOTH the Telegram adapter and webchat.Hub,
so the same conversation is observable from both surfaces live
   |
   v
app.channel (was Telegram-only; becomes multiChannel once webchat is wired)
```

### Transport: SSE push, plain POST to send

`ports.Channel` (`internal/ports/ports.go:86-93`) is already push-shaped —
`Deliver`, `DeliverTrackable`, `EditText`, `SendTyping` get called from
wherever the agent loop happens to be, not in response to a request. Server-
Sent Events map to that directly: one persistent `GET /api/chat/stream`
connection per browser tab, kept open, that Eggy writes events into.
Sending a message is a separate, ordinary `POST /api/chat/send` — request,
enqueue, 202 Accepted; the actual response arrives asynchronously over the
SSE stream, the same way Telegram's webhook acknowledges a message
immediately and the real reply comes later via `Deliver`.

WebSocket was considered and rejected: it needs either hand-rolled framing or
a third-party library (there is currently exactly one non-stdlib dependency
in this whole project outside the MCP SDK, `yaml.v3`), for bidirectionality
this feature doesn't actually need — sending isn't latency-sensitive, only
receiving is, and SSE already covers that with `net/http` alone
(`http.Flusher`).

**Keepalive is required, not optional.** Railway (and most reverse proxies)
close idle HTTP connections after a timeout well under "forever." Without
periodic traffic, an open SSE stream with nothing to push will get silently
dropped at the proxy layer long before the browser notices — `webchat.Hub`
must write an SSE comment (`: keepalive\n\n`, which `EventSource` ignores as
a message but which counts as traffic) on a fixed interval — every 15–20
seconds is the conventional value — for every open connection, independent
of whatever real events are or aren't flowing.

### Multi-channel fan-out

`app.channel` today is a single `ports.Channel` (Telegram). For the web
chat and Telegram to observe the same conversation live, a small
`multiChannel` type implements `ports.Channel` by calling each method on
every registered channel in turn. Most methods (`Deliver`, `SendTyping`,
`AnswerCallback`) fan out trivially — call both, ignore what doesn't apply
(the web channel's `AnswerCallback` is a no-op; there is no Telegram-style
"answer this callback query" concept in a browser).

`DeliverTrackable`/`EditText` need care: each underlying channel generates
its own message ID for the same logical message. `multiChannel.DeliverTrackable`
calls both channels' `DeliverTrackable`, then returns a compound ID encoding
both — `"telegram:<id>|web:<id>"` — omitting either half a channel doesn't
apply to (if Telegram is unconfigured, just `"web:<id>"`). `EditText` parses
that compound ID back apart (split on `|`, then on the first `:` in each
segment) and routes each piece to the channel that produced it. Both `:`
and `|` are safe as separators only because both halves are IDs Eggy itself
generates — Telegram's message IDs are decimal integers, and `webchat`'s
generated IDs must be constrained (e.g. a hex or base36 counter) to never
contain either character; this is not parsing arbitrary or user-supplied
input. This is the one real piece of bookkeeping this design adds; the
existing behavior of Telegram-only editable progress messages during
implementation runs is what it exists to preserve now that a second channel
can be present.

### Owner identity, chat ID, and the dispatcher's Owner check

Telegram uses the numeric owner ID as `chatID`. The web channel doesn't
have an equivalent per-connection identity — there's exactly one owner and
one conversation — so `multiChannel` and `webchat.Hub` ignore `chatID` for
routing entirely: every `Deliver`/`DeliverTrackable`/etc. call broadcasts to
every currently-open SSE connection, regardless of the `chatID` argument's
value. `POST /api/chat/send` builds an `events.Message{ChatID: "", Text: ...}`
and lets the existing `decodeMessage` default the empty `ChatID` to the
configured Telegram owner ID string, exactly as it already does for any
event without one — no new decoding path needed.

Separately, and more load-bearing: `events.Event` itself carries its own
`Owner` field, which is not the same thing as `events.Message.ChatID`.
`Dispatcher.Handle` (`internal/kernel/services/dispatcher.go`) rejects any
event outright — `ErrOwnerDenied`, no side effects, nothing delivered — unless
`event.Owner` exactly equals the dispatcher's configured owner string, the
same value `NewApp` computes once as `owner := strconv.FormatInt(config.Telegram.OwnerID, 10)`
and passes to `NewDispatcher`. Every event this design enqueues —
`/api/chat/send` and `/api/chat/approve` alike — must set `Owner` to that
exact string, threaded in via `WebUIConfig.OwnerID` (see below). Getting
this wrong doesn't surface as an error the owner would ever see; the event
is just silently dropped before `handleMessage`/`handleApproval` ever run.

## Event flow

- **Sending**: `POST /api/chat/send` (behind `requireWebSession`, the same
  middleware the config API already uses) decodes `{text: string}`, builds
  `events.Event{Type: events.TypeMessage, Source: "web", Owner: <configured owner>, Payload: <marshaled events.Message{Text: text}>}`,
  and calls `app.Enqueue` — the identical entry point Telegram's webhook
  handler uses. From here it is indistinguishable from a Telegram message:
  same dispatcher, same `handleMessage`, same agent loop. The web session's
  authentication (`requireWebSession`) is the trust boundary, exactly as
  Telegram's webhook-secret validation is Telegram's — `Owner` is what lets
  the event past `Dispatcher.Handle` once it's already been authenticated,
  not a second authentication step.
- **Receiving**: whatever the agent loop already calls on `app.channel`
  (`Deliver` for a plain reply, `SendTyping` while working,
  `DeliverTrackable`/`EditText` for run progress, `DeliverApproval` for an
  approval prompt) now reaches `multiChannel`, which reaches
  `webchat.Hub`, which writes a JSON-encoded SSE event to every open
  connection: `event: message`, `event: typing`, `event: edit`,
  `event: approval`.
- **Approving**: `POST /api/chat/approve` `{approval_id, approved}` builds
  `events.Event{Type: events.TypeApproval, Owner: <configured owner>, Payload: <marshaled events.ApprovalDecision{ApprovalID: ..., Approved: ...}>}`
  and calls `app.Enqueue` — again the same entry point Telegram's callback
  handler uses, reaching the exact same `handleApproval`. No new
  approval-decision logic; only a new way to enqueue the same event shape.
  `CallbackQueryID`/`MessageID` are left empty — those are Telegram-specific
  fields `handleApproval` already treats as optional
  (`decision.CallbackQueryID != ""` is already a conditional check today,
  and `telegram.DeliverOutcome` already falls back to `Deliver` instead of
  `EditText` when `MessageID` is empty).
- **Catching up**: `GET /api/chat/history` (session-cookie authenticated,
  no request body) reads `State.RecentMessages` directly — the exact same
  bounded window Telegram already relies on, no new store — and returns it
  as `CommandResult{State: ResultSuccess, TableHeaders: ["role", "content"], TableRows: [...]}`,
  oldest first: the same `table_headers`/`table_rows` shape every other
  list in this web API already uses (providers, models, MCP servers), so
  `RenderJSON` needs no new response schema and the frontend already has a
  row-rendering pattern to copy. The frontend calls this once on initial
  load and again on every SSE reconnect (see Frontend structure), replacing
  its rendered message list wholesale rather than trying to diff against
  what it already has.

## Frontend structure

- `ChatPage.tsx` (new): on mount, opens `new EventSource("/api/chat/stream")`
  (cookies are sent automatically for same-origin `EventSource`, so no
  extra auth wiring). Listens for `message`, `typing`, `edit`, and
  `approval` events, rendering a scrolling list of messages plus a typing
  indicator. Editable messages are tracked by ID in local state so an
  `edit` event updates the existing bubble instead of appending a new one.
  An `approval` event renders an inline card with Approve/Reject buttons
  that `POST /api/chat/approve`.
- A send box at the bottom `POST`s to `/api/chat/send` and clears on
  success.
- **Navigation**: `App.tsx` changes from "always render `ConfigPage` once
  authenticated" to a local `view: "chat" | "config"` state, defaulting to
  `"chat"`, with a small settings icon/link that flips it to `"config"`. No
  router — this is the same two-states-in-one-component approach already
  used for `"checking" | "authenticated" | "unauthenticated"`.
- **Reconnection**: `EventSource` auto-reconnects natively on drop, but
  anything pushed while disconnected is lost. On every `open` event
  (including reconnects), `ChatPage` re-fetches recent history via
  `GET /api/chat/history` (new, thin wrapper reading the existing
  `State.RecentMessages`, session-cookie authenticated like everything
  else) and reconciles it against what's already rendered, rather than
  assuming the stream alone is a complete record.

## Error handling

- `POST /api/chat/send`/`/approve` reuse `writeWebResult`/`writeWebError`
  and `CommandResult.RenderJSON`, the same response envelope the config API
  already uses — no new error shape.
- If the SSE connection can't be established at all (expired session), the
  browser's `EventSource` fires an `error` event on a 401; `ChatPage`
  treats that exactly like the config cards' `SessionExpiredError` today —
  bounce back to `LoginPage`.
- A `Deliver`/`DeliverTrackable` call with zero open connections (browser
  tab closed) is not an error — the webchat channel simply has nothing to
  write to; Telegram (if configured) still receives it via `multiChannel`
  regardless.

## Testing

- `internal/adapters/channels/webchat` tests: a fake `http.ResponseWriter`
  registered with the `Hub`, proving `Deliver`/`SendTyping`/`EditText`/
  `DeliverApproval` each produce the correct SSE event shape, and that a
  broadcast reaches every registered connection.
- `multiChannel` tests: `DeliverTrackable`/`EditText` compound-ID
  encode/decode round-trips correctly, including the "only one channel
  configured" case (no `|` separator, no empty half).
- `internal/bootstrap` HTTP tests (`httptest`, matching `web_test.go`'s
  existing style): `/api/chat/send` enqueues the expected event and
  requires a session; `/api/chat/approve` reaches the same `handleApproval`
  path a Telegram callback would, proven by a shared test helper if one
  doesn't already exist; `/api/chat/stream` requires a session and delivers
  a broadcast `Deliver` call as a correctly formatted SSE frame.
- Frontend: manual verification only, matching the config UI's existing
  precedent — no test framework introduced.
- `make fmt vet test race build` must pass.

## Implementation sequence constraints

1. Add behavior test-first, starting with `webchat.Hub` and the
   `ports.Channel` implementation, since `multiChannel` and every HTTP route
   depend on it.
2. Keep all SSE framing and connection-registry logic inside
   `internal/adapters/channels/webchat`; keep `multiChannel` and route
   wiring in `internal/bootstrap`, matching the existing rule that adapters
   are registered only there.
3. Do not change `ports.Channel`'s method signatures to fit the web
   channel — every existing adapter (Telegram) implements them and must
   keep working unchanged.
4. Do not add a WebSocket dependency or a new conversation history store;
   both are explicitly out of scope per Non-goals.
5. `/api/chat/approve` must reach the exact same `handleApproval` code path
   a Telegram callback reaches — do not create a second, parallel
   approval-decision implementation.
6. Verify with `make fmt vet test race build`.
