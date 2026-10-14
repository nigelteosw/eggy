# Agent Runner harness: learnings from Pi

Date: 2026-10-14
Status: Implemented on branch `agent-runner-harness-learnings`, uncommitted. Go tests, race tests and the `eggyd` build pass locally. The Linux sandbox tests have not run: Docker was down, so `make smoke` is outstanding.

## Implementation notes

Where the build differs from the proposals above:

- **Change 1.** The budget counts uncached prompt tokens plus completion tokens. The cost-based cap was not built; a provider that reports only a total is charged that total. Each model call is recorded as a `model` step with its spend, using the run's existing event log.
- **Change 2.** Coding runs use a `FoldChars` target. A fold triggered by the request budget goes down to 160,000 characters in one batch, rather than one step at a time. The tail budget no longer binds for coding runs.
- **Change 3.** `ContextPolicy.PreserveExcerpts` is set for coding runs only. Owner turns still collapse whitespace.
- **Change 6.** A turn's notes are saved to `handoff.txt` in its run directory when it asks the owner, and carried forward across later questions. No schema change.
- **Change 7.** The output-limit check touched `ports.ModelResponse`, the OpenAI-compatible adapter, and the shared loop, so it applies to every turn, not only coding runs.
- **Change 5.** Command output is capped at 40 KiB, the end of it kept in memory. The full output is written to `<run>/tmp/output-*.log`, which is in the sandbox's writable tree.

## Intent

A coding run on `eggy` failed with `token budget exhausted (2000000 tokens)`
while it was still making progress: edits were landing and checks were
running. This document records why, compares our harness against Pi's
(`@earendil-works/pi-coding-agent` 0.86.1, read from its compiled `dist/`),
and proposes the changes that follow.

## The failing run

- 16:01 — run starts, explores ~20 tool calls, asks the owner which list to
  paginate.
- 17:00 — resumed run starts, re-explores the same files (~25 reads/searches),
  then edits. 7 of ~25 edits fail with
  `old text matches 0 times in <file>; it must match exactly once`, all on
  files read early in the run.
- 17:03 — fails at the 2M token cap, about 3.5 minutes into the second leg.

## Diagnosis

### 1. The budget counts repeated prompt tokens

`budgetModel.Generate` (`internal/extensions/agentrunner/worker.go`) adds
`Usage.TotalTokens` per model call. A coding loop resends its whole context
every step, mostly as cache reads, so the cumulative total grows roughly
quadratically with steps. About 65 calls at ~30k tokens each reaches 2M
(estimate — we do not record per-call usage). The budget also carries the
first leg's spend through `run.UsedTokens`.

The cap is measuring context re-reads, not work. A healthy run of normal
length trips it.

### 2. A resumed run starts from nothing

`turnInput` gives the resumed turn the brief plus the question and answer —
none of the first leg's findings. The second leg re-explored the codebase
while still being charged for the first leg's exploration.

### 3. Compaction defeats the prompt cache and corrupts file text

`codingPolicy` uses `RecentSteps: 16`. After step 16, every step folds the
oldest step into the checkpoint:

- **Cache.** The checkpoint is a system message directly after the preserved
  prefix and changes on every call, so everything after the prefix misses
  the provider's prompt cache on every call.
- **File text.** `collapse()` (`internal/core/agent/compaction.go`) runs
  `strings.Fields`, which removes every newline and indent from folded tool
  results and tool-call arguments. A model that writes `old` text from the
  checkpoint can never match the file. This is the most likely cause of the
  edit failures.

### 4. Tool output loses the information the model needs

- `read` cuts at 2000 lines without saying so, and its `offset` starts at 0
  while `search` reports line numbers starting at 1.
- `bash` and `run_check` keep the **start** of the output: `cappedBuffer`
  (`sandbox_linux.go`) stops at 64KB, then `boundToolResult` keeps the first
  48k characters. A long failing `go test` loses its summary and errors.
- The `edit` error does not tell the model what to do next.

## How Pi does it

### Loop (`pi-agent-core/dist/agent-loop.js`)

- No step cap and no token budget. The loop runs until the model stops
  calling tools; the context window is the only limit, handled by compaction.
- Context is append-only between compactions, so the cached prefix stays
  valid.
- Tool errors go back to the model as results, never ending the turn (as
  ours do).
- If the response hit the output token limit (`stopReason === "length"`),
  every tool call in it is failed without running, with "re-issue the tool
  call with complete arguments": cut-off arguments may still parse.
- Tool calls run in parallel unless a tool declares itself sequential.
  Results join in the order the model issued them.
- Steering messages are picked up between steps; follow-ups queued after the
  model stops restart the loop.

### Compaction (`dist/core/compaction/compaction.js`)

- Triggers only when context tokens exceed `contextWindow − reserveTokens`
  (16,384), or on a provider context-overflow error (compact once, retry
  once).
- Context size comes from the last response's real usage, plus a chars/4
  estimate for messages added since.
- Keeps the most recent ~20,000 tokens verbatim. It cuts only at user or
  assistant messages, never between a tool call and its result; a turn split
  in half gets its own "turn prefix" summary.
- The summary is written by the model in a fixed structure: Goal,
  Constraints & Preferences, Progress (Done / In Progress / Blocked), Key
  Decisions, Next Steps, Critical Context, with the instruction "preserve
  exact file paths, function names, and error messages". Later compactions
  *update* the previous summary rather than starting a new one.
- The summary ends with `<read-files>` and `<modified-files>` lists taken
  from the tool calls, so the model knows what to re-read rather than
  reconstruct.
- Summarization requests don't write to the prompt cache, and transient
  failures are retried.

### Tools (`dist/core/tools/`)

- **edit** — `{path, edits: [{oldText, newText}]}`. Every edit is matched
  against the original file; overlapping edits are rejected with "merge
  them". Matching is exact first, then lenient (NFKC, trailing whitespace
  per line, curly quotes, Unicode dashes and spaces). In the lenient case
  only the touched lines are rewritten, so other lines keep their original
  bytes. Windows line endings and a byte-order mark are normalized and
  restored. `prepareArguments` repairs common model mistakes: `edits` sent
  as a JSON string, a single object, or the old top-level `oldText`.
  Errors: "Could not find the exact text… must match exactly including all
  whitespace and newlines", "Found N occurrences… provide more context",
  "No changes made". Success returns a short message to the model and the
  diff to the UI only.
- **read** — offsets start at 1. Capped at 2000 lines **or 50KB**. Every
  cut says what was left out: `[Showing lines 1-2000 of 5400. Use
  offset=2001 to continue.]`. A single line over the cap gets a `sed`
  command to read it instead.
- **bash** — keeps the **end** of the output (2000 lines / 50KB), saves the
  full output to a temp file, and gives the model its path:
  `[Showing lines X-Y of N. Full output: <path>]`.
- **grep** — match lines cut at 500 characters.
- **System prompt** — each tool contributes a one-line snippet and its own
  rules. For edit: "use one edit call with multiple entries", "oldText is
  matched against the original file", "keep oldText as small as possible
  while still unique".

## Proposed changes

In priority order. 1–3 would have kept this run alive; 3–4 address the edit
failures.

1. **Budget on cost, not repeated prompt tokens.** Count new work only
   (`PromptTokens − CachedPromptTokens + CompletionTokens`), or cap by
   `CostUSD` where the provider reports it. Keep `maxCodingSteps` as the
   runaway guard. Record usage on each model call so the run page can show
   where the spend went.
2. **Compact coding runs only near the context limit.** Raise `RecentSteps`
   and `BudgetChars` well past normal run length, so folding happens only when
   the request nears `RequestChars − ReserveChars`. Between compactions the
   context then only grows, which keeps the cache valid. Consider folding a
   large block at once (keep a recent window, as Pi's ~20k tokens) rather
   than one step at a time, so the checkpoint changes rarely.
3. **Stop `collapse()` flattening whitespace in tool output.** At minimum for
   coding runs: keep newlines and indentation in excerpts. Add a list of
   files read and modified to the checkpoint, and say plainly that
   checkpoint text is not exact and the file must be re-read before editing.
4. **Edit tool.** Accept several edits per call, matched against the original
   file with overlap detection. Add Pi's lenient matching fallback and
   line-ending/byte-order-mark handling. Change the error to "could not find
   the text; re-read the file — text from the checkpoint is not exact".
5. **Read, bash and run_check.** `read`: offsets starting at 1, matching
   `search`, a byte cap, and a "use offset=N to continue" notice. `bash` and
   `run_check`: keep the end of the output, and keep the full output in the
   run directory with its path in the result.
6. **Resume with what the first leg learned.** Pass the first leg's
   checkpoint, or a short summary of it, into the resumed turn's input.
7. **Output-limit stop.** If a response was cut off by the output token
   limit, fail its tool calls instead of running them. This needs the
   adapter to report the finish reason.
8. **Per-tool prompt guidance.** Add Pi-style tool rules to
   `codingInstructions`.

## Boundaries and cost

- Changes 2–3 touch `internal/core/agent`, which every turn uses. Owner turns
  keep their current policy: the coding run passes its own `ContextPolicy`,
  and any change to `collapse` must keep the owner-turn tests passing.
- Changes 1, 4, 5, 6 and 8 stay inside `internal/extensions/agentrunner`.
- No new config keys, tools, background loops or durable record types. The
  per-call usage in change 1 would extend the existing run record or event
  log rather than adding a table.
- Not adopted from Pi: model-written summaries in the shared loop. They add a
  model call on the compaction path, and owner turns compact far more often
  than coding runs. Revisit if change 3's file list and whitespace-preserving
  excerpts prove insufficient.
