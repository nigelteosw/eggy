# Eggy multi-thread web chat and independent channels design

**Status:** Approved for implementation planning
**Date:** 2026-07-23

## Context

An earlier design (since superseded, was
`docs/superpowers/specs/2026-07-23-web-chat-interface-design.md`) built a
web chat surface that shared one single global conversation with Telegram —
a `multiChannel` type fanned every reply out to both surfaces so a message
sent on either side appeared on both. That assumption was wrong: Telegram
and the web UI are independent channels into the same underlying agent
core, not mirrors of one conversation. Telegram keeps operating on its own
single, fixed, continuous thread exactly as it does today. The web UI gets
multiple independently-resumable conversation threads — a sidebar of past
and current threads, each its own conversation, the model free to do
whatever a thread's content calls for (general chat, a coding task, a
calendar action) with no structural distinction between "just chatting"
and "doing a coding run": that distinction already barely exists today (see
below) and this design does not introduce one for threads either.

This spec also formalizes something already true architecturally but
worth stating plainly: a coding run is not a separate kind of thread. The
model decides, inside an ordinary conversation turn, to call
`repository_modify`/`repository_continue` — the same as it decides to call
any other tool. A thread named "Debug failing CI on arm64" and a thread
named "Draft the launch thread" are the same kind of object; one just
happens to involve the model calling repository tools.

## Goals

- Telegram and the web UI are independent channels: a message sent on one
  never appears on, or affects, the other. Both still exercise the exact
  same dispatcher, agent loop, tools, and approval engine — "plugins" into
  one shared core, not two separate assistants.
- The web UI supports multiple, independently-resumable conversation
  threads, each with its own scoped message history, listed in a sidebar,
  switchable, creatable. Telegram continues to operate on a single fixed
  thread, invisible from the web UI.
- No structural split between "conversation" and "coding run" — a thread is
  a thread; whatever the model does inside one (including a coding run) is
  just tool calls within that thread's turn, exactly as it already works
  today for Telegram.
- Tool activity that happens mid-turn (e.g. a coding run's progress) stays
  visible inline in the thread as it happens, not just as a final result —
  reusing the existing `DeliverTrackable`/`EditText` progress-message
  mechanism Telegram already gets, now scoped per web thread.
- New threads are auto-titled from their content; no manual naming step.

## Non-goals

- Rename, delete, or pin/unpin endpoints for threads. Create, list, and
  switch are enough to make the sidebar functional; those three are a
  natural low-risk follow-on once this model exists.
- The rest of the reference UI shell this was inspired by: a Skills panel,
  an Artifacts panel, a Messaging nav item, a command palette, or a
  status bar (tokens/session time/gateway/cron). Each of those is its own
  feature and gets its own spec later. This spec is threads + sidebar +
  chat panel only.
- Merging the coding-run implementation loop into the same `agent.Loop`
  instance or message list as the outer conversation. A coding run
  continues to be a second, independently-configured loop
  (`app.implementationLoop`) with its own tools/prompt/step-budget and its
  own durable per-run transcript (`ImplementationSession`,
  `data/sessions/<id>/*`) — the outer thread only ever sees the run's
  tool-result summary plus whatever progress messages get delivered along
  the way. Collapsing that into one loop is a much deeper change with no
  clear benefit here, and today's tool-call boundary already achieves "no
  structural split visible to the user."
- Making Telegram's fixed thread visible or readable from the web UI.
  They're independent; you'd check Telegram itself for that history.
- A client-side router library. Switching threads is still local component
  state, not a routed URL.
- Any data migration for the last ~20 messages currently sitting in
  `state.json`'s global `RecentMessages` — dropped, not migrated (see
  Rollout).

## Architecture

```text
Telegram webhook                    Browser tab (one thread open)
   |                                   |  GET  /api/chat/threads
   |                                   |  POST /api/chat/threads
   |                                   |  GET  /api/chat/threads/{id}/history
   |                                   |  GET  /api/chat/threads/{id}/stream (SSE)
   |                                   |  POST /api/chat/threads/{id}/send
   |                                   |  POST /api/chat/approve
   v                                   v
        events.Event{Source, Owner, Payload: events.Message{ChatID: <thread-id-or-empty>, Text}}
                          |
                          v
                  app.Enqueue -> Dispatcher -> App.processEvent -> handleMessage
                          |
        destination := destinationFromEvent(event)   // {Kind: "telegram"} or {Kind: "web", ThreadID}
        ctx := withDestination(ctx, destination)      // carried for the rest of this turn
                          |
                          v
              agent.Loop.RunSelected(ctx, ...)  -- context/history built from
              SQLite `messages` WHERE conversation_id = destination's thread id
                          |
              (model may call any tool, including repository_modify,
               exactly as it decides to call any other tool)
                          |
                          v
              a.channel  (routedChannel, implements ports.Channel)
                 reads destination back out of ctx on every call
                 |                                  |
                 v                                  v
          telegram.Client                    webchat.Channel
          (real Telegram chat ID)            (thread-scoped SSE push
                                               via webchat.Hub)
```

### Thread identity and storage

`internal/adapters/memory/sqlite` gains a `threads` table:
`id, title (nullable until auto-titled), channel ('web'|'telegram'),
created_at, updated_at`. The existing `messages.conversation_id` column
(already in the schema, previously hardcoded to `'owner'` by every
`WriteMessage` call) becomes the real thread ID: every web thread gets its
own generated ID at creation time; Telegram always uses one fixed, reserved
ID (`"telegram"`) that is never returned by the thread-listing query
(`WHERE channel = 'web'`).

`State.RecentMessages` — today a single global, unpartitioned slice
(`internal/ports/ports.go:189`), the actual live per-turn context window —
is retired entirely. It was already flagged (TODO.md's prior fast-follow
note) as the real blocker to thread-scoping: tagging SQLite rows with
`conversation_id` alone doesn't make the agent's turn context
thread-aware if the live window is still one global JSON-file list. Since
SQLite (`internal/adapters/memory/sqlite`) is already mandatory — always
opened in `NewApp`, not a feature flag — it can safely become the *single*
source of truth for both the durable log and the live recent-window: a
turn's context is built with `SELECT ... WHERE conversation_id = ?
ORDER BY id DESC LIMIT N`, scoped to whichever thread (Telegram's fixed one,
or the web thread the incoming message belongs to) is active for this turn.
This removes the two-stores-that-can-drift problem in one move instead of
adding thread-scoping on top of it.

`ConversationService.Record`/reset take a thread ID; `/clear`-style resets
become per-thread (clearing one thread's window, not a global wipe).
`ConversationService` no longer needs `ports.StateStore` at all once
`State.RecentMessages` is gone — the implementation plan should confirm
nothing else in that service depends on it.

**Auto-titling:** once a thread's first exchange completes, if its title is
still null, derive one from the first user message via cheap truncation
(no separate model call) and persist it. Good enough for v1; a
model-generated title is a clear, easy upgrade later if the truncated
version reads poorly in practice.

### Channel routing: destination carried via context, not fan-out

`multiChannel` (built for the superseded design) fanned every `ports.Channel`
call to both Telegram and web. That's exactly the behavior being removed.
Replacing it: `routedChannel` (`internal/bootstrap`), which implements
`ports.Channel` by reading a `destination` value out of the incoming
`context.Context` on every method call and forwarding to *only* the
matching underlying channel.

Why context and not a field on `App`: `App.Run`'s event loop spawns a
goroutine per dequeued event (`internal/bootstrap/app.go:716-719`), so a
live web message, a Telegram message, and a scheduled heartbeat can
genuinely execute concurrently. A single mutable "current channel" field
would let concurrent turns clobber each other's destination. Every
`ports.Channel` method already takes `context.Context` as its first
parameter, and every tool's `Execute(ctx, ...)` receives that same per-turn
`ctx` — so this requires no interface changes (`ports.Channel`'s method
signatures stay exactly as they are) and no changes to how tools are
constructed; only `handleMessage` needs to set the destination once, at the
top of a turn, derived from the triggering `events.Event`'s `Source` and
(for web) the thread ID carried in `events.Message.ChatID` — an existing
field, not a new one; web populates it with the thread ID instead of
leaving it empty for the owner-ID default.

`webchat.Hub`/`webchat.Channel` become thread-aware: `Register` takes a
thread ID (from the SSE connection's URL), and `Broadcast` only reaches
connections currently registered for the target thread.

**The `chatID` parameter every `ports.Channel` method already takes is
superseded by `ctx`, not combined with it.** Existing call sites (the
progress tracker, `calendarTools`, `skillProposeTool`, `CommandService`)
were all constructed once at `App` startup with a fixed value baked in —
the Telegram owner ID string — because until now there was only ever one
destination. `routedChannel` ignores whatever `chatID` a caller passes and
resolves the real target entirely from the `ctx`-carried `destination`:
Telegram's real chat ID is the same configured owner-ID constant every
other Telegram call already uses (there is only one owner, so this needs
no new plumbing), and web's target is `destination.ThreadID`. This means
none of those existing constructors need to change at all — they can keep
passing their old fixed value, since `routedChannel` never reads it.

**Approvals need one more piece.** The eventual approve/reject decision
arrives later as a brand-new, unrelated dispatcher event (`TypeApproval`) —
by then there is no ambient `ctx` left from the turn that requested it.
`approvals.Approval` gains a `Destination` field, stamped once at
request-time (same context-read mechanism, since `ApprovalService.Request`
already receives `ctx` and needs no signature change). `App.handleApproval`
(`internal/bootstrap/app.go:652-678`) then routes the outcome using
`approval.Destination` instead of today's hardcoded Telegram owner ID
(`app.go:654`) — which, notably, is the reason multi-channel fan-out
"worked" for web only by accident: `webchat.Channel` ignored `chatID`
entirely, so broadcasting to everyone stood in for real routing. That
accident goes away once channels are independent.

`AnswerCallback` keeps forwarding to Telegram unconditionally — only
Telegram ever produces a callback-query ID; there is no ambiguity to
route.

### Web API surface

- `GET /api/chat/threads` — list web threads (`id, title, updated_at`),
  most-recently-active first, for the sidebar.
- `POST /api/chat/threads` — create a new, untitled thread; returns
  `{id}`. This is what the sidebar's "New" action calls.
- `GET /api/chat/threads/{id}/history` — replaces the old global
  `/api/chat/history`, scoped to one thread.
- `GET /api/chat/threads/{id}/stream` — replaces the old global
  `/api/chat/stream`; SSE, thread-scoped per the Hub changes above.
- `POST /api/chat/threads/{id}/send` — replaces the old global
  `/api/chat/send`, scoped to one thread. Builds
  `events.Event{Type: events.TypeMessage, Source: "web", Owner: <configured owner>, Payload: events.Message{ChatID: <thread id>, Text: text}}`.
- `POST /api/chat/approve` — unchanged shape (`{approval_id, approved}`);
  no thread ID in the request, since the approval record already carries
  its destination.

All routes stay behind the existing `requireWebSession` middleware,
unchanged.

## Event flow

- **Sending (web):** `POST /api/chat/threads/{id}/send` decodes `{text}`,
  builds the event above with `ChatID` set to the thread ID, and calls
  `app.Enqueue` — the same entry point Telegram's webhook uses. At the top
  of `handleMessage`, the destination (`{Kind: "web", ThreadID: id}`) is
  derived from the event and attached to `ctx` for the rest of the turn.
- **Sending (Telegram):** unchanged from today, except the destination
  attached to `ctx` is `{Kind: "telegram"}`, and the turn's context is read
  from SQLite `WHERE conversation_id = "telegram"` instead of the old
  global `state.RecentMessages`.
- **Receiving:** whatever the agent loop calls on `a.channel` (`Deliver`,
  `SendTyping`, `DeliverTrackable`/`EditText` for progress, including a
  coding run's), `routedChannel` reads the destination back out of `ctx`
  and forwards to exactly one underlying channel — Telegram's real chat ID,
  or `webchat.Hub` scoped to the originating thread.
- **Approving:** `POST /api/chat/approve` is unchanged in shape and still
  reaches the exact same `handleApproval` a Telegram callback reaches; the
  only change is `handleApproval` now looks up `approval.Destination`
  (stamped at request-time) instead of assuming Telegram.
- **Catching up:** `GET /api/chat/threads/{id}/history` reads that
  thread's SQLite window directly (oldest first), same
  `CommandResult{TableHeaders: ["role","content"], TableRows: [...]}` shape
  the rest of the web API already uses. Called on initial thread open and
  on every SSE reconnect, same reconcile-by-refetch pattern as before.

## Frontend structure

- `ThreadSidebar.tsx` (new): fetches `GET /api/chat/threads` on mount;
  renders title + relative time per row; a "New" action at top calls
  `POST /api/chat/threads` and switches to the returned thread.
- `App.tsx` gains an `activeThreadID: string | null` alongside its
  existing `view`/`status` state. Selecting or creating a thread sets it;
  zero threads yet is a first-run empty state.
- `ChatPage.tsx` becomes thread-aware: takes `threadID` as a prop; its
  `EventSource`/history-fetch/send calls target the thread-scoped routes.
  Switching threads tears down the old `EventSource` and opens a new one —
  the existing connect-on-mount `useEffect` just gains `threadID` in its
  dependency array instead of running once.
- Inline tool activity (a coding run's progress showing up mid-thread, not
  just as a final result) needs no new mechanism: `ChatPage.tsx` already
  handles `edit` events from `DeliverTrackable`/`EditText`; this now simply
  reaches the correct thread via the routed channel instead of every open
  connection.
- Auto-titling is a backend concern; simplest v1 frontend behavior is
  re-fetching the thread list after a new thread's first message resolves,
  no push mechanism required.

## Error handling

- Same envelope as the rest of the web API (`writeWebResult`/
  `writeWebError`, `CommandResult.RenderJSON`) — no new error shape.
- A thread ID that doesn't exist (deleted out from under an open tab, or
  malformed) on any `/api/chat/threads/{id}/*` route returns 404 via
  `writeWebError`.
- SSE `error` events on a 401 are handled exactly as before — bounce to
  `LoginPage`.
- A `Deliver`/`DeliverTrackable` call for a thread with zero open
  connections is not an error; nothing is listening, nothing is lost
  (the next `history` fetch on reconnect catches it up).

## Testing

- `internal/adapters/memory/sqlite`: `threads` table CRUD, and
  `WriteMessage`/a new thread-scoped recent-window query actually filtering
  by `conversation_id`.
- `internal/bootstrap`: `routedChannel` tests — a call with a `"telegram"`
  destination in `ctx` reaches only the Telegram fake; a call with a
  `{"web", threadID}` destination reaches only the web fake, scoped to that
  thread ID; an approval's stored `Destination` correctly routes
  `handleApproval`'s outcome delivery regardless of which channel requested
  it.
- `internal/adapters/channels/webchat`: `Hub.Register`/`Broadcast` scoped
  by thread ID — a connection registered for thread A never receives a
  broadcast targeted at thread B.
- `internal/bootstrap` HTTP tests: thread create/list/history/send/stream
  round-trip; `/api/chat/approve` still reaches the same `handleApproval`
  path a Telegram callback would.
- Frontend: manual verification only, matching existing precedent.
- `make fmt vet test race build` must pass.

## Rollout

No data migration. This is pre-launch with a single owner: the last ~20
messages in `state.json`'s global `RecentMessages` are dropped, not
carried over — everything of substance is already durably logged in SQLite
under `conversation_id = 'owner'` from before this change, and those old
rows simply stay orphaned (never surfaced by the new thread-scoped
queries, harmless). No existing web thread exists yet to migrate either,
since no multi-thread web UI has shipped before this.

## Implementation sequence constraints

1. SQLite `threads` table and thread-scoped message queries first — every
   other piece (routing, routes, frontend) depends on thread IDs existing
   and being queryable.
2. `routedChannel` and the context-carried destination next, replacing
   `multiChannel` outright rather than running both side by side.
3. Web routes and `webchat.Hub` thread-scoping together, since the routes
   are what supplies thread IDs to the Hub.
4. Frontend sidebar + thread-aware `ChatPage` last.
5. Do not change `ports.Channel`'s method signatures.
6. `/api/chat/approve` must keep reaching the exact same `handleApproval`
   code path a Telegram callback reaches.
7. Verify with `make fmt vet test race build` at each step.
