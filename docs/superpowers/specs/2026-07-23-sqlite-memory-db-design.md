# Eggy SQLite memory database design

**Status:** Approved for implementation planning
**Date:** 2026-07-23

## Context

Eggy currently keeps conversation history as a bounded, in-memory/file-backed
window: `State.RecentMessages` (`internal/ports/ports.go`) holds only the last
N messages, persisted inside `state.json`. There is no full-text or semantic
search over anything older than that window, and `TODO.md`'s "P1: Harden
durable context and recall" section says explicitly:

> Design bounded, file-backed conversation search before implementing it; do
> not add a database solely for transcript recall.

That was a deliberate decision, restated in the MCP memory comparison notes:
*"Eggy stays file-backed, single-volume, and provider-neutral rather than
adding a database, search index, or skills runtime purely to match Hermes."*
This spec knowingly reverses that specific piece of it, at the owner's
explicit direction, because durable, searchable conversation history is now a
building block the web chat interface (see the in-progress chat design)
depends on to be useful across sessions — not a database added "purely to
match Hermes." `TODO.md`'s "P1: Harden durable context and recall" section
must be updated to match once this ships (see Implementation sequence
constraints).

This is additive, not a replacement: `config.yaml`, `state.json` (approvals,
schedules, repositories, agent runtime state), and the curated
`SOUL.md`/`USER.md`/`MEMORY.md` documents are unchanged. What moves into the
new database is the durable conversation transcript and the machinery to
search it.

## Goals

- Persist every conversation turn (not just the bounded recent window)
  durably, across restarts, keyed by role/source/timestamp.
- Make that history searchable two ways: full-text (exact/keyword) and
  semantic (embedding similarity), so the agent can recall relevant past
  context on request rather than only ever seeing the last N messages.
- Keep the storage engine embedded, file-backed, and single-binary-friendly —
  no external database service, no CGO (this project builds with
  `CGO_ENABLED=0`; see Architecture for why that constrains the driver
  choice).
- Make semantic search an optional enhancement: conversation storage and
  full-text search work with zero configuration; vector search activates
  only once an embeddings provider is configured, following the same
  "unconfigured = feature absent, not a boot failure" pattern as MCP and
  Calendar.
- Keep recall bounded, redacted, and explicitly presented as historical
  context rather than current authority — the existing standing constraint
  for recalled excerpts, unchanged by moving from "file-backed" to
  "database-backed."

## Non-goals

- Migrating `config.yaml`, `state.json`, or the curated
  `SOUL.md`/`USER.md`/`MEMORY.md` documents into SQLite. Those keep their
  current file formats and locking.
- A heavyweight or external vector database (Pinecone, Weaviate, pgvector,
  a separate search service). SQLite only, matching the existing "exactly
  one `eggyd` replica, file-backed state" constraint.
- Real ANN indexing (HNSW, IVF, product quantization) in this iteration.
  Similarity search is brute-force cosine over stored embeddings, computed in
  Go. This is a deliberate, explicit tradeoff (see Architecture) — acceptable
  at a single owner's realistic history size (thousands to tens of thousands
  of messages, not millions), with a documented upgrade path if that stops
  being true.
- Automatically injecting recalled history into every turn's context.
  Recall is an explicit, on-demand tool call, not ambient context stuffing —
  this keeps context budgets predictable and matches the existing
  `skill_read` on-demand pattern rather than the always-injected
  `SOUL.md`/`USER.md`/`MEMORY.md` layer.
- Semantic search over skills, code, or repository content in this
  iteration. The schema is generic enough to extend to those later (see
  Future extensions), but this spec implements conversation-message recall
  only.
- Choosing a CGO-based driver (`mattn/go-sqlite3`) plus a native vector
  extension (`sqlite-vec`). Considered and rejected — see Architecture.

## Architecture

```text
Agent turn completes (Telegram, web chat, heartbeat, or scheduled)
        |
        v
ports.MemoryStore (new, provider-neutral port)
        |
        +-- WriteMessage(...)              -- synchronous, every turn
        |
        v
internal/adapters/memory/sqlite (new adapter package; storage only)
        |
        +-- messages table (role, content, source, created_at, embedding)
        +-- messages_fts (FTS5 virtual table, keyword search)

services.MemoryEmbeddingWorker (new kernel service; orchestration)
        |
        +-- polls ports.MemoryStore.PendingEmbeddings(...)
        +-- calls ports.Embedder.Embed(...)
        +-- writes results back via ports.MemoryStore.SetEmbedding(...)
                |
                v
        internal/adapters/models/openaicompat gains an Embed method
        implementing ports.Embedder against a configured embeddings endpoint
```

The embedding worker deliberately lives in `internal/kernel/services`, not
inside the SQLite adapter: it orchestrates between two ports
(`MemoryStore` and `Embedder`), and this codebase's existing pattern for
"coordinate multiple ports" is a kernel service (e.g. `CodingService`
orchestrating `Runner` + `CodingRepository` + `Implementer`), not one
adapter reaching into another. The SQLite adapter package stays storage-only
— it never holds a reference to an `Embedder`.

Embeddings reuse `internal/adapters/models/openaicompat` (an added `Embed`
method on the existing `Model` type, hitting `/embeddings` instead of
`/chat/completions`) rather than a new sibling package. It's the same
OpenAI-compatible wire protocol and the same HTTP-client/credential
plumbing that package already owns; a separate `internal/adapters/embeddings/`
package would just duplicate that setup for a different config section.

### Driver choice: `modernc.org/sqlite`, no CGO

Eggy's Dockerfile builds with `CGO_ENABLED=0` today (`Dockerfile:15-17`), and
nothing else in this project uses CGO. Two real options exist for SQLite in
Go:

1. **`mattn/go-sqlite3`** (CGO, links the real SQLite C library) — lets you
   load the `sqlite-vec` extension for native, SQL-level vector search with
   real ANN indexing. Requires a C toolchain in the build image, breaks
   `CGO_ENABLED=0`, complicates cross-compilation, and is a bigger shift to
   this project's build story than the feature itself justifies.
2. **`modernc.org/sqlite`** (pure Go, SQLite's C source transpiled to Go via
   the `cc`/`ccgo` toolchain, published separately) — no CGO, works with the
   existing build. It has no mechanism to load native C extensions like
   `sqlite-vec`, since it isn't linking against a real `libsqlite3` a
   `.so`/`.dylib` extension could attach to. Recent versions build FTS5 in by
   default, but this has not been verified against the exact version this
   project will pin — **Task 1 of the implementation plan must confirm FTS5
   works against the pinned `modernc.org/sqlite` version before anything else
   is built on top of it**; if it turns out not to be available, keyword
   search falls back to a plain `LIKE`/substring query, which is a schema
   change worth catching before, not after, other tasks depend on FTS5.

This spec picks **option 2**. Vector similarity search is therefore
implemented at the application layer: embeddings are stored as `BLOB`
columns (raw little-endian `float32` arrays), and a query embeds its own
text, then computes cosine similarity in Go, keeping the top-K. This is
brute-force — O(n) per query — which is the explicit, accepted cost of
staying CGO-free.

Candidates for a similarity query are **not** an unbounded full-table scan:
`SearchSimilar` always takes a `limit` and an implicit recency bound — the
adapter scores only the most recent `N` embedded messages (`N` configurable,
default a few thousand) rather than deserializing the entire embeddings
table on every call. At realistic single-owner history sizes this is still
comfortably fast (a few thousand 1536-dimension float32 comparisons is
low-single-digit milliseconds of Go computation), but it also caps the
amount of BLOB data read off disk per query as history grows into the tens
of thousands of rows, instead of that cost growing unbounded forever. If
conversation history ever grows large enough for even that to matter
(realistically not for a single owner's personal assistant), the documented
upgrade path is revisiting option 1, or moving just the embedding index to a
purpose-built local vector store, without touching the rest of this design.

### New port

```go
// internal/ports/ports.go, alongside StateStore, ContextStore, SkillsStore
type MemoryStore interface {
    WriteMessage(ctx context.Context, message StoredMessage) error
    SearchText(ctx context.Context, query string, limit int) ([]StoredMessage, error)
    SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]StoredMessage, error)
    // PendingEmbeddings and SetEmbedding exist only for
    // services.MemoryEmbeddingWorker; the SQLite adapter itself never calls
    // an Embedder.
    PendingEmbeddings(ctx context.Context, limit int) ([]StoredMessage, error)
    SetEmbedding(ctx context.Context, id int64, embedding []float32) error
}

type StoredMessage struct {
    ID        int64
    Role      Role
    Content   string
    Source    string    // "telegram" | "web" | "heartbeat" | "scheduled"
    CreatedAt time.Time
}

// internal/ports/ports.go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
}
```

`internal/adapters/memory/sqlite.Store` implements `MemoryStore`. It is
registered only in `internal/bootstrap`, matching every other adapter, and
never imports or references `Embedder`.

### Schema

```sql
CREATE TABLE IF NOT EXISTS messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT    NOT NULL DEFAULT 'owner',
    role            TEXT    NOT NULL,
    content         TEXT    NOT NULL,
    source          TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    embedding       BLOB
);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content, content='messages', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;
```

`conversation_id` is hardcoded to `'owner'` today (Eggy is single-owner,
single conversation, matching the "same shared conversation" decision for
the web chat), but exists as a column now rather than being added as a
migration later if that ever changes.

The database file lives at `<data_dir>/eggy.db` (sibling to `state.json`,
`skills/`, `mcp/`), opened with `journal_mode=WAL` and a `busy_timeout` —
SQLite's single-writer model is a natural fit for the existing "exactly one
`eggyd` replica" constraint, not a new risk.

### Embeddings

`internal/adapters/models/openaicompat` gains an `Embed` method on its
existing `Model` type, implementing the `Embedder` port defined above
against a configured OpenAI-compatible `/embeddings` endpoint — the same
HTTP-client and credential-resolution plumbing that package already has for
chat completions, just a different endpoint and request/response shape.
Configuration mirrors `providers`/`models`:

```yaml
embeddings:
  provider: openrouter
  model: text-embedding-3-small
  dimensions: 1536
```

If `embeddings` is absent from config, `internal/bootstrap` does not
construct an `Embedder` or a `services.MemoryEmbeddingWorker` — messages
still get written and are still full-text searchable, only semantic
(`SearchSimilar`) recall is unavailable. This mirrors exactly how MCP
servers or Calendar behave when unconfigured: absence is not a startup
failure.

### Data flow

- **Write path**: after every completed conversation turn — Telegram, the
  new web chat, or (per existing constraints) never for heartbeat/scheduled
  turns that must not persist as first-person conversation — the owning
  service calls `MemoryStore.WriteMessage` for the user message and the
  assistant's response. This is synchronous and best-effort: a write failure
  is logged, not fatal to the turn (matching how existing state writes are
  already handled defensively elsewhere).
- **Embedding path**: `services.MemoryEmbeddingWorker`, using the same
  periodic-loop machinery the scheduler/heartbeat already use, calls
  `MemoryStore.PendingEmbeddings` (only constructed when an `Embedder` is
  configured), batches the results, calls `Embedder.Embed`, and writes each
  vector back via `MemoryStore.SetEmbedding`. This keeps embedding latency
  and provider availability off the live conversation turn, and keeps the
  SQLite adapter itself free of any dependency on the embeddings port.
- **Recall path**: a new agent tool, `recall_conversation`, lets the model
  search past conversation on request:
  full-text via `SearchText`, semantic via `SearchSimilar` when available.
  Results are passed through the same redaction path implementation-session
  progress already uses (`SecretGuard`-style scrubbing) before ever reaching
  the model, are bounded (a fixed max excerpt count/length), and are
  explicitly framed in the tool result as historical context, not current
  instructions — preserving the existing standing constraint verbatim.

## Testing

- Adapter tests (`internal/adapters/memory/sqlite`) against a `t.TempDir()`
  SQLite file (not `:memory:`, so WAL-mode behavior is exercised the way
  production actually runs): insert/read round-trip, FTS5 keyword search
  relevance (or the `LIKE` fallback if Task 1 finds FTS5 unavailable),
  cosine-similarity ranking correctness against known vectors, and that
  `SearchSimilar` only scans the bounded recency window, not the whole
  table.
- `services.MemoryEmbeddingWorker` tests (fake `MemoryStore` + fake
  `Embedder`): rows get embedded exactly once, a restart mid-batch doesn't
  duplicate or skip work, and the worker never runs at all when no
  `Embedder` is configured.
- `openaicompat.Model.Embed` tests against a fake HTTP server, matching the
  existing chat-completion tests' style in the same package.
- Bootstrap/service tests proving: recalled excerpts pass through redaction,
  recall is never auto-injected into ordinary turn context, and
  heartbeat/scheduled turns never write to conversation history.
- `make fmt vet test race build` must pass; the SQLite driver's cgo-free
  build must be verified explicitly (`CGO_ENABLED=0 go build ./...` in CI/
  Docker, which the existing Dockerfile already does).

## Implementation sequence constraints

1. First confirm FTS5 actually works against the pinned `modernc.org/sqlite`
   version (a throwaway spike is fine); only then add behavior test-first for
   the real schema/migration and insert/read round-trip, since everything
   else depends on it.
2. Keep all SQL, the `modernc.org/sqlite` driver usage, and schema
   management inside `internal/adapters/memory/sqlite`, with no reference to
   `Embedder` anywhere in that package. Keep the embed/poll/write-back
   orchestration inside `services.MemoryEmbeddingWorker`, not inside the
   adapter. Wire construction only in `internal/bootstrap`.
3. Do not change `ports.StateStore`, `config.yaml`'s existing sections
   (`server`, `telegram`, `repositories`, `runner`, `calendar`, `mcp`, ...),
   or migrate any existing file-based state into SQLite.
4. Conversation storage and full-text search are unconditionally available
   once this ships (a new `eggy.db` is created automatically, the same way
   `state.json` is today) — no feature flag, no opt-out. Semantic search is
   the only conditionally-available piece, gated on `embeddings` being
   configured.
5. Update `TODO.md`'s "P1: Harden durable context and recall" section to
   replace "do not add a database solely for transcript recall" with what
   was actually decided and why, and add a completed-item entry once this
   ships, so the roadmap doesn't contradict the shipped architecture.
6. Never auto-inject recalled history into ordinary turn context; never let
   heartbeat or scheduled turns write to conversation history (matching the
   existing constraint that those turns cannot take conversation-shaping
   actions).
7. Verify with `make fmt vet test race build`, explicitly confirming the
   build stays CGO-free.

## Known risks / open decisions

Two judgment calls this spec deliberately leaves to the owner rather than
deciding silently:

- **No write-time secret filtering.** `WriteMessage` stores content exactly
  as the conversation produced it — no `SecretGuard`-style rejection at
  write time, only redaction later, at recall time, before results reach the
  model. This matches how `RecentMessages` already behaves today (nothing
  currently stops a secret pasted into chat from sitting in `state.json`).
  The difference is permanence and reach: today that secret eventually
  rotates out of the bounded recent-message window; with this store it
  persists indefinitely and becomes searchable. Options: (a) accept this,
  since it's not a regression from current behavior, just a longer memory of
  the same exposure; (b) add `SecretGuard.Validate`-style rejection or
  redaction on the write path too, which would need a decision on what
  happens to a turn whose content gets rejected — silently store a redacted
  version, or fail the write (and does a failed write ever block the
  conversation turn itself)?
- **No retention or pruning story.** `eggy.db` grows forever; nothing in
  this spec deletes old messages. For a single owner this is likely fine for
  a very long time, but it's an explicit gap, not an oversight — worth a
  decision now (e.g. a bounded row count or age with oldest-first eviction)
  or a deliberate "not needed yet, revisit if the file size becomes a
  problem."

## Future extensions (explicitly out of scope now)

- Extending the same `messages`-shaped storage/search machinery to
  skills (semantic skill matching instead of literal `name: description`
  scanning) or to durable memory facts, once conversation recall has proven
  the pattern.
- Revisiting the CGO/`sqlite-vec` tradeoff if brute-force cosine search
  becomes a measured bottleneck.

## References

- `TODO.md`, "P1: Harden durable context and recall" (the constraint this
  spec revises).
- `docs/superpowers/specs/2026-07-22-eggy-mcp-client-design.md`'s memory
  comparison notes (the "gap is intentional" reasoning this spec knowingly
  departs from, and why).
- `modernc.org/sqlite`: <https://pkg.go.dev/modernc.org/sqlite>
