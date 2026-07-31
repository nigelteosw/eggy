# Context Budget Plan

Replacing Eggy's current context management with something closer to what
mature harnesses actually do.

Status: plan only, nothing implemented.

---

## 1. What Eggy does today

Two independent mechanisms, neither aware of the other, neither aware of tokens.

### 1a. Cross-turn history — `internal/kernel/services/conversation.go`

```go
conversation := services.NewConversationService(memoryStore, 20, options.Now, options.Logger)
```

- A hardcoded **20-message window**, oldest first, per conversation.
- No summarization. Message 21 is simply gone from the model's view.
- `RecentMessages` reconstructs only `{Role, Content}` — `ToolCalls`,
  `ToolCallID`, and `Name` are dropped, so history is text-only by
  construction.
- `Reset` moves the window cursor; durable history is untouched.

**This is the layer with no compaction at all.** It is also the layer the
owner notices, because it is what makes Eggy forget what it was doing three
turns ago.

### 1b. Within-turn compaction — `internal/kernel/agent/compaction.go`

```go
contextPolicy := agent.ContextPolicy{
    BudgetChars:        96000,
    RecentSteps:        16,
    OutputExcerptChars: 8192,
    MaxSteps:           maxToolStepsPerTurn, // 500
}
```

- Operates only on `tail` — the assistant/tool exchange the loop itself
  appended. `preserved` (instructions, durable context, history, the request)
  is never touched.
- Trigger: `len(steps) > RecentSteps || stepChars(steps) > BudgetChars`.
- Folds whole steps (assistant + its tool results) so a tool result never
  outlives its call. **Correct, and worth keeping.**
- Emits a system-role `CheckpointMessage`, not a fabricated assistant turn.
  **Also correct, also worth keeping.**

### What is actually broken

| # | Problem | Evidence |
|---|---------|----------|
| 1 | The "summary" is not a summary | `SummarizeMessage` returns `"Called grep, read"` / `"Used bash"`, truncated to 160–320 runes. A 40-step turn compacts to a list of tool names with no goal, no findings, no decisions. |
| 2 | Budget is in **characters**, not tokens | `BudgetChars: 96000` is an absolute unrelated to the model's window, the system prompt, or the tool schemas. Same number for a 200k model and a 32k one. |
| 3 | Real usage is measured and then ignored | `ports.ModelUsage.PromptTokens` is populated by every provider and accumulated in `RunResult.Usage` — and never read by any decision. |
| 4 | `OutputExcerptChars` is dead config | Set in `bootstrap/app.go:244`, defaulted in `normalized()`, **referenced nowhere else**. Tool results enter `tail` at full size. |
| 5 | No overflow recovery | If the char estimate under-counts, the provider 400s and `Run` returns the error. The turn dies. |
| 6 | No cross-turn compaction | See 1a. |
| 7 | No per-session budget | One `ContextPolicy` for every turn on every model. |
| 8 | Policy is not configurable | Hardcoded in `bootstrap/app.go`, not in `config.yaml`. |

---

## 2. How the others do it

### Claude Code — three tiers, escalating in cost

1. **Microcompaction** — evict bulky *tool results* early, before the window
   is tight. Transcript structure survives; only the fat payloads go.
2. **Auto-compaction** — near the limit, an LLM writes a human-readable
   summary into a `compaction` block replacing the summarized span.
   Deliberately plaintext and inspectable.
3. **`/compact [instructions]`** — manual, at task boundaries, with a focus
   hint ("preserve the schema decisions").

The complementary half is not compaction at all: CLAUDE.md / memory files
carry durable rules so they never depend on surviving a summary. Practitioner
guidance is to compact around **60%**, not 95%.

### OpenClaw — budget resolution + defense in depth

- A **Context Window Guard** pre-flight-validates available window before
  every LLM call.
- Window size resolves from a **priority chain of sources**, so per-session
  budgets differ: main session 200k, sub-agents 32k.
- Three triggers: proactive threshold, **overflow recovery** (pattern-matches
  dozens of provider-specific "context too long" strings across Anthropic /
  OpenAI / Bedrock / Gemini / Ollama / OpenRouter, then compacts and retries),
  and manual `/compact`.
- Config under `agents.defaults.compaction`: `keepRecentTokens` (20k),
  `model` (summarize with a different model), `maxActiveTranscriptBytes`,
  `identifierPolicy: strict` (don't let the summarizer mangle opaque IDs).
- **Memory flush before compaction**: a silent turn where the agent writes
  durable notes to memory files first.
- Full history stays on disk; compaction only changes what the model *sees
  next turn*.

### Pi — the simplest mechanically, closest to portable

- Trigger: `contextTokens > contextWindow - reserveTokens`, `reserveTokens`
  default **16384** (room for the reply).
- Cut point: **walk backwards from newest**, accumulating token estimates
  until `keepRecentTokens` (**20000**) is covered. Everything older collapses
  into one `CompactionEntry`; reload = summary + messages from
  `firstKeptEntryId`.
- Tool results truncated to **2000 chars** at serialization, with a marker
  stating how many chars were dropped. Head-truncated file reads get a
  continuation nudge.
- Compaction runs in the **background** where possible, on a fresh routing
  session ID with prompt-cache writes disabled — one-off prompts aren't worth
  polluting the cache with.
- `/autocompact` exposes `reserveTokens` / `keepRecentTokens` in a toggle UI.

### Hermes — layered and pluggable

- **Two independent compressors**: a gateway hygiene layer at **85%** fill
  (safety net, before agent processing), and the primary `ContextCompressor`
  at **50%** inside the tool loop, using real token counts from API responses.
- Four phases:
  1. Prune tool results >200 chars to placeholders.
  2. Compute head/tail boundaries, aligned to tool-pair edges.
  3. Send the whole middle to a summarizer model with a **structured
     template — goal, progress, decisions, next steps**.
  4. Reassemble head + summary + tail, clean orphaned tool pairs.
- Config: `threshold` 0.50, `target_ratio` 0.20, `protect_last_n` 20,
  `min_tail_user_messages` 1, `in_place`.
- Hard constraint: **the summarizer's window must be ≥ the main model's**, or
  compression silently fails and drops context.
- Prompt caching co-designed with compaction: `system_and_3` places 4
  breakpoints (system + last three non-system messages) so compaction
  invalidates selectively.
- `ContextEngine` is an ABC; `context.engine` selects an implementation.
- When Hermes drives **Codex CLI** it *cannot* compact — the app-server owns
  the thread. `compression.codex_app_server_auto` is `native` / `hermes` /
  `off`.

### Convergent design (do not deviate without a reason)

1. Summarize the middle, protect head and tail.
2. Never split a tool call from its result; align cut points to pair edges.
3. Truncate tool results on the way *in*, not just at compaction time.
4. Budget in tokens against the model's real window, minus a response reserve.
5. Keep the full transcript on disk; compaction changes only the live view.
6. Give the summarizer a structured template, not a free-form "summarize this".

Eggy already has (1) partially, (2) fully, and (5). It has none of (3), (4),
or (6).

---

## 3. Target design

### 3.1 Token accounting — `internal/kernel/agent/tokens.go` (new)

Replace `MessageChars` as the budget unit.

```go
// Budget is a turn's live token allowance, derived from the model's window
// rather than a fixed constant.
type Budget struct {
    ContextWindow int // model's total window
    ReserveTokens int // headroom for the reply; default 16384 (Pi)
    KeepRecent    int // tokens of tail never folded; default 20000 (Pi/OpenClaw)
}

func (b Budget) Max() int { return b.ContextWindow - b.ReserveTokens }
```

Estimation strategy, in order of preference:

1. **Measured** — `ports.ModelUsage.PromptTokens` from the previous response
   in this turn. Exact for everything up to the last call.
2. **Estimated** — for messages appended since that response, a heuristic.
   Start with `runes/3.5` plus a per-message overhead (~4 tokens) and a
   per-tool-call overhead. Deliberately pessimistic.

This mirrors Hermes (real counts inside the loop) and Pi (estimates + provider
metrics). Do **not** add a tokenizer dependency; the reserve absorbs the error.

`ContextWindow` needs a source. Add it to the model config alongside the model
ID — `ports.Model` does not expose it and shouldn't have to.

### 3.2 Tool-result truncation at the boundary — cheapest win

In `Loop.Run`, where the tool message is built:

```go
toolMessage := ports.Message{Role: ports.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: string(output)}
```

Truncate `Content` to `ToolResultLimit` (start at 4000 runes — Pi uses 2000,
Hermes 200 for *pruned* results) with an explicit marker:

```
…[truncated 18422 of 22422 characters]
```

This finally gives `OutputExcerptChars` a job — rename it `ToolResultChars`
and use it here. Most turns will then never reach the compaction threshold at
all. This is Claude Code's microcompaction tier, and it is a ~20-line change.

**Design note:** the full output must still reach the event stream
(`EventToolEnd`) so surfaces and logs see everything; only the model's copy
shrinks.

### 3.3 LLM summarization — the quality fix

`ContextPolicy.compact` currently calls `SummarizeMessage` per message. Replace
the folded span with a model-generated summary.

New port shape:

```go
// Summarizer condenses a folded span into the checkpoint the model reads in
// its place. Nil means fall back to the current mechanical stub.
type Summarizer interface {
    Summarize(ctx context.Context, span []ports.Message, instructions string) (string, error)
}
```

Prompt template, following Hermes:

- **Goal** — what the turn is trying to accomplish.
- **Progress** — what has been done, with concrete results.
- **Decisions** — choices made and why, including rejected options.
- **Next steps** — what remains.
- **Identifiers** — file paths, IDs, and error strings must be reproduced
  verbatim (OpenClaw's `identifierPolicy: strict`).

Constraints to honour:

- The summarizer model's window must be **≥** the main model's, or the fold
  silently loses content. Validate at bootstrap and refuse to start otherwise.
- Summarization is a model call inside the loop — it can fail. On error, fall
  back to the current mechanical `SummarizeMessage` path rather than failing
  the turn.
- Use a cheap fast model by default; make it configurable
  (`compaction.model`).

`compact` becomes context-aware and fallible, so its signature changes:

```go
func (p ContextPolicy) compact(ctx context.Context, tail []ports.Message, summary string) ([]ports.Message, string, bool)
```

### 3.4 Overflow recovery

Wrap the `target.Model.Generate` call in `Loop.Run`: on a context-overflow
error, force a compaction pass and retry **once**. Needs a classifier —
`ports.IsContextOverflow(err)` matching provider error strings. Start with the
providers Eggy actually has and extend as they appear; OpenClaw's list is the
reference for what this eventually looks like.

Without this, every estimation bug is a dead turn.

### 3.5 Cross-turn compaction — the gap the owner feels

`ConversationService`'s fixed 20 needs to become a budget:

- Keep messages back to `KeepRecent` tokens.
- Everything older folds into a **persisted** conversation summary, stored
  alongside the thread, refreshed when the window advances.
- Inject that summary as a system message in the `preserved` block, above
  history.
- `Reset` clears the summary along with the window cursor.

**Blocker to resolve first:** `RecentMessages` drops `ToolCalls` /
`ToolCallID` / `Name`. If cross-turn history should ever carry tool exchanges,
`ports.StoredMessage` and the SQLite schema need those columns. Decide
explicitly: text-only history is a legitimate choice (it sidesteps orphaned
tool-pair cleanup entirely), but it should be a decision, not an accident.

### 3.6 Configuration

Move the policy out of `bootstrap/app.go` into `config.yaml`:

```yaml
context:
  reserve_tokens: 16384      # headroom for the reply
  keep_recent_tokens: 20000  # tail never folded
  tool_result_chars: 4000    # truncation at the tool boundary
  max_steps: 500             # runaway guard, unchanged
  summarizer:
    model: ""                # empty = main model
  conversation:
    keep_recent_tokens: 12000
```

Per-session budgets (OpenClaw's 200k main / 32k sub-agent) are worth having
once Eggy has sub-agents; `Budget` being a value type means it costs nothing
to thread through later.

### 3.7 Observability — how to see the impact

The reason the current implementation feels invisible is that it emits
nothing. Add:

- `EventCompaction` on the loop's event stream: steps folded, tokens before
  and after, whether the summarizer or the fallback ran.
- Structured log line per compaction.
- Extend `/context` to report: live tokens vs. budget, percentage used,
  compactions this turn, whether the conversation summary is active.

Do this **first**. Everything else is unmeasurable without it.

---

## 4. Sequencing

Ordered by value per unit of risk. Each step ships independently.

| Step | Change | Risk | Why here |
|------|--------|------|----------|
| 0 | Observability: `EventCompaction`, logs, `/context` reporting | none | Nothing below is verifiable without it. Also answers "does compaction ever actually fire today?" |
| 1 | Tool-result truncation at the tool boundary (§3.2) | low | ~20 lines. Kills most budget pressure outright. Gives `OutputExcerptChars` a purpose. |
| 2 | Token accounting: `Budget`, estimator, `ContextWindow` in model config (§3.1) | low | Pure addition; run it alongside `BudgetChars` and log the divergence before switching over. |
| 3 | Switch `compact` to the token budget, delete `BudgetChars` | medium | Behaviour change, but step 0 makes it observable and step 2 has already validated the numbers. |
| 4 | Overflow recovery + retry (§3.4) | low | Safety net for step 3's estimation error. |
| 5 | LLM summarization with the structured template (§3.3) | medium | The quality fix. Depends on nothing above, but is only worth it once compaction fires at sensible times. |
| 6 | Cross-turn conversation compaction (§3.5) | high | Touches persistence and possibly the schema. Resolve the tool-call question first. |
| 7 | Config surface (§3.6) | low | Once the knobs have proven themselves. |

**Explicitly not doing** (yet, and why):

- **Background/async compaction** (Pi) — Eggy's turn latency is not currently
  the complaint. Revisit if step 5 makes turns visibly stall.
- **Prompt-cache breakpoint co-design** (Hermes `system_and_3`) — depends on
  cache-control support in the provider adapters that Eggy doesn't have.
- **Memory flush before compaction** (OpenClaw) — attractive given Eggy
  already has the `memory` tool, but it's an extra model turn per compaction.
  Reconsider after step 5.
- **Pluggable `ContextEngine`** (Hermes) — one good implementation before an
  abstraction over several.

---

## 5. Open questions

1. **Where does `ContextWindow` come from?** Per-model config entry, a
   provider capability lookup, or a hardcoded table? Config is the least
   magic and the easiest to get wrong quietly.
2. **Should cross-turn history carry tool exchanges?** (§3.5) Text-only is
   simpler and defensible; decide deliberately.
3. **Does the summarizer share the turn's model?** Sharing is simplest and
   satisfies the window-size constraint for free. A cheaper model saves money
   but requires the bootstrap validation in §3.3.
4. **Is `preserved` really immune?** Today compaction never touches
   instructions, durable context, history, or the request. If SOUL.md +
   memory + user + 20 messages ever exceeds the budget on their own, the turn
   is unrecoverable. Needs a floor check at minimum.

---

## References

- [Compaction · OpenClaw](https://docs.openclaw.ai/concepts/compaction)
- [Context Management — coolclaws/openclaw-book (DeepWiki)](https://deepwiki.com/coolclaws/openclaw-book/3.2-context-management)
- [Pi — compaction.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/compaction.md)
- [Pi Coding Agent — Compaction](https://pi.dev/docs/latest/compaction)
- [Hermes — Context Compression and Caching](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/context-compression-and-caching.md)
- [Context Compression — NousResearch/hermes-agent (DeepWiki)](https://deepwiki.com/NousResearch/hermes-agent/10.1-context-compression)
- [Context Compaction Deep Dive: Codex CLI, Claude Code, OpenCode](https://codex.danielvaughan.com/2026/04/14/context-compaction-deep-dive-codex-cli-claude-code-opencode/)
- [Context management in agent harnesses — Arize](https://arize.com/blog/context-management-in-agent-harnesses/)
