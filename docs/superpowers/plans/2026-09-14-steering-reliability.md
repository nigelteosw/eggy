# Steering reliability implementation plan

> **For agentic workers:** Use `superpowers:executing-plans` to implement this plan task by task after approval. Steps use checkboxes for tracking. This document does not authorize implementation, commits, or deployment.

**Goal:** Preserve the original request and subsequent steering through concurrent admission, persistence, termination, and compaction.

**Architecture:** Extend the existing `ActiveTurns` registry and `turns.Service`; keep one execution owner per account and conversation. Persist user input before exposing it to execution, retain the execution owner's registration through reply delivery and follow-up handoff, and preserve steering verbatim within the existing context ceiling.

**Tech stack:** Go 1.26, standard-library synchronization, existing SQLite conversation store, existing Telegram and web surfaces.

**Spec:** The design contract below is the self-contained specification for this plan.

**Status:** Proposed, pending review. Based on checkout `73fe947` and the uncommitted additive-steering prompt change. Concurrency and cancellation findings come from source inspection; reproduce them with deterministic tests before implementation. Existing steering tests passed during the preceding change; that does not verify these proposed fixes.

## Global constraints

- Production remains a single `eggyd` process and one replica.
- Keep the kernel and ports provider-neutral; bootstrap wires and dispatches only.
- Preserve principal-derived account ownership and conversation isolation.
- Keep scheduled and heartbeat turns unprompted and non-steerable.
- Retain independent payload-bound approvals. Steering never approves a tool or changes `/mode`.
- Preserve complete `ports.Message` values in live execution, including image parts. Persist only sanitized text and image markers, never image bytes.
- Preserve the earlier additive-steering prompt change and unrelated checkout edits, including `design_handoff_eggy_mobile/`.
- Add or change behavior test-first; run focused tests before the full suite.
- No new queue modes, config keys, tools, dependencies, background loops, durable queue, or schema changes in this scope.

## Design contract

### 1. One execution owner

An account/conversation has at most one live execution owner, including preparation, model/tool execution, reply delivery, and automatic late-follow-up handling. Admission atomically chooses between starting that owner and handing input to it. A later message cannot replace the registered owner or its cancellation target.

Use a short per-key admission critical section for the decision, history snapshot, durable user-message write, and publication to the active owner. Do not hold the registry-wide map mutex across storage I/O, model calls, tool execution, or channel delivery. Other conversations remain independent. Reclaim idle per-key synchronization entries.

Define order precisely: messages are ordered by successful service admission, not by worker scheduling after execution begins, producer timestamps, or text matching. For genuinely simultaneous ingress there is no claimed cross-channel wall-clock ordering. The existing HTTP/webhook enqueue acknowledgement remains transport acceptance, not a guarantee of durable admission; closing that crash window requires a separate durable-ingress design.

Bind the execution context to its registration generation. `Pending`, finish, and release use that generation, so an old context cannot drain or release a newer owner's messages. Incoming steering selects the current owner through its authenticated account/conversation key.

If a non-steerable turn already owns the same key, an owner request waits for its completion using the existing event worker and a completion signal, then retries admission. It never enters the unprompted turn's context. Waiting must be context-cancellable and must not hold the admission lock. No new per-conversation goroutine is needed. Serialize contenders by the registry's admission order if more than one is waiting.

### 2. Record before executing

For a new owner turn, snapshot the preceding conversation history before recording its initial input, under the same admission lock. Then record that input and register the owner before releasing admission. Build the first model context from that snapshot plus the original input exactly once. Later arrivals enter only through `Pending`, preventing history-plus-steering duplication.

For a steer, durably record its sanitized user message before publishing the complete message into the active queue. An input write failure must propagate and must not start a model call or publish a steer. Change `ConversationService.Record` to return the store error after logging it; audit callers that deliberately prefer best-effort behavior and make that choice explicit at those callers.

Successful execution records only the assistant reply: remove the original-input write from the success path. Late follow-ups are already recorded and must not write their input again. Keep each original user message separate in storage even if the existing execution path batches multiple late messages.

Recovery means the original and follow-up text remain in the existing history/recall store, including after failure. It does not mean a durable resumable agent execution or exactly-once external effects. Preserve existing event deduplication; do not introduce automatic retries after a failed or ambiguously completed action. The existing bounded recent-history window still applies to a later explicit continuation; older text may need recall. Image recovery after process exit requires the owner to resend the attachment.

### 3. Termination is an explicit outcome

Use the model/run outcome, not the named function's eventual return error, to decide whether follow-ups may run. Delivering a stop or step-limit message successfully must not turn a stopped/limited run into a successful run.

| Outcome | Pending input | Next action |
| --- | --- | --- |
| Normal completion and successful reply delivery | Present | Run a follow-up with the same execution owner, after delivering the original reply. |
| Normal completion and successful reply delivery | Absent | Atomically close the owner and release its slot. |
| Owner stop | Any | Cancel, finish the current execution, report the stop and any unread follow-ups; never auto-continue. |
| Model/tool-loop error, step limit, context limit, preparation or persistence failure | Any | Report failure and unread follow-ups; never auto-continue. |
| Reply delivery failure or parent-context cancellation | Any | Close without running follow-ups; retain already recorded input and log the delivery limitation. |

Keep a stopped registration until its worker has actually finished. Mark it stopping and cancel its context; do not immediately delete it and allow another model execution to overlap. A message arriving during stopping waits for completion and then becomes a new explicit owner turn. It must not be mistaken for pre-stop pending input.

Replace the recursive deferred `s.run` continuation with an explicit loop around the existing per-turn execution. On successful delivery, atomically take pending messages or close the owner. A message arriving during delivery joins that owner and is covered by the same handoff. Keep separate traces for separate late follow-up runs while retaining one execution owner.

Do not replay messages that `Pending` already handed to the model. An interrupted task can still be unfinished even when there are zero unread follow-ups. Explain that distinction in failure copy. For unread input, say: `Your follow-up messages were saved but were not processed. Ask me to continue when you are ready.` Use this claim only after successful persistence. Do not echo potentially sensitive message contents into notifications.

`/stop` prevents further tool launches once cancellation is observed, including between sequential calls in the same model response. Already completed mutations are not undone. A tool ignoring cancellation must finish before the execution slot is released. Ordinary steering continues to land at the existing step boundary; skipping unstarted tools in response to steering is outside this plan.

`/clear` must not race an active owner into writing pre-clear work into the new session. Serialize reset with admission and refuse it while that conversation is active or stopping, with `Stop the current turn before clearing this conversation.` This is a guard on the same lifecycle, not a second cancellation path.

### 4. Keep steering verbatim or fail visibly

Delete `contextWindow.boundSteering` and its invocation. Continue moving folded user messages into `w.steering`, preserving order and complete message parts. Never send those messages through the lossy checkpoint summarizer.

Keep the existing whole-request budget, tool-result bounds, and whole-tool-step compaction. If mandatory instructions, original input, steering, and the newest step cannot fit, return `ErrContextTooLarge` before another model call. Task 3 reports the limit and does not restart automatically with shortened input. Do not raise budgets to make tests pass.

### Footprint and deletion budget

This is a reliability feature, not a line-count refactor. Planning estimate: add 120–250 production lines for admission/ownership and explicit outcome handling; delete 45–90 lines of split start/steer handling, recursive continuation, late input recording, and lossy steering truncation. Measure the actual delta at review; do not add abstractions merely to match this estimate.

Config keys: +0. Tools: +0. Durable record types: +0. Background loops: +0. SQLite migrations: none. Existing user-message rows are written earlier, not duplicated; failures will now retain rows previously omitted. Synchronization remains in `ActiveTurns`, not in a second queue manager.

## Task 1 — Make admission and ownership atomic

**Files:** `internal/kernel/services/turns.go`, `internal/kernel/services/turns_test.go`, `internal/kernel/services/approval_ownership_test.go`, `internal/kernel/turns/turns.go`, `internal/kernel/turns/turns_test.go`, `internal/bootstrap/steering_test.go`.

**Interfaces:** Replace the separate service-side `Steer`/`Begin` decision with one registry admission operation. Its inputs are authenticated context, complete input message, whether the requested turn is steerable, and a preparation callback for the short admission section. Its result distinguishes the execution owner from joined input and supplies a generation-bound execution context. Keep these types kernel-local. Preserve `Stop(ctx) bool` and `Active() bool` for existing callers; update `turns.Registry` and its fake together.

- [x] Write a deterministic regression in `turns_test.go`: block the first request during preparation, submit a second request to the same account/conversation, then release preparation. Assert exactly one active `Loop.Run`, original input retained, and second input available exactly once through pending input. Use channels/barriers, not sleeps.
- [x] Add registry cases for independent accounts/conversations, stale-generation drain/release, a non-steerable owner with a waiting owner request, cancelled waiters, and cleanup of idle entries. Keep scheduled/heartbeat policy tests intact.
- [x] Run the focused tests and observe the old competing-start behavior. A race-detector pass alone is insufficient: this is a logical race even if every map access is locked.
- [x] Implement the admission operation and generation binding. Algorithm:

```text
acquire this account/conversation's admission slot
if an incompatible or stopping owner exists:
    register this waiter in admission order
    release slot; wait for its turn or context cancellation; retry
prepare input/history while this admission is exclusive
if a steerable owner exists:
    publish the input to that owner; return joined
else:
    register an owner with a unique generation; return execution context
release slot
```

- [x] Keep admission separate from long execution. Ensure preparation errors release the slot and leave no published input or ghost owner. Task 2 supplies the durable preparation operation.
- [x] Exercise two distinct message events through the real `App.Run` event queue in `steering_test.go`; do not rely solely on the existing re-entrant `HandleEvent` test.
- [x] Run `go test ./internal/kernel/services ./internal/kernel/turns ./internal/bootstrap -run 'Admission|Steer|ActiveTurns|Ownership' -count=1`, then the same selection with `-race`.

## Task 2 — Persist inputs in admission order

**Files:** `internal/kernel/services/conversation.go`, `internal/kernel/services/conversation_test.go`, `internal/kernel/turns/turns.go`, `internal/kernel/turns/turns_test.go`, `internal/bootstrap/steering_test.go`; audit `ConversationService.Record` callers and `plugins/store/sqlite/store.go` without changing the schema.

**Interfaces:** Keep `Record(ctx, conversationID, message, source) error`, but make its error meaningful. The admission callback captures preceding history for a new owner and records one sanitized input before registry publication. A joined steer does not reload history. Carry the initial history snapshot into the owner execution.

- [ ] Add a test whose fake loop inspects recorded input at entry and then returns `errors.New("model unavailable")`. Assert the original message exists before execution and remains after failure.
- [ ] Add integration cases asserting stored order `user A, user B, assistant answer`, no repeated user row after success, and no repeated original or steered input in model requests. Use identical text for A and B in a separate case to catch accidental text-based deduplication.
- [ ] Replace `TestConversationDurableWriteFailureIsLoggedAndSwallowed` with an error-propagation test. Inject a failed write for original and steering input; assert zero execution/publication for the failed input and a visible error through the owning surface. Keep assistant-write failures explicit too.
- [ ] Run `go test ./internal/kernel/services ./internal/kernel/turns -run 'Conversation|Record|Persist|Steer' -count=1` and confirm the new tests fail for late recording and swallowed errors.
- [ ] Implement this preparation order under Task 1's admission slot:

```text
if starting a new owner:
    precedingHistory = RecentMessages(conversation)
    if history cannot be read: refuse admission with an error
Record(conversation, sanitized user input, source)
if recording fails: refuse admission; do not publish input
publish complete live input / establish the execution owner
```

- [ ] Remove success-path recording of the initial user input. Ensure late follow-ups do not enter the ordinary admission/write path again. For the same-owner handoff, carry the original history snapshot, original user message, each consumed steering message in order, and the completed assistant replies in memory. Capture consumed messages in the existing `PendingInput` closure. Append newly handed-off messages once and pass an empty `input` to `Loop.Run`, since those messages are already in its supplied history. Do not reload current SQLite history into this same-owner continuation or deduplicate by text. This preserves live attachment parts and avoids duplicating recorded messages; ordinary new owners still take a fresh history snapshot. Apply the existing context ceiling to this carried context.
- [ ] Retain secret filtering and attachment-marker tests. Confirm the web history refresh remains compatible with earlier recording; no UI rewrite is required for this fix.
- [ ] Repeat focused tests with the real SQLite store and run existing dispatcher duplicate-event tests. Document that execution failure followed by manual event replay is not a new exactly-once guarantee; do not silently add automatic event replay.

## Task 3 — Make stop, failure, and successful handoff explicit

**Files:** `internal/kernel/turns/turns.go`, `internal/kernel/turns/turns_test.go`, `internal/kernel/services/turns.go`, `internal/kernel/services/turns_test.go`, `internal/kernel/agent/loop.go`, `internal/kernel/agent/loop_test.go`, `internal/commands/commands.go`, `internal/commands/commands_test.go`, `internal/bootstrap/steering_test.go`, `internal/bootstrap/app_events.go` only if needed to route existing error delivery.

**Interfaces:** A kernel-local execution outcome distinguishes completed, stopped, failed, and limited runs. Registry finish atomically returns either pending messages for another run under the same owner, or closes the registration. `Pending` and finish accept the generation-bound context from Task 1. Transport delivery errors remain separate from execution outcome.

- [ ] Add table-driven service tests for the following cases. Each case supplies undrained input through the registry and counts actual loop invocations and delivered notifications:

```text
run error               reply delivery      expected loop invocations
nil                     succeeds           2 (original + pending follow-up)
context.Canceled        succeeds           1
agent.ErrToolStepLimit  succeeds           1
agent.ErrContextTooLarge succeeds          1
model unavailable       succeeds           1
nil                     fails              1
```

- [ ] Reproduce `/stop` with a real registry and a blocked fake model: enqueue a follow-up, stop, unblock model cancellation, assert no second model call. Assert the saved follow-up remains in history and the notification explains it was not processed.
- [ ] Add a stop-during-tool test with two sequential tool calls; after the first returns and cancellation is observed, the second must not execute. Assert an uncancellable first tool keeps the slot occupied until it returns.
- [ ] Add completion-race tests: input during final reply delivery, input during handoff, input after closure, and input while stopping. Assert each input is either joined to its owner or admitted as a new explicit turn, never lost or drained by the wrong generation.
- [ ] Add `/clear` tests for active, stopping, idle, and another account/conversation. Existing approval tests must still prove ordinary messages cannot approve anything.
- [ ] Run the focused tests first, observing the erroneous continuation after successful stop/limit delivery.
- [ ] Replace the recursive deferred continuation with this explicit owner loop:

```text
run using current input and context
classify execution outcome independently of notification delivery
record/deliver result or failure notification
if outcome is not completed OR delivery failed:
    close owner; report unread count when delivery is possible; return
atomically take pending input or close owner
if closed: return
continue with pending input, already recorded, under the same owner
```

- [ ] Recheck the owner's stop flag at handoff: `/stop` arriving during successful reply delivery must suppress continuation too. The flag check and handoff must share the registry synchronization point.
- [ ] Keep failure notifications owned by one existing delivery path. Bootstrap must not duplicate the service's user-facing error. On delivery failure, log the failure; do not pretend the user received a notification or invoke the model again to manufacture one.
- [ ] Run `go test ./internal/kernel/agent ./internal/kernel/services ./internal/kernel/turns ./internal/commands ./internal/bootstrap -run 'Stop|Cancel|Limit|Failed|Undrained|Handoff|Clear|Steer' -count=1`, then with `-race`.

## Task 4 — Preserve all live steering through compaction

**Files:** `internal/kernel/agent/compaction.go`, `internal/kernel/agent/compaction_test.go`, `internal/kernel/agent/loop_test.go`, `internal/kernel/turns/turns_test.go`, `docs/src/content/docs/use/long-turns.md`.

**Interfaces:** Keep existing `ContextPolicy`, `contextWindow.fit(overhead) error`, and `ErrContextTooLarge`. No new budget knobs or summarization interface.

- [ ] Add a regression that exceeds the current 12,000-character rescued-steering threshold while remaining below the whole-request ceiling. Make the oldest instruction longer than the checkpoint excerpt limit so truncation is observable. Assert exact content, order, and single occurrence in the model-facing messages after repeated folding.

Start with this direct regression against the real context-window implementation, then add the folding/model-request integration case using the existing `queuedModel` and `callStep` fixtures:

```go
func TestCompactionRetainsOlderSteeringVerbatim(t *testing.T) {
    first := "Keep this entire instruction: " + strings.Repeat("x", 13000)
    second := "Also include the dates."
    w := newContextWindow(ContextPolicy{}, []ports.Message{
        {Role: ports.RoleUser, Content: "Compare the documents."},
    })
    w.steering = []ports.Message{
        {Role: ports.RoleUser, Content: first},
        {Role: ports.RoleUser, Content: second},
    }
    if err := w.fit(0); err != nil {
        t.Fatal(err)
    }
    var users []string
    for _, message := range w.messages() {
        if message.Role == ports.RoleUser {
            users = append(users, message.Content)
        }
    }
    if len(users) != 3 || users[0] != "Compare the documents." || users[1] != first || users[2] != second {
        t.Fatal("compaction shortened, reordered, duplicated, or removed owner instructions")
    }
}
```

- [ ] Add a budget-exhaustion regression asserting `errors.Is(err, ErrContextTooLarge)` and no subsequent provider call. Add a multipart steering case asserting image bytes remain in live parts while persisted history/traces contain only the permitted marker.
- [ ] Run `go test ./internal/kernel/agent -run 'Compaction|ContextTooLarge|Steer' -count=1` and observe the old truncation.
- [ ] Delete `boundSteering` and remove `w.boundSteering()` from `fit`; retain the existing final mandatory-size check:

```go
if total := w.mandatoryChars() + MessageChars(w.tail); total > allowance {
    return fmt.Errorf("%w: %d characters of required input against a %d-character allowance", ErrContextTooLarge, total, allowance)
}
```

- [ ] Confirm tool call/result pairs remain intact and the original prompt survives. Update the long-turn documentation to describe admission ordering, earlier persistence, explicit failure/stop behavior, and the context-limit tradeoff. Qualify the existing unconditional claim that a steer always gets an answer.
- [ ] Run the full agent and turns package tests after the focused tests pass.

## Acceptance and verification

Implement Tasks 1 → 2 → 3 → 4. Task 4 is technically independent, but this order gives its budget failure the explicit handling from Task 3. Review each task's diff and tests before proceeding. Do not commit or push without the user's authorization.

- [ ] One original request plus multiple follow-ups reaches the model in admission order without replacing the original request, duplicating input, or creating competing execution owners.
- [ ] A new request cannot read its own persisted copy plus append it again. Late follow-ups likewise appear once, including image parts in the live process.
- [ ] Original and steering text survive failed execution in SQLite; failed persistence prevents admission and produces an honest error.
- [ ] Stop, limits, failures, and reply-delivery failures never cause automatic pending work to run. Successful completion handles late input atomically.
- [ ] Account/conversation ownership, unprompted restrictions, approvals, restart draining, and `/clear` session boundaries remain intact.
- [ ] All steering stays verbatim until completion or an explicit context-size error; no instruction is silently shortened to make it fit.
- [ ] Run `GOCACHE=/tmp/eggy-go-cache GOTMPDIR=/tmp make fmt vet test race build`.
- [ ] Run `make smoke` when Docker is available. Report daemon or environment failures as blockers, not passes.
- [ ] Run `git diff --check`, inspect `git status --short` and the full task diff, and measure the production-line delta against the footprint estimate.

## Explicit limits

This plan does not provide crash-safe ingress, durable tool checkpoints, automatic restart/resume, a new queue UI, or a guarantee that a model obeys every instruction. It fixes the four identified live-steering defects and makes their failure behavior honest. Any future crash-safe delivery work must separately address idempotent message admission and ambiguous external tool effects rather than equating saved chat history with exactly-once execution.
