# Durable Implementation Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Eggy's owner-triggered native coding work durable and resumable, with compacted transcript context, useful Telegram milestones, and unchanged independent commit/push/PR approvals.

**Architecture:** Add a provider-neutral implementation-session contract and kernel coordinator, backed by a JSON-file adapter beneath `data_dir/sessions`. The native loop emits structured transcript/tool events; the coordinator sanitizes, persists, compacts, and projects them to Telegram. Existing `CodingRun` records remain the approval/shipping source of truth, while a session with the same ID supplies history and recovery state.

**Tech Stack:** Go 1.26, standard library, existing YAML configuration, JSON-file persistence, Telegram Bot API adapter, existing OpenAI-compatible model adapter.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral; register all adapters through `internal/bootstrap`.
- Do not add a database, ORM, web framework, agent framework, native plugin runtime, or external Pi/Hermes runtime.
- Only an explicit owner request can start or resume implementation work. A restart must mark an in-flight session interrupted; it must never resume a model run automatically.
- Persist only sanitized assistant-visible transcript, bounded output excerpts, semantic events, and checkpoints. Never persist hidden reasoning, credentials, unrestricted terminal output, or unredacted active secrets.
- Retain the existing workspace path/environment/timeout/output/process-group restrictions and all independent commit, push, and pull-request approvals. Protected branches remain unpushable.
- Preserve `/data/state.json` schema compatibility. Session persistence is separate under `data_dir/sessions`; existing coding runs remain shippable.
- `runner.root` must be a descendant of `data_dir` so an uncommitted workspace survives a Railway restart. Default and documented deployment value: `/data/runs`.
- Use test-first changes. Finish with `make fmt vet test race build`; run `make smoke` when Docker is available.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/ports/ports.go` | Provider-neutral session records, event records, and `ImplementationSessionStore` contract. |
| `internal/kernel/services/implementation_sessions.go` | Session statuses, deterministic title/recap/event classification, redaction, context compaction, and session lifecycle coordinator. |
| `internal/kernel/services/implementation_sessions_test.go` | Unit tests for redaction, compacted context, resumability, lifecycle transitions, and approval invalidation decisions. |
| `internal/adapters/sessions/jsonfile/store.go` | Atomic `session.json`/`context.json` writes and append-only `events.jsonl` persistence. |
| `internal/adapters/sessions/jsonfile/store_test.go` | Adapter durability, event ordering, atomicity, and reload tests. |
| `internal/kernel/agent/loop.go` | Emits transcript/tool lifecycle events and returns complete implementation history. |
| `internal/kernel/agent/loop_test.go` | Regression coverage for event order, terminal calls, tool errors, and preserved messages. |
| `internal/kernel/services/implementer.go` | Rebuilds a native-loop request from a saved session context and forwards structured events. |
| `internal/kernel/services/implementer_test.go` | Verifies a resumed transcript reaches the model and semantic events replace raw tool labels. |
| `internal/kernel/services/coding.go` | Starts/resumes a durable workspace, records session lifecycle, and retains workspace on interruption. |
| `internal/kernel/services/coding_test.go` | Covers start, explicit resume, restart recovery, missing workspace, and diff change after pending approval. |
| `internal/kernel/services/repository_tools.go` | Adds the lane-gated `repository_continue` tool and returns a deterministic session result. |
| `internal/kernel/services/repository_tools_test.go` | Covers explicit continuation and rejects unknown/non-resumable sessions. |
| `internal/kernel/services/approval_service.go` and `internal/kernel/approvals/approvals.go` | Add explicit invalidation for stale pending approvals. |
| `internal/kernel/services/shipping.go` and `internal/kernel/services/shipping_test.go` | Record post-approval session milestones without changing payload validation. |
| `internal/kernel/lane/lane.go` and `internal/kernel/lane/lane_test.go` | Grant implementation capability only for explicit resume/continue lifecycle language. |
| `internal/bootstrap/config.go`, `config.example.yaml`, `README.md` | Configure durable roots and per-model context budget; document `/continue`. |
| `internal/bootstrap/app.go`, `internal/bootstrap/commands.go`, `internal/bootstrap/*_test.go` | Wire the store/coordinator and provide deterministic `/continue [run-id] [instruction...]`. |
| `internal/adapters/channels/telegram/progress_tracker.go`, `commands.go`, and tests | Render accumulated concise milestones and advertise the continue shortcut. |

## Task 1: Define the provider-neutral session model and kernel coordinator

**Files:**
- Modify: `internal/ports/ports.go`
- Create: `internal/kernel/services/implementation_sessions.go`
- Create: `internal/kernel/services/implementation_sessions_test.go`

**Interfaces:**
- Produces `ports.ImplementationSession`, `ports.ImplementationSessionEvent`, `ports.SessionContext`, and `ports.ImplementationSessionStore`.
- Produces `services.ImplementationSessions`, used by `CodingService`, `ShippingService`, repository tools, and bootstrap recovery.
- The store is called only through `Create`, `Load`, `ListResumable`, `AppendEvent`, and `Update`; filesystem and JSON details are not exposed to the kernel.

- [ ] **Step 1: Write failing domain tests for session identity, summaries, redaction, and compaction**

```go
func TestSessionCoordinatorCreatesOwnerTriggeredSession(t *testing.T) {
	store := newMemorySessionStore()
	sessions := NewImplementationSessions(store, SessionPolicy{ContextBudgetChars: 200, RecentMessages: 2, OutputExcerptChars: 80}, fixedNow)
	session, err := sessions.Create(context.Background(), ports.ImplementationSession{
		ID: "run-1", Repository: "eggy", Instruction: "Add resumable sessions", Workspace: "/data/runs/run-1",
	})
	if err != nil || session.Status != ports.SessionCreated || session.Title != "Add resumable sessions" {
		t.Fatalf("session=%#v err=%v", session, err)
	}
}

func TestSessionCoordinatorCompactsAndRedactsTranscript(t *testing.T) {
	sessions := NewImplementationSessions(newMemorySessionStore(), SessionPolicy{ContextBudgetChars: 40, RecentMessages: 1, OutputExcerptChars: 12}, fixedNow, "live-secret")
	if _, err := sessions.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Instruction: "test"}); err != nil { t.Fatal(err) }
	_, err := sessions.Append(context.Background(), "run-1", ports.ImplementationSessionEvent{
		Kind: ports.SessionToolResult, ToolName: "terminal", Content: "live-secret output that exceeds the retained budget",
	})
	if err != nil { t.Fatal(err) }
	session, _ := sessions.Load(context.Background(), "run-1")
	if strings.Contains(session.Context.Summary, "live-secret") || len(session.Context.RecentMessages) != 1 {
		t.Fatalf("context=%#v", session.Context)
	}
}
```

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/kernel/services -run 'TestSessionCoordinator(CreatesOwnerTriggeredSession|CompactsAndRedactsTranscript)' -count=1`

Expected: FAIL because the session types and coordinator do not exist.

- [ ] **Step 3: Add the port records and store contract**

Add provider-neutral records in `internal/ports/ports.go`; reuse `ports.Message` for retained model-visible history.

```go
type ImplementationSessionStatus string

const (
	SessionCreated                ImplementationSessionStatus = "created"
	SessionRunning                ImplementationSessionStatus = "running"
	SessionInterrupted            ImplementationSessionStatus = "interrupted"
	SessionBlocked                ImplementationSessionStatus = "blocked"
	SessionAwaitingCommitApproval ImplementationSessionStatus = "awaiting_commit_approval"
	SessionCommitted              ImplementationSessionStatus = "committed"
	SessionAwaitingPushApproval   ImplementationSessionStatus = "awaiting_push_approval"
	SessionPushed                 ImplementationSessionStatus = "pushed"
	SessionAwaitingPRApproval     ImplementationSessionStatus = "awaiting_pr_approval"
	SessionCompleted              ImplementationSessionStatus = "completed"
	SessionCancelled              ImplementationSessionStatus = "cancelled"
)

type SessionContext struct {
	Summary        string    `json:"summary,omitempty"`
	RecentMessages []Message `json:"recent_messages,omitempty"`
}

type ImplementationSession struct {
	ID, Title, Repository, Instruction, Workspace, Branch, BaseRevision, Model, PromptVersion string
	Status ImplementationSessionStatus
	Context SessionContext
	StartedAt, UpdatedAt time.Time
}

type ImplementationSessionEvent struct {
	Sequence uint64
	At       time.Time
	Kind     string
	Message  string
	ToolName string
	Content  string
	ModelMessage Message
}

type ImplementationSessionStore interface {
	Create(context.Context, ImplementationSession) (ImplementationSession, error)
	Load(context.Context, string) (ImplementationSession, error)
	ListResumable(context.Context) ([]ImplementationSession, error)
	AppendEvent(context.Context, string, ImplementationSessionEvent) (ImplementationSession, error)
	Update(context.Context, string, func(*ImplementationSession) error) (ImplementationSession, error)
}
```

- [ ] **Step 4: Implement deterministic kernel policy and coordinator**

Create `implementation_sessions.go` with a `SessionPolicy` and coordinator. The coordinator must:

```go
type SessionPolicy struct {
	ContextBudgetChars int
	RecentMessages     int
	OutputExcerptChars int
}

type ImplementationSessions struct { /* store, policy, now, redactor */ }

func (s *ImplementationSessions) Create(ctx context.Context, input ports.ImplementationSession) (ports.ImplementationSession, error)
func (s *ImplementationSessions) Append(ctx context.Context, id string, event ports.ImplementationSessionEvent) (ports.ImplementationSession, error)
func (s *ImplementationSessions) ResumeContext(ctx context.Context, id string) ([]ports.Message, ports.ImplementationSession, error)
func (s *ImplementationSessions) MarkInterrupted(ctx context.Context) (int, error)
func (s *ImplementationSessions) SetStatus(ctx context.Context, id string, status ports.ImplementationSessionStatus, message string) error
```

Generate a title from the trimmed first instruction line, capped at 80 runes. Redact active secret values and the existing credential-shaped patterns before appending an event. Bound tool output before persistence. Build compaction summaries from deterministic semantic event messages and retain the configured tail of `ports.Message` values. Do not call a model during compaction.

- [ ] **Step 5: Run focused domain tests to verify they pass**

Run: `go test ./internal/kernel/services -run 'TestSessionCoordinator' -count=1`

Expected: PASS, including redaction and bounded-context assertions.

- [ ] **Step 6: Commit the session domain contract**

```bash
git add internal/ports/ports.go internal/kernel/services/implementation_sessions.go internal/kernel/services/implementation_sessions_test.go
git commit -m "feat: add implementation session domain"
```

### Task 2: Persist sessions atomically beneath the durable data directory

**Files:**
- Create: `internal/adapters/sessions/jsonfile/store.go`
- Create: `internal/adapters/sessions/jsonfile/store_test.go`

**Interfaces:**
- Consumes `ports.ImplementationSessionStore` from Task 1.
- Produces `jsonfile.Open(root string) ports.ImplementationSessionStore` for bootstrap.
- Writes `<root>/<id>/session.json`, `<root>/<id>/context.json`, and `<root>/<id>/events.jsonl` with `0600` files and `0700` directories.

- [ ] **Step 1: Write failing adapter tests for event durability and atomic metadata/context writes**

```go
func TestStoreReloadsSessionAndOrderedEvents(t *testing.T) {
	root := t.TempDir()
	store := Open(root)
	if _, err := store.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Status: ports.SessionCreated}); err != nil { t.Fatal(err) }
	if _, err := store.AppendEvent(context.Background(), "run-1", ports.ImplementationSessionEvent{Kind: ports.SessionToolResult, Message: "Inspected: README.md"}); err != nil { t.Fatal(err) }
	loaded, err := Open(root).Load(context.Background(), "run-1")
	if err != nil || loaded.ID != "run-1" { t.Fatalf("session=%#v err=%v", loaded, err) }
	body, err := os.ReadFile(filepath.Join(root, "run-1", "events.jsonl"))
	if err != nil || !strings.Contains(string(body), "Inspected: README.md") { t.Fatalf("events=%q err=%v", body, err) }
}
```

- [ ] **Step 2: Run the adapter test to verify it fails**

Run: `go test ./internal/adapters/sessions/jsonfile -run TestStoreReloadsSessionAndOrderedEvents -count=1`

Expected: FAIL because the adapter package does not exist.

- [ ] **Step 3: Implement the JSON-file adapter**

Use a per-session process lock and the existing atomic-write pattern from `internal/adapters/state/jsonfile/store.go`. Assign event sequence numbers under the lock. Append one JSON object per line to `events.jsonl`, then atomically write the changed `session.json` and `context.json`; never rewrite the event log. Reject malformed IDs using the runner's existing safe run-ID shape. `ListResumable` returns only `interrupted`, `blocked`, `created`, and `awaiting_commit_approval` sessions sorted by `UpdatedAt` descending.

- [ ] **Step 4: Add missing-path, atomic-cleanup, and non-resumable-state tests**

```go
func TestStoreDoesNotLeaveTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	store := Open(root)
	_, _ = store.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Status: ports.SessionCreated})
	paths, _ := filepath.Glob(filepath.Join(root, "run-1", ".*"))
	if len(paths) != 0 { t.Fatalf("temporary files=%v", paths) }
}
```

- [ ] **Step 5: Run the adapter package tests**

Run: `go test ./internal/adapters/sessions/jsonfile -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the durable session adapter**

```bash
git add internal/adapters/sessions/jsonfile
git commit -m "feat: persist implementation sessions"
```

### Task 3: Make the native loop preserve transcript and emit structured tool events

**Files:**
- Modify: `internal/kernel/agent/loop.go`
- Modify: `internal/kernel/agent/loop_test.go`
- Modify: `internal/kernel/services/implementer.go`
- Modify: `internal/kernel/services/implementer_test.go`

**Interfaces:**
- Consumes `ports.ImplementationSessionEvent` and `ImplementationSessions.Append` from Tasks 1–2.
- Produces `agent.ImplementationRunResult{Terminal, Usage, Messages}` and `agent.ImplementationEvent` callbacks.
- `NativeImplementer` accepts previously retained `[]ports.Message` and emits semantic `ports.CodingProgress` records rather than `used <tool>`.

- [ ] **Step 1: Write failing loop tests for exact transcript/event ordering**

```go
func TestRunImplementationEmitsTranscriptForToolCallAndResult(t *testing.T) {
	// queued model: assistant read_file call, then finish_implementation call
	var events []ImplementationEvent
	result, err := loop.RunImplementation(ctx, "model", initial, "finish_implementation", func(event ImplementationEvent) { events = append(events, event) })
	if err != nil { t.Fatal(err) }
	if got, want := []string{events[0].Kind, events[1].Kind}, []string{"tool_start", "tool_end"}; !reflect.DeepEqual(got, want) { t.Fatalf("events=%#v", events) }
	if len(result.Messages) != 4 || result.Messages[1].Role != ports.RoleAssistant || result.Messages[2].Role != ports.RoleTool { t.Fatalf("messages=%#v", result.Messages) }
}
```

- [ ] **Step 2: Run the focused loop test to verify it fails**

Run: `go test ./internal/kernel/agent -run TestRunImplementationEmitsTranscriptForToolCallAndResult -count=1`

Expected: FAIL because `ImplementationEvent` and `ImplementationRunResult` do not exist.

- [ ] **Step 3: Replace the name-only callback with structured events and a returned transcript**

Define these agent-local types and update all callers/tests:

```go
type ImplementationEvent struct {
	Kind     string // assistant_message, tool_start, tool_end, tool_error, terminal
	Call     ports.ToolCall
	Output   string
	Err      error
	Message  ports.Message
}

type ImplementationRunResult struct {
	Terminal json.RawMessage
	Usage    ports.ModelUsage
	Messages []ports.Message
}

func (l *Loop) RunImplementation(
	ctx context.Context, alias string, messages []ports.Message, terminalTool string,
	onEvent func(ImplementationEvent),
) (ImplementationRunResult, error)
```

Emit the assistant message before each tool call, a start event before execution, and an end/error event after execution. Preserve tool-error messages in the returned history, retain terminal tool arguments, and keep existing step-limit and unknown-tool behavior unchanged.

- [ ] **Step 4: Update `NativeImplementer` to restore history and classify events**

Replace the positional `Implement` parameters with a request object that carries prior context:

```go
type ImplementationRequest struct {
	RunID, Workspace, Instruction string
	History []ports.Message
}

type Implementer interface {
	Implement(context.Context, ImplementationRequest, func(ports.ImplementationSessionEvent), func(ports.CodingProgress)) (ports.CodingResult, error)
	Interrupt(runID string) error
}
```

Build messages as system contract, optional compact session-summary system message, retained history, then the current owner instruction. Map `read_file`, `patch`, `write_file`, and `terminal` events to deterministic milestone text; include a terminal command's exit status but never its full output in a `CodingProgress` message.

- [ ] **Step 5: Run focused loop and implementer tests**

Run: `go test ./internal/kernel/agent ./internal/kernel/services -run 'TestRunImplementation|TestNativeImplementer' -count=1`

Expected: PASS. Assert the Telegram-facing progress is `Inspected: ...` or `Validation: ...`, never `used terminal`.

- [ ] **Step 6: Commit the eventful native loop**

```bash
git add internal/kernel/agent/loop.go internal/kernel/agent/loop_test.go internal/kernel/services/implementer.go internal/kernel/services/implementer_test.go
git commit -m "feat: retain native implementation transcripts"
```

### Task 4: Orchestrate start, explicit resume, recovery, and approval invalidation

**Files:**
- Modify: `internal/kernel/services/coding.go`
- Modify: `internal/kernel/services/coding_test.go`
- Modify: `internal/kernel/services/approval_service.go`
- Modify: `internal/kernel/approvals/approvals.go`
- Modify: `internal/kernel/services/shipping.go`
- Modify: `internal/kernel/services/shipping_test.go`

**Interfaces:**
- Consumes `ImplementationSessions`, the eventful `Implementer`, and the JSON-file store from Tasks 1–3.
- Produces `CodingService.Resume(ctx, runID, instruction, progress)` and `CodingService.RecoverSessions(ctx)`.
- Produces `ApprovalService.Invalidate(ctx, id, reason)` and `approvals.Invalidated`; all shipping payload equality checks remain as they are.

- [ ] **Step 1: Write failing tests for an explicit resume on the same branch/workspace**

```go
func TestCodingServiceResumeUsesPersistedWorkspaceAndContext(t *testing.T) {
	service, sessions, implementer := newSessionCodingService(t, ports.SessionInterrupted)
	run, result, err := service.Resume(context.Background(), "run-1", "continue and fix the test", nil)
	if err != nil { t.Fatal(err) }
	if run.ID != "run-1" || implementer.workspace != "/data/runs/run-1" || !strings.Contains(implementer.history[0].Content, "Previous session") {
		t.Fatalf("run=%#v implementer=%#v", run, implementer)
	}
	if result.CommitMessage == "" || sessions.status("run-1") != ports.SessionAwaitingCommitApproval { t.Fatal("session was not resumed") }
}
```

- [ ] **Step 2: Run the resume test to verify it fails**

Run: `go test ./internal/kernel/services -run TestCodingServiceResumeUsesPersistedWorkspaceAndContext -count=1`

Expected: FAIL because `Resume` and session dependencies do not exist.

- [ ] **Step 3: Inject the session coordinator into `CodingService` and implement start/resume transitions**

Update construction and methods:

```go
func NewCodingService(store ports.StateStore, runner ports.Runner, repository ports.CodingRepository, implementer Implementer, sessions *ImplementationSessions, now func() time.Time) *CodingService
func (s *CodingService) Start(ctx context.Context, runID string, repository ports.Repository, instruction string, progress func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error)
func (s *CodingService) Resume(ctx context.Context, runID, instruction string, progress func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error)
```

`Start` creates the session before the first Telegram progress event, writes the persistent workspace/branch/base revision as each becomes known, then delegates to the existing clone/branch/branch-and-HEAD checks. `Resume` loads a resumable session and its `CodingRun`, verifies the workspace revision matches its saved branch/base revision, obtains `ResumeContext`, and runs the same implementer without cloning or branching. Both paths persist a sanitized event before calling the progress callback.

- [ ] **Step 4: Add stale approval invalidation before resumed editing**

Add `Invalidated approvals.Status` and an approval-service method that can invalidate a pending approval only:

```go
func (s *ApprovalService) Invalidate(ctx context.Context, id, reason string) error
```

When a session is resumed from `awaiting_commit_approval`, invalidate its pending commit approval before the first new tool call. The subsequent completed diff must request a new commit approval. Keep `ShippingService.Commit`'s branch/revision/diff digest comparisons intact as a second, independent defence.

- [ ] **Step 5: Record shipping milestones without changing authorization semantics**

Inject a narrow session lifecycle recorder into `ShippingService` and record `committed`, `awaiting_push_approval`, `pushed`, `awaiting_pr_approval`, and `completed` only after the existing corresponding action/request has succeeded. Do not let the recorder authorize or execute git work.

```go
type SessionLifecycle interface {
	SetStatus(context.Context, string, ports.ImplementationSessionStatus, string) error
}
```

- [ ] **Step 6: Add recovery and safety regression tests**

Cover all of the following in `coding_test.go` and `shipping_test.go`:

```go
func TestRecoverSessionsMarksRunningInterruptedWithoutCallingImplementer(t *testing.T) { /* expect status interrupted and zero model calls */ }
func TestResumeRejectsMissingWorkspaceOrMismatchedBranch(t *testing.T) { /* expect blocked session and no new branch */ }
func TestResumeInvalidatesOldCommitApprovalAndRequestsNewExactApproval(t *testing.T) { /* old status invalidated; new payload has new diff */ }
func TestShippingStillRejectsChangedDiffAfterApproval(t *testing.T) { /* existing ErrPayloadChanged assertion remains */ }
```

- [ ] **Step 7: Run focused service and approval tests**

Run: `go test ./internal/kernel/services ./internal/kernel/approvals -run 'TestCodingService|TestRecoverSessions|TestResume|TestShipping|TestApproval' -count=1`

Expected: PASS, including protected-branch and changed-payload regression coverage.

- [ ] **Step 8: Commit session orchestration**

```bash
git add internal/kernel/services/coding.go internal/kernel/services/coding_test.go internal/kernel/services/approval_service.go internal/kernel/approvals/approvals.go internal/kernel/services/shipping.go internal/kernel/services/shipping_test.go
git commit -m "feat: resume durable implementation sessions"
```

### Task 5: Expose continuation only through explicit owner actions

**Files:**
- Modify: `internal/kernel/lane/lane.go`
- Modify: `internal/kernel/lane/lane_test.go`
- Modify: `internal/kernel/services/repository_tools.go`
- Modify: `internal/kernel/services/repository_tools_test.go`
- Modify: `internal/bootstrap/commands.go`
- Modify: `internal/bootstrap/commands_test.go`
- Modify: `internal/adapters/channels/telegram/commands.go`
- Modify: `internal/adapters/channels/telegram/commands_test.go`

**Interfaces:**
- Consumes `CodingService.Resume` from Task 4.
- Produces `repository_continue` for the implementation lane and deterministic `/continue [run-id] [instruction...]`.
- No scheduled event, heartbeat, or ordinary chat turn receives the continuation tool.

- [ ] **Step 1: Write failing lane tests for explicit continuation language**

```go
{name: "continue named run", text: "Continue implementation session run-1 and fix the failing test", want: Implementation},
{name: "resume named session", text: "Resume coding session run-1", want: Implementation},
{name: "conversation continue", text: "continue explaining that", want: Assistant},
{name: "negated resume", text: "do not resume the coding session", want: Assistant},
```

- [ ] **Step 2: Run lane tests to verify they fail**

Run: `go test ./internal/kernel/lane -run TestDetect -count=1`

Expected: FAIL because explicit continuation language is not currently an implementation capability.

- [ ] **Step 3: Implement narrow lifecycle detection and `repository_continue`**

Recognize only affirmative phrases containing both `continue`/`resume` and `run`/`session`; retain the existing negation check. Add this implementation-lane-only tool definition:

```go
Name: "repository_continue"
Schema: json.RawMessage(`{"type":"object","properties":{"run_id":{"type":"string","minLength":1},"instruction":{"type":"string"}},"required":["run_id"],"additionalProperties":false}`)
```

It calls `CodingService.Resume`, creates a new independent commit approval only after a changed/finished result, and returns `session_id`, `branch`, concise recap, validation, changed files, and the next approval ID. Do not grant it to assistant/schedule/heartbeat lanes.

- [ ] **Step 4: Add deterministic Telegram command support**

Implement `/continue [run-id] [instruction...]` in `CommandService`. With no ID, load the most recently updated resumable session; with no instruction, use `Continue the approved task, inspect the current state, and complete the next safe implementation step.` Return a clear usage error when there are no resumable sessions. Add `{Name: "continue", Description: "Resume a coding session: /continue [run-id] [instruction]"}` to Telegram commands.

- [ ] **Step 5: Run focused lane/tool/command tests**

Run: `go test ./internal/kernel/lane ./internal/kernel/services ./internal/bootstrap ./internal/adapters/channels/telegram -run 'TestDetect|TestRepository|TestCommand|TestCommandRegistry' -count=1`

Expected: PASS. Assert an ordinary “continue explaining” message never advertises `repository_continue`.

- [ ] **Step 6: Commit explicit continuation controls**

```bash
git add internal/kernel/lane internal/kernel/services/repository_tools.go internal/kernel/services/repository_tools_test.go internal/bootstrap/commands.go internal/bootstrap/commands_test.go internal/adapters/channels/telegram/commands.go internal/adapters/channels/telegram/commands_test.go
git commit -m "feat: add explicit implementation continuation"
```

### Task 6: Wire durable configuration, progress timeline, startup recovery, and acceptance coverage

**Files:**
- Modify: `internal/bootstrap/config.go`
- Modify: `internal/bootstrap/config_test.go`
- Modify: `internal/bootstrap/config_init.go`
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/app_test.go`
- Modify: `internal/adapters/channels/telegram/progress_tracker.go`
- Modify: `internal/adapters/channels/telegram/progress_tracker_test.go`
- Modify: `config.example.yaml`
- Modify: `README.md`

**Interfaces:**
- Consumes `jsonfile.Open(filepath.Join(config.DataDir, "sessions"))` and `ImplementationSessions` from earlier tasks.
- Produces a bootstrap-wired session coordinator, an in-place Telegram timeline, startup interruption recovery, and documented durable Railway configuration.

- [ ] **Step 1: Write failing config tests for durable runner root and context settings**

```go
func TestConfigRejectsRunnerRootOutsideDataDirWhenSessionsEnabled(t *testing.T) {
	cfg, _, err := loadText(t, strings.Replace(validConfigV2(), "root: /tmp/runs", "root: /other/runs", 1), testSecrets())
	if err == nil || !strings.Contains(err.Error(), "runner.root must be within data_dir") { t.Fatalf("cfg=%#v err=%v", cfg, err) }
}
```

Add `implementation_sessions.context_budget_chars`, `implementation_sessions.recent_messages`, and `implementation_sessions.output_excerpt_chars` to `Config`; default them to `96000`, `16`, and `8192`. Validate each is positive. Change the default runner root to `filepath.Join(data_dir, "runs")` after `data_dir` defaults.

- [ ] **Step 2: Run configuration tests to verify they fail**

Run: `go test ./internal/bootstrap -run 'TestConfig|TestLoadConfig' -count=1`

Expected: FAIL because configuration does not yet validate durable workspaces or session budgets.

- [ ] **Step 3: Wire the session adapter and recovery through bootstrap**

In `NewApp`, construct the adapter and coordinator once:

```go
sessionStore := sessionjson.Open(filepath.Join(config.DataDir, "sessions"))
sessions := services.NewImplementationSessions(sessionStore, services.SessionPolicy{
	ContextBudgetChars: config.ImplementationSessions.ContextBudgetChars,
	RecentMessages: config.ImplementationSessions.RecentMessages,
	OutputExcerptChars: config.ImplementationSessions.OutputExcerptChars,
}, options.Now, activeSecrets...)
```

Pass `sessions` to `NewCodingService`, `NewShippingService`, repository tools, and `CommandService`. In `App.Run`, call session recovery before accepting work; it marks persisted `running` sessions interrupted and emits no model request.

- [ ] **Step 4: Change Telegram progress from overwritten raw tool labels to a bounded milestone timeline**

Modify `ProgressTracker` to hold the latest 6 non-empty semantic milestones per run and edit the tracked message with a heading plus joined timeline:

```go
Implementation session run-1
• Inspected: README.md
• Edited: internal/kernel/services/coding.go
• Validation: go test ./... passed
```

On a terminal `completed`, `blocked`, `interrupted`, or `error` event, retain the final timeline as the last status message and clear only the in-memory tracking cursor. If an edit fails after restart, send a new timeline message as today. Do not include event `Content` in Telegram output.

- [ ] **Step 5: Add end-to-end acceptance transcripts**

Extend `TestRepositoryModifyReachesCommitApprovalThroughNativeImplementer` and add a resume transcript that proves:

```go
// first explicit implementation: read_file -> finish_implementation -> commit approval
// simulated restart: running session becomes interrupted, with no model request
// /continue run-id: model receives compact prior context, reads/patches/tests, then receives a fresh commit approval
// approval click: existing commit -> push -> PR flow still executes only after each separate decision
```

Assert the Telegram payload contains `Inspected:` and `Ready for commit approval`, and does not contain `used terminal`. Assert all workspace paths begin with the configured `data_dir/runs` root.

- [ ] **Step 6: Update config and operator documentation**

Change `config.example.yaml` to:

```yaml
data_dir: "/data"
runner:
  root: "/data/runs"
implementation_sessions:
  context_budget_chars: 96000
  recent_messages: 16
  output_excerpt_chars: 8192
```

Update README setup wording to require a volume-backed `data_dir` and `runner.root` beneath it on Railway. Document `/runs`, `/continue [run-id] [instruction...]`, explicit-only resumption, compact Telegram milestones, and the unchanged commit → push → PR approval sequence.

- [ ] **Step 7: Run focused bootstrap/Telegram acceptance tests**

Run: `go test ./internal/bootstrap ./internal/adapters/channels/telegram -run 'TestConfig|TestLoadConfig|TestRepositoryModify|TestCommandService|TestProgressTracker' -count=1`

Expected: PASS, including restart-without-auto-resume and semantic-timeline assertions.

- [ ] **Step 8: Run the complete verification suite**

Run: `make fmt vet test race build`

Expected: exit code 0.

If Docker is available, run: `make smoke`

Expected: exit code 0. If the Docker daemon is unavailable, record that exact limitation in the handoff instead of claiming smoke verification.

- [ ] **Step 9: Commit bootstrap, documentation, and acceptance coverage**

```bash
git add internal/bootstrap internal/adapters/channels/telegram config.example.yaml README.md
git commit -m "feat: wire durable implementation sessions"
```

## Plan self-review

- Spec coverage: Tasks 1–2 create provider-neutral durable session records; Task 3 preserves the inner tool transcript and concise events; Task 4 handles resume, compaction lifecycle, restart recovery, and approval invalidation; Task 5 makes resumption explicitly owner-triggered; Task 6 enforces durable workspaces, improves Telegram, documents Railway operation, and adds end-to-end verification.
- Safety coverage: Tasks 3–6 preserve the inner-only tool registry, runner restrictions, explicit resume gate, protected-branch behavior, and three independent shipping approvals.
- Compatibility coverage: Task 1 keeps `CodingRun` in existing state; Task 2 stores sessions outside `state.json`; Task 6 validates a durable workspace root rather than silently accepting `/tmp` on Railway.
- Placeholder scan: the plan has no deferred implementation markers; every task specifies exact paths, interfaces, commands, expected results, and commit scope.
- Type consistency: `ImplementationSessionStore`, `ImplementationSessions`, `ImplementationRequest`, `CodingService.Resume`, and `SessionLifecycle` are defined before later tasks consume them.
