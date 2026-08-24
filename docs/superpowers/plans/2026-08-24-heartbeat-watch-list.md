# Heartbeat Watch List (Stage 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give Eggy's heartbeat something to watch (`memories/WATCH.md`) and a way to record what it saw without messaging the owner, so a finding worth reporting once is not reported again every interval forever.

**Architecture:** A fourth agent-writable Markdown context document holds the watch list. A `heartbeat_respond` tool — present only in the heartbeat's allowlist — carries a structured `notify` decision plus an optional whole-document rewrite of that list, replacing the current string-sniffing silence protocol. A beat whose watch list is effectively empty is skipped before any model call, and warns once so the silence is distinguishable from a bug.

**Tech Stack:** Go 1.x, standard library only. Tests are stdlib `testing`, table-driven where the existing neighbours are.

**Spec:** `docs/superpowers/specs/2026-08-24-heartbeat-checkins-design.md` (Stage 1 = spec sections 1, 2, and the empty-watch-list half of section 3).

## Global Constraints

- **Read the spec's "Staging" section before starting.** Stage 2 (`active_hours`, `include_recent_history`) is explicitly NOT in this plan. Do not implement them.
- **The watch document holds things to look at, never things with their own cadence.** No intervals, no `at:`, no cron anywhere in `WATCH.md` or its tool schema. Anything wanting a time is a schedule.
- **Unprompted turns stay read-only.** The heartbeat allowlist is `ReadOnlyTools()` plus `heartbeat_respond` and nothing else. It must name no MCP tool and no mutation tool.
- **`internal/kernel` may not import `plugins/`.** This is a standing rule; the kernel talks to stores through `internal/ports` only.
- **Byte budgets are package constants, not config keys.** `DefaultWatchMaxBytes` sits beside `DefaultUserMaxBytes` in `plugins/context/markdown`.
- Go module path is `github.com/nigelteosw/eggy`.
- Run `go test ./...` before every commit. Run `gofmt -w` on every file touched.

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/ports/ports.go` | `ContextWatch`, `AgentContext.Watch`/`.WatchMaxBytes`, `ContextStore.ReplaceDocument` | 1 |
| `internal/home/home.go` | `Layout.Watch()` → `memories/WATCH.md` | 1 |
| `plugins/context/markdown/store.go` | `initialWatch`, `DefaultWatchMaxBytes`, `Paths.Watch`, `ReplaceDocument` | 1 |
| `internal/bootstrap/app_wiring.go` | pass the watch path and budget into the store | 1 |
| `internal/kernel/services/context.go` | `memory` tool accepts `file: "watch"` | 2 |
| `internal/kernel/services/heartbeat_tools.go` | **new** — response carrier + `heartbeat_respond` | 3 |
| `internal/bootstrap/app.go` | register `heartbeat_respond` | 3 |
| `internal/kernel/agent/prompt.go` | `HeartbeatTurnMessage()` teaches the tool protocol | 4 |
| `internal/kernel/turns/turns.go` | `HeartbeatTurn` wires doc + tool; `run` prefers the structured response | 4 |
| `internal/bootstrap/app_events.go` | empty-watch-list skip and its one-time warning | 5 |

---

### Task 1: The watch document

Adds the fourth context document end to end: the port constant, the store's read/write support, the home path, and the wiring. Nothing consumes it yet — that is Task 4.

**Files:**
- Modify: `internal/ports/ports.go:184-216`
- Modify: `internal/home/home.go:57-70`
- Modify: `plugins/context/markdown/store.go:17-95,136-180`
- Modify: `internal/bootstrap/app_wiring.go:70-73`
- Test: `plugins/context/markdown/store_test.go`
- Test: `internal/home/home_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `ports.ContextWatch ports.ContextDocument = "watch"`
  - `ports.AgentContext.Watch string`, `.WatchMaxBytes int64`
  - `ports.ContextStore.ReplaceDocument(ctx context.Context, document ports.ContextDocument, content string) error`
  - `markdown.DefaultWatchMaxBytes int64`
  - `markdown.Paths.Watch string`
  - `home.Layout.Watch() string`

- [ ] **Step 1: Write the failing store test**

Add to `plugins/context/markdown/store_test.go`:

```go
func TestWatchDocumentRoundTrips(t *testing.T) {
	dir := t.TempDir()
	store := InDir(dir, 0, 0)
	ctx := context.Background()

	if err := store.ReplaceDocument(ctx, ports.ContextWatch, "# Eggy Watch\n\nPR #18 open since Aug 20\n"); err != nil {
		t.Fatalf("ReplaceDocument: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(loaded.Watch, "PR #18 open since Aug 20") {
		t.Fatalf("watch=%q", loaded.Watch)
	}
	if loaded.WatchMaxBytes != DefaultWatchMaxBytes {
		t.Fatalf("WatchMaxBytes=%d want %d", loaded.WatchMaxBytes, DefaultWatchMaxBytes)
	}
}

func TestWatchDocumentAcceptsEntryEdits(t *testing.T) {
	dir := t.TempDir()
	store := InDir(dir, 0, 0)
	ctx := context.Background()

	if err := store.AddEntry(ctx, ports.ContextWatch, "PR #18 open since Aug 20"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if err := store.ReplaceEntry(ctx, ports.ContextWatch, "PR #18", "PR #18 open since Aug 20 — mentioned Aug 22"); err != nil {
		t.Fatalf("ReplaceEntry: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(loaded.Watch, "mentioned Aug 22") {
		t.Fatalf("watch=%q", loaded.Watch)
	}
}

// Soul stays load-only through every write path, ReplaceDocument included.
func TestReplaceDocumentRefusesSoul(t *testing.T) {
	store := InDir(t.TempDir(), 0, 0)
	err := store.ReplaceDocument(context.Background(), ports.ContextSoul, "rewritten")
	if err == nil {
		t.Fatal("ReplaceDocument on soul succeeded")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err=%v", err)
	}
}

func TestReplaceDocumentRefusesAnOverBudgetWrite(t *testing.T) {
	store := InDir(t.TempDir(), 0, 0)
	oversized := "# Eggy Watch\n\n" + strings.Repeat("x", int(DefaultWatchMaxBytes)+1)
	err := store.ReplaceDocument(context.Background(), ports.ContextWatch, oversized)
	if err == nil {
		t.Fatal("oversized ReplaceDocument succeeded")
	}
	if !strings.Contains(err.Error(), "is full") {
		t.Fatalf("err=%v", err)
	}
}

// A rejected write must leave the previous content intact: the beat that
// wrote it still has to deliver its notification against a sane document.
func TestReplaceDocumentLeavesContentIntactOnRejection(t *testing.T) {
	store := InDir(t.TempDir(), 0, 0)
	ctx := context.Background()
	if err := store.ReplaceDocument(ctx, ports.ContextWatch, "# Eggy Watch\n\nkeep me\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	oversized := "# Eggy Watch\n\n" + strings.Repeat("x", int(DefaultWatchMaxBytes)+1)
	if err := store.ReplaceDocument(ctx, ports.ContextWatch, oversized); err == nil {
		t.Fatal("oversized write succeeded")
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(loaded.Watch, "keep me") {
		t.Fatalf("watch=%q", loaded.Watch)
	}
}
```

Ensure the file imports `strings`, `context`, and `github.com/nigelteosw/eggy/internal/ports`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./plugins/context/markdown/ -run 'Watch|ReplaceDocument' -v`
Expected: FAIL — `undefined: ports.ContextWatch`, `store.ReplaceDocument undefined`, `undefined: DefaultWatchMaxBytes`.

- [ ] **Step 3: Add the port constant, context fields, and interface method**

In `internal/ports/ports.go`, extend `AgentContext` (currently lines 184-194):

```go
type AgentContext struct {
	Soul   string `json:"soul"`
	User   string `json:"user"`
	Memory string `json:"memory"`
	// Watch is the heartbeat's checklist: what the owner asked Eggy to keep
	// an eye on, and what Eggy has already said about each item. Unlike the
	// other three it is not rendered into every turn's system prompt -- a
	// heartbeat turn appends it itself, so ordinary turns pay nothing for it
	// and it cannot churn the prompt prefix that R6 wants byte-stable.
	Watch string `json:"watch"`
	// UserMaxBytes, MemoryMaxBytes, and WatchMaxBytes are the write budgets
	// ContextStore enforces on the agent-writable documents, used to render an
	// in-context usage indicator. Zero suppresses the indicator. Soul has no
	// budget: it is owner-editable and never agent-written.
	UserMaxBytes   int64 `json:"user_max_bytes,omitempty"`
	MemoryMaxBytes int64 `json:"memory_max_bytes,omitempty"`
	WatchMaxBytes  int64 `json:"watch_max_bytes,omitempty"`
}
```

Add the document constant beside the existing three:

```go
const (
	ContextSoul   ContextDocument = "soul"
	ContextUser   ContextDocument = "user"
	ContextMemory ContextDocument = "memory"
	// ContextWatch is the heartbeat's watch list. It holds things to look at,
	// never things with their own cadence: an entry that wants a time is a
	// schedule and belongs in the schedule store. That rule is what keeps it
	// from becoming a second scheduler, which is what retired the last
	// heartbeat (see retiredConfigFields in internal/config/config_init.go).
	ContextWatch ContextDocument = "watch"
)
```

Add the method to `ContextStore`:

```go
type ContextStore interface {
	Load(context.Context) (AgentContext, error)
	AddEntry(ctx context.Context, document ContextDocument, text string) error
	ReplaceEntry(ctx context.Context, document ContextDocument, oldText, text string) error
	RemoveEntry(ctx context.Context, document ContextDocument, oldText string) error
	// ReplaceDocument overwrites document wholesale. The entry methods above
	// are the right shape for a document edited a fact at a time; a heartbeat
	// rewriting its whole watch list is not that, and expressing it as N
	// substring matches would leave the list half-updated when one missed.
	ReplaceDocument(ctx context.Context, document ContextDocument, content string) error
}
```

- [ ] **Step 4: Implement the store changes**

In `plugins/context/markdown/store.go`, add the initial document beside the others:

```go
const (
	initialSoul   = "# Eggy Soul\n\nI'm Eggy: a small eggy buddy, happiest when quietly useful. Warm and a little playful, never sappy about it. Underneath the smile, still practical, truthful, concise, and evidence-led — say what's actually true, not what sounds nice.\n"
	initialUser   = "# Eggy User\n"
	initialMemory = "# Eggy Memory\n"
	initialWatch  = "# Eggy Watch\n"
)
```

Add the budget:

```go
const (
	DefaultUserMaxBytes   = 2 << 10
	DefaultMemoryMaxBytes = 4 << 10
	// Deliberately the largest of the three: the watch list carries both the
	// items and Eggy's notes about what it already said, and the annotation is
	// the whole anti-repetition mechanism. Still bounded, for the reason the
	// other two are.
	DefaultWatchMaxBytes = 6 << 10
)
```

Extend `Paths` and `Store`:

```go
type Paths struct {
	Soul   string
	User   string
	Memory string
	Watch  string
}

type Store struct {
	paths          Paths
	userMaxBytes   int64
	memoryMaxBytes int64
	watchMaxBytes  int64
	mu             sync.Mutex
}
```

`Open` gains the budget parameter — this is a breaking signature change, and every caller is updated in Step 5:

```go
func Open(paths Paths, userMaxBytes, memoryMaxBytes, watchMaxBytes int64) *Store {
	if userMaxBytes <= 0 {
		userMaxBytes = DefaultUserMaxBytes
	}
	if memoryMaxBytes <= 0 {
		memoryMaxBytes = DefaultMemoryMaxBytes
	}
	if watchMaxBytes <= 0 {
		watchMaxBytes = DefaultWatchMaxBytes
	}
	return &Store{paths: paths, userMaxBytes: userMaxBytes, memoryMaxBytes: memoryMaxBytes, watchMaxBytes: watchMaxBytes}
}

func InDir(dir string, userMaxBytes, memoryMaxBytes int64) *Store {
	return Open(Paths{
		Soul:   filepath.Join(dir, "SOUL.md"),
		User:   filepath.Join(dir, "USER.md"),
		Memory: filepath.Join(dir, "MEMORY.md"),
		Watch:  filepath.Join(dir, "WATCH.md"),
	}, userMaxBytes, memoryMaxBytes, 0)
}
```

`InDir` keeps its two-budget signature so its existing test callers are untouched; it defaults the watch budget.

In `Load`, read the fourth document and return the new fields:

```go
	watch, err := s.loadDocument(s.paths.Watch, initialWatch)
	if err != nil {
		return ports.AgentContext{}, err
	}
	return ports.AgentContext{
		Soul: soul, User: user, Memory: memory, Watch: watch,
		UserMaxBytes: s.userMaxBytes, MemoryMaxBytes: s.memoryMaxBytes, WatchMaxBytes: s.watchMaxBytes,
	}, nil
```

Add the `writableDocument` case:

```go
	case ports.ContextWatch:
		return s.paths.Watch, initialWatch, s.watchMaxBytes, nil
```

Add `ReplaceDocument` beside `RemoveEntry`. It deliberately does not go through `rewrite`: `rewrite` splits into header plus entries so an edit can address one line, and a whole-document write has no use for that split.

```go
// ReplaceDocument overwrites document with content. Unlike the entry methods
// it does not preserve the existing header, because the caller supplied a
// whole document.
//
// The budget is enforced the same way rewrite enforces it, including the
// shrinking-edit escape hatch: a write that leaves the document no larger
// than it found it always proceeds, so a document already over budget can
// still be brought back under.
func (s *Store) ReplaceDocument(ctx context.Context, document ports.ContextDocument, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, initial, maxBytes, err := s.writableDocument(document)
	if err != nil {
		return err
	}
	updated := strings.TrimRight(content, "\n") + "\n"
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(path, func() error {
		current, err := s.loadDocumentUnlocked(path, initial)
		if err != nil {
			return err
		}
		if int64(len(updated)) > maxBytes && len(updated) >= len(current) {
			return fmt.Errorf("%s is full (%d/%d bytes): consolidate or remove entries before adding more", filepath.Base(path), len(updated), maxBytes)
		}
		return atomicfile.Write(path, []byte(updated), 0o600)
	})
}
```

- [ ] **Step 5: Add the home path and update every `Open` caller**

In `internal/home/home.go`, add beside `User()`:

```go
func (l Layout) Watch() string { return filepath.Join(l.Memories(), "WATCH.md") }
```

Update the package doc comment's directory tree — the line currently reading `//	  memories/     MEMORY.md, USER.md` becomes:

```go
//	  memories/     MEMORY.md, USER.md, WATCH.md
```

In `internal/bootstrap/app_wiring.go`, update the store construction (currently lines 70-73):

```go
	opened.context = contextmarkdown.Open(contextmarkdown.Paths{
		Soul: layout.Soul(), User: layout.User(), Memory: layout.Memory(), Watch: layout.Watch(),
	}, contextmarkdown.DefaultUserMaxBytes, contextmarkdown.DefaultMemoryMaxBytes, contextmarkdown.DefaultWatchMaxBytes)
```

- [ ] **Step 6: Update every `ContextStore` fake**

Adding a method to the interface breaks every in-tree implementation. Find them:

Run: `grep -rln "ReplaceEntry" --include='*_test.go' internal plugins`

For each fake that implements `ports.ContextStore`, add:

```go
func (s *fakeContextStore) ReplaceDocument(ctx context.Context, document ports.ContextDocument, content string) error {
	return nil
}
```

Match the receiver name and type to the fake you are editing. If a fake records calls for assertions, record this one the same way its neighbours are recorded rather than returning nil.

- [ ] **Step 7: Add the home path test**

Add to `internal/home/home_test.go`:

```go
func TestWatchLivesUnderMemories(t *testing.T) {
	layout := At("/data")
	if got, want := layout.Watch(), "/data/memories/WATCH.md"; got != want {
		t.Fatalf("Watch()=%q want %q", got, want)
	}
}
```

- [ ] **Step 8: Run the full suite**

Run: `go test ./...`
Expected: PASS. If a package fails to build on `contextmarkdown.Open`, it is a caller missed in Step 5 — add the fourth argument.

- [ ] **Step 9: Commit**

```bash
gofmt -w internal/ports/ports.go internal/home/home.go plugins/context/markdown/store.go internal/bootstrap/app_wiring.go
git add internal/ports/ports.go internal/home/home.go plugins/context/markdown/store.go plugins/context/markdown/store_test.go internal/bootstrap/app_wiring.go internal/home/home_test.go
git commit -m "Add the WATCH.md context document

A fourth agent-writable Markdown document, the thing a heartbeat looks
at. ContextStore gains ReplaceDocument because a beat rewrites its whole
watch list at once, and expressing that as N substring matches leaves
the list half-updated when one misses."
```

---

### Task 2: The `memory` tool writes the watch list

Lets the owner curate the list conversationally — "keep an eye on the deploy" — without a new tool.

**Files:**
- Modify: `internal/kernel/services/context.go:66-72,139-147`
- Test: `internal/kernel/services/context_test.go`

**Interfaces:**
- Consumes: `ports.ContextWatch` (Task 1).
- Produces: the `memory` tool accepts `file: "watch"`.

- [ ] **Step 1: Write the failing test**

Add to `internal/kernel/services/context_test.go`, matching the existing setup helper in that file:

```go
func TestMemoryToolWritesTheWatchList(t *testing.T) {
	store := &recordingContextStore{}
	tools := NewContextTools(store, NewSecretGuard(nil))

	_, err := tools[0].Execute(context.Background(), json.RawMessage(`{"action":"add","file":"watch","text":"deploy on Railway — check it settles"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if store.document != ports.ContextWatch {
		t.Fatalf("document=%q want %q", store.document, ports.ContextWatch)
	}
}

func TestMemoryToolRejectsAnUnknownFile(t *testing.T) {
	tools := NewContextTools(&recordingContextStore{}, NewSecretGuard(nil))
	_, err := tools[0].Execute(context.Background(), json.RawMessage(`{"action":"add","file":"soul","text":"nope"}`))
	if err == nil {
		t.Fatal("writing soul succeeded")
	}
}
```

Reuse the existing fake in that file if one is already named differently — check `internal/kernel/services/context_test.go:32` for the established pattern and follow it rather than adding a second fake.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kernel/services/ -run 'MemoryToolWritesTheWatchList|MemoryToolRejectsAnUnknownFile' -v`
Expected: FAIL — `file must be memory or user`.

- [ ] **Step 3: Implement**

In `internal/kernel/services/context.go`, extend `writableDocument`:

```go
func writableDocument(file string) (ports.ContextDocument, error) {
	switch file {
	case "memory":
		return ports.ContextMemory, nil
	case "user":
		return ports.ContextUser, nil
	case "watch":
		return ports.ContextWatch, nil
	default:
		return "", errors.New("file must be memory, user, or watch")
	}
}
```

Update the schema enum:

```go
var memoryToolSchema = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["add","replace","remove"]},"file":{"type":"string","enum":["memory","user","watch"]},"text":{"type":"string","minLength":1},"old_text":{"type":"string","minLength":1}},"required":["action","file"],"additionalProperties":false}`)
```

Update the description. Note the explicit no-cadence rule — this is the constraint that keeps the watch list from becoming a second scheduler, and the model has to be told it:

```go
const memoryToolDescription = `Curate durable memory across sessions. file "memory" holds reusable knowledge and conventions; file "user" holds stable owner preferences and profile facts; file "watch" holds what the owner asked you to keep an eye on between check-ins.
Actions: "add" appends a new entry (needs text); "replace" rewrites an existing entry (needs old_text and text); "remove" deletes one (needs old_text).
old_text matches an entry by substring and must identify exactly one. "memory" and "user" are already in your context, so there is no read action.
A watch entry is a thing to look at, never a thing with its own schedule. Anything that should happen at a particular time is a schedule: use the schedule tool, not this one.
Store only durable, verified facts. Never store credentials, transient chat, or unsupported assumptions.`
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/kernel/services/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/kernel/services/context.go
git add internal/kernel/services/context.go internal/kernel/services/context_test.go
git commit -m "Let the memory tool curate the watch list

One enum value rather than a second tool, so 'keep an eye on the deploy'
works conversationally. The description carries the no-cadence rule
explicitly: a watch entry that wants a time is a schedule."
```

---

### Task 3: `heartbeat_respond`

The tool that replaces string-sniffing with a decision. Not wired into any turn yet — that is Task 4.

**Files:**
- Create: `internal/kernel/services/heartbeat_tools.go`
- Create: `internal/kernel/services/heartbeat_tools_test.go`
- Modify: `internal/bootstrap/app.go:201-206`

**Interfaces:**
- Consumes: `ports.ContextWatch`, `ports.ContextStore.ReplaceDocument` (Task 1).
- Produces:
  - `services.HeartbeatResponse` struct with fields `Responded bool`, `Notify bool`, `Text string`
  - `services.WithHeartbeatResponse(ctx context.Context) (context.Context, *HeartbeatResponse)`
  - `services.NewHeartbeatTools(store ports.ContextStore, guard *SecretGuard) []ports.Tool`
  - `services.HeartbeatRespondToolName = "heartbeat_respond"`

- [ ] **Step 1: Write the failing test**

Create `internal/kernel/services/heartbeat_tools_test.go`:

```go
package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

type watchStore struct {
	content  string
	document ports.ContextDocument
	err      error
}

func (s *watchStore) Load(context.Context) (ports.AgentContext, error) {
	return ports.AgentContext{Watch: s.content}, nil
}
func (s *watchStore) AddEntry(context.Context, ports.ContextDocument, string) error { return nil }
func (s *watchStore) ReplaceEntry(context.Context, ports.ContextDocument, string, string) error {
	return nil
}
func (s *watchStore) RemoveEntry(context.Context, ports.ContextDocument, string) error { return nil }
func (s *watchStore) ReplaceDocument(_ context.Context, document ports.ContextDocument, content string) error {
	if s.err != nil {
		return s.err
	}
	s.document, s.content = document, content
	return nil
}

func TestHeartbeatRespondRecordsANotification(t *testing.T) {
	store := &watchStore{}
	tool := NewHeartbeatTools(store, NewSecretGuard(nil))[0]
	ctx, response := WithHeartbeatResponse(context.Background())

	if _, err := tool.Execute(ctx, json.RawMessage(`{"notify":true,"notification_text":"PR #18 has been open three days"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !response.Responded || !response.Notify {
		t.Fatalf("response=%+v", response)
	}
	if response.Text != "PR #18 has been open three days" {
		t.Fatalf("Text=%q", response.Text)
	}
}

// The silent path is the whole anti-repetition mechanism: seen, recorded,
// owner not messaged.
func TestHeartbeatRespondStaysSilentAndStillWritesTheWatchList(t *testing.T) {
	store := &watchStore{}
	tool := NewHeartbeatTools(store, NewSecretGuard(nil))[0]
	ctx, response := WithHeartbeatResponse(context.Background())

	raw := json.RawMessage(`{"notify":false,"watch":"# Eggy Watch\n\nPR #18 open since Aug 20 — mentioned Aug 22\n"}`)
	if _, err := tool.Execute(ctx, raw); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !response.Responded || response.Notify {
		t.Fatalf("response=%+v", response)
	}
	if store.document != ports.ContextWatch {
		t.Fatalf("document=%q", store.document)
	}
	if !strings.Contains(store.content, "mentioned Aug 22") {
		t.Fatalf("content=%q", store.content)
	}
}

func TestHeartbeatRespondRequiresTextWhenNotifying(t *testing.T) {
	tool := NewHeartbeatTools(&watchStore{}, NewSecretGuard(nil))[0]
	ctx, _ := WithHeartbeatResponse(context.Background())
	if _, err := tool.Execute(ctx, json.RawMessage(`{"notify":true}`)); err == nil {
		t.Fatal("notify with no text succeeded")
	}
}

func TestHeartbeatRespondRejectsASecretInTheWatchList(t *testing.T) {
	tool := NewHeartbeatTools(&watchStore{}, NewSecretGuard([]string{"hunter2"}))[0]
	ctx, _ := WithHeartbeatResponse(context.Background())
	_, err := tool.Execute(ctx, json.RawMessage(`{"notify":false,"watch":"token is hunter2"}`))
	if err == nil {
		t.Fatal("secret-bearing watch list accepted")
	}
}

// A rejected watch write must not lose the notification: the finding is what
// the owner actually needs, and the annotation is best-effort.
func TestHeartbeatRespondKeepsTheNotificationWhenTheWatchWriteFails(t *testing.T) {
	store := &watchStore{err: errors.New("WATCH.md is full (7000/6144 bytes)")}
	tool := NewHeartbeatTools(store, NewSecretGuard(nil))[0]
	ctx, response := WithHeartbeatResponse(context.Background())

	if _, err := tool.Execute(ctx, json.RawMessage(`{"notify":true,"notification_text":"deploy failed","watch":"too big"}`)); err == nil {
		t.Fatal("expected the watch write error to surface to the model")
	}
	if !response.Responded || !response.Notify || response.Text != "deploy failed" {
		t.Fatalf("response=%+v", response)
	}
}

func TestHeartbeatRespondIsReadOnlyClassified(t *testing.T) {
	definition := NewHeartbeatTools(&watchStore{}, NewSecretGuard(nil))[0].Definition()
	if definition.Name != HeartbeatRespondToolName {
		t.Fatalf("name=%q", definition.Name)
	}
	if !definition.Effect.Internal {
		t.Fatalf("effect=%+v want Internal", definition.Effect)
	}
}
```

Add `"errors"` to the import block.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kernel/services/ -run Heartbeat -v`
Expected: FAIL — `undefined: NewHeartbeatTools`, `undefined: WithHeartbeatResponse`.

- [ ] **Step 3: Implement**

Create `internal/kernel/services/heartbeat_tools.go`:

```go
package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// HeartbeatRespondToolName is the heartbeat's own reply channel.
const HeartbeatRespondToolName = "heartbeat_respond"

// HeartbeatResponse is what a beat decided, carried on the turn's context so
// the tool can hand its decision back to the turn that ran it.
//
// A context value rather than a return value because a tool is registered
// once at startup and shared by every turn, while this decision belongs to
// one beat. It mirrors destination.With, which solves the same problem for
// the same reason.
type HeartbeatResponse struct {
	// Responded distinguishes "the model called the tool and chose silence"
	// from "the model never called the tool", which is what lets the turn fall
	// back to the HEARTBEAT_OK text protocol only when it has to.
	Responded bool
	Notify    bool
	Text      string
}

type heartbeatResponseKey struct{}

// WithHeartbeatResponse attaches a fresh response to ctx and returns both. The
// caller reads the response after the loop returns.
func WithHeartbeatResponse(ctx context.Context) (context.Context, *HeartbeatResponse) {
	response := &HeartbeatResponse{}
	return context.WithValue(ctx, heartbeatResponseKey{}, response), response
}

// HeartbeatResponseFromContext returns the response carried on ctx, or nil on
// a turn that is not a heartbeat.
func HeartbeatResponseFromContext(ctx context.Context) *HeartbeatResponse {
	response, _ := ctx.Value(heartbeatResponseKey{}).(*HeartbeatResponse)
	return response
}

const heartbeatRespondDescription = `End your check-in. Call this exactly once, as the last thing you do.
notify=false means say nothing to the owner. That is the normal outcome: check in, find nothing that warrants interrupting them, stay quiet.
notify=true delivers notification_text to the owner's phone. Use it only for something that genuinely warrants interrupting them right now.
watch optionally replaces the whole watch list. Use it to record what you observed, so a later check-in reads your note and does not report the same thing twice — for example "PR #18 open since Aug 20 — mentioned Aug 22". Keep every item the owner put there unless it is genuinely finished; you are annotating their list, not replacing it with your own.
Never put a time, interval, or cron expression in the watch list. Anything that should happen at a particular time is a schedule.`

var heartbeatRespondSchema = json.RawMessage(`{"type":"object","properties":{"notify":{"type":"boolean"},"notification_text":{"type":"string","minLength":1},"watch":{"type":"string"}},"required":["notify"],"additionalProperties":false}`)

type heartbeatRespondTool struct {
	store ports.ContextStore
	guard *SecretGuard
}

// NewHeartbeatTools returns the heartbeat's reply tool. It is registered like
// any other kernel tool but reaches a turn only through the heartbeat's
// allowlist, so it costs no prompt bytes on an ordinary turn.
func NewHeartbeatTools(store ports.ContextStore, guard *SecretGuard) []ports.Tool {
	if guard == nil {
		guard = NewSecretGuard(nil)
	}
	return []ports.Tool{heartbeatRespondTool{store: store, guard: guard}}
}

func (t heartbeatRespondTool) Definition() ports.ToolDefinition {
	// Internal, not ReadOnly: it writes WATCH.md. Like the memory tool, every
	// write lands in a document the owner reads and can edit directly and
	// nowhere else, which is exactly what ports.InternalTool classifies.
	return ports.ToolDefinition{
		Name:        HeartbeatRespondToolName,
		Description: heartbeatRespondDescription,
		Schema:      heartbeatRespondSchema,
		Effect:      ports.InternalTool(),
	}
}

func (t heartbeatRespondTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Notify           bool   `json:"notify"`
		NotificationText string `json:"notification_text"`
		Watch            string `json:"watch"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	if input.Notify && strings.TrimSpace(input.NotificationText) == "" {
		return nil, errors.New("notification_text is required when notify is true")
	}

	response := HeartbeatResponseFromContext(ctx)
	if response == nil {
		return nil, errors.New("heartbeat_respond is only available on a heartbeat turn")
	}
	// Recorded before the watch write, so a rejected annotation still delivers
	// the finding. The finding is what the owner needs; the annotation only
	// saves them from hearing it twice.
	response.Responded = true
	response.Notify = input.Notify
	response.Text = strings.TrimSpace(input.NotificationText)

	if strings.TrimSpace(input.Watch) != "" {
		if err := t.guard.Validate("", input.Watch); err != nil {
			return nil, err
		}
		if err := t.store.ReplaceDocument(ctx, ports.ContextWatch, input.Watch); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(`{"acknowledged":true}`), nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/kernel/services/ -run Heartbeat -v`
Expected: PASS.

- [ ] **Step 5: Register the tool**

In `internal/bootstrap/app.go`, extend `baseTools` (currently lines 201-206):

```go
	baseTools = append(baseTools, services.NewHeartbeatTools(contextStore, services.NewSecretGuard(activeSecrets))...)
```

Place it on the line directly after the existing `NewContextTools` append.

- [ ] **Step 6: Run the full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w internal/kernel/services/heartbeat_tools.go internal/bootstrap/app.go
git add internal/kernel/services/heartbeat_tools.go internal/kernel/services/heartbeat_tools_test.go internal/bootstrap/app.go
git commit -m "Add heartbeat_respond

Replaces a parsed sentinel with a decision. notify:false is the whole
point: a beat records what it saw in WATCH.md without messaging the
owner, so the next beat reads the note and stays quiet.

The decision travels on the turn's context, mirroring destination.With,
because the tool is registered once and shared while the decision
belongs to one beat."
```

---

### Task 4: The heartbeat turn uses both

Wires the document and the tool into `HeartbeatTurn`, and makes `run` prefer the structured decision over the text sentinel.

**Files:**
- Modify: `internal/kernel/agent/prompt.go:214-224`
- Modify: `internal/kernel/turns/turns.go:180-195,243-252,320-405`
- Test: `internal/kernel/turns/turns_test.go`

**Interfaces:**
- Consumes: `services.WithHeartbeatResponse`, `services.HeartbeatRespondToolName`, `ports.AgentContext.Watch` (Tasks 1, 3).
- Produces: `turns.Policy.IncludeWatchDocument bool`; `HeartbeatTurn` behaviour.

- [ ] **Step 1: Write the failing tests**

Add to `internal/kernel/turns/turns_test.go`, following the existing fakes and `newTestService` helper in that file:

```go
// notify:false is silence with a memory -- the point of the whole stage.
func TestHeartbeatRespondSilenceDeliversNothing(t *testing.T) {
	service, channel, loop := newHeartbeatService(t, "# Eggy Watch\n\nPR #18\n")
	loop.onRun = func(ctx context.Context) {
		response := services.HeartbeatResponseFromContext(ctx)
		response.Responded, response.Notify = true, false
	}
	loop.reply = "I looked and annotated the list."

	if err := service.HeartbeatTurn(context.Background(), "check in"); err != nil {
		t.Fatalf("HeartbeatTurn: %v", err)
	}
	if len(channel.delivered) != 0 {
		t.Fatalf("delivered=%v", channel.delivered)
	}
}

func TestHeartbeatRespondNotificationDeliversItsText(t *testing.T) {
	service, channel, loop := newHeartbeatService(t, "# Eggy Watch\n\nPR #18\n")
	loop.onRun = func(ctx context.Context) {
		response := services.HeartbeatResponseFromContext(ctx)
		response.Responded, response.Notify = true, true
		response.Text = "PR #18 has been open three days"
	}
	loop.reply = "HEARTBEAT_OK"

	if err := service.HeartbeatTurn(context.Background(), "check in"); err != nil {
		t.Fatalf("HeartbeatTurn: %v", err)
	}
	if len(channel.delivered) != 1 || channel.delivered[0] != "PR #18 has been open three days" {
		t.Fatalf("delivered=%v", channel.delivered)
	}
}

// The structured decision wins: a model that both calls the tool and pads its
// prose must not deliver the prose.
func TestHeartbeatStructuredResponseBeatsTheTextReply(t *testing.T) {
	service, channel, loop := newHeartbeatService(t, "# Eggy Watch\n\nPR #18\n")
	loop.onRun = func(ctx context.Context) {
		response := services.HeartbeatResponseFromContext(ctx)
		response.Responded, response.Notify = true, false
	}
	loop.reply = "Everything looks fine, here is a long essay about it."

	if err := service.HeartbeatTurn(context.Background(), "check in"); err != nil {
		t.Fatalf("HeartbeatTurn: %v", err)
	}
	if len(channel.delivered) != 0 {
		t.Fatalf("delivered=%v", channel.delivered)
	}
}

// A model that ignores the tool still gets the v1 protocol.
func TestHeartbeatFallsBackToTheSentinelWhenTheToolIsNotCalled(t *testing.T) {
	service, channel, loop := newHeartbeatService(t, "# Eggy Watch\n\nPR #18\n")
	loop.reply = "HEARTBEAT_OK"

	if err := service.HeartbeatTurn(context.Background(), "check in"); err != nil {
		t.Fatalf("HeartbeatTurn: %v", err)
	}
	if len(channel.delivered) != 0 {
		t.Fatalf("delivered=%v", channel.delivered)
	}
}

func TestHeartbeatCarriesTheWatchDocument(t *testing.T) {
	service, _, loop := newHeartbeatService(t, "# Eggy Watch\n\nPR #18 open since Aug 20\n")
	loop.reply = "HEARTBEAT_OK"

	if err := service.HeartbeatTurn(context.Background(), "check in"); err != nil {
		t.Fatalf("HeartbeatTurn: %v", err)
	}
	var found bool
	for _, message := range loop.history {
		if strings.Contains(message.Content, "PR #18 open since Aug 20") {
			found = true
		}
	}
	if !found {
		t.Fatal("the watch document never reached the model")
	}
}

func TestOwnerTurnDoesNotCarryTheWatchDocument(t *testing.T) {
	service, _, loop := newHeartbeatService(t, "# Eggy Watch\n\nPR #18 open since Aug 20\n")
	loop.reply = "sure"

	if err := service.OwnerMessage(context.Background(), "hello", "telegram"); err != nil {
		t.Fatalf("OwnerMessage: %v", err)
	}
	for _, message := range loop.history {
		if strings.Contains(message.Content, "PR #18 open since Aug 20") {
			t.Fatal("the watch document leaked into an owner turn")
		}
	}
}

func TestHeartbeatAllowsOnlyReadOnlyToolsPlusRespond(t *testing.T) {
	allowed := ReadOnlyTools().AllowedTools
	if allowed[services.HeartbeatRespondToolName] {
		t.Fatal("heartbeat_respond must not be in the shared read-only floor")
	}
	options := heartbeatTools()
	if !options.AllowedTools[services.HeartbeatRespondToolName] {
		t.Fatal("heartbeat_respond is missing from the heartbeat allowlist")
	}
	for name := range options.AllowedTools {
		if strings.Contains(name, "mcp") {
			t.Fatalf("heartbeat allowlist names an MCP tool: %q", name)
		}
	}
	if options.AllowedTools["memory"] || options.AllowedTools["schedule"] {
		t.Fatal("heartbeat allowlist names a mutation tool")
	}
}
```

Add a helper beside the existing ones in that file. Adapt the fake field names to whatever `turns_test.go` already calls them — this shows the shape, not necessarily the exact identifiers:

```go
// newHeartbeatService builds a service whose context store returns watch as
// the watch document, and returns the channel and loop fakes for assertions.
func newHeartbeatService(t *testing.T, watch string) (*Service, *fakeChannel, *fakeLoop) {
	t.Helper()
	channel := &fakeChannel{}
	loop := &fakeLoop{}
	service := New(Options{
		Registry:     &fakeRegistry{},
		Conversation: &fakeConversation{},
		Context:      &fakeContextStore{watch: watch},
		Store:        &fakeStateStore{},
		Runtime:      &fakeRuntime{},
		Skills:       &fakeSkills{},
		Loop:         loop,
		Channel:      channel,
	})
	return service, channel, loop
}
```

`fakeLoop` needs two additions: an `onRun func(context.Context)` called at the top of `Run`, and a `history []ports.Message` field capturing the history argument. `fakeContextStore` needs a `watch` field returned as `AgentContext.Watch`, plus the `ReplaceDocument` method from Task 1 Step 6.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kernel/turns/ -run Heartbeat -v`
Expected: FAIL — `undefined: heartbeatTools`, and the delivery assertions fail because `run` still ignores the structured response.

- [ ] **Step 3: Update the heartbeat system message**

In `internal/kernel/agent/prompt.go`, replace `HeartbeatTurnMessage`:

```go
// HeartbeatTurnMessage carries ScheduledTurnMessage's read-only framing plus
// the permission that is the whole point of a heartbeat: saying nothing. The
// protocol lives here rather than in the configured instruction so an owner
// who overrides heartbeat.instruction cannot accidentally delete it.
//
// The sentinel is kept as a fallback for a model that answers in prose
// instead of calling heartbeat_respond, which is why both protocols are
// described.
func HeartbeatTurnMessage() ports.Message {
	return ports.Message{Role: ports.RoleSystem, Content: "Heartbeat: a periodic check-in the owner is not present for. " +
		"Work through the watch list below, then call heartbeat_respond exactly once to end the check-in. " +
		"Staying silent is the normal outcome: report only what genuinely warrants interrupting the owner right now. " +
		"Before reporting anything, read what the watch list already records about that item -- if you have already told them, say nothing and leave the note as it is. " +
		"When you observe something worth remembering, write it back through heartbeat_respond's watch field so a later check-in does not repeat it. " +
		"Read-only observations only; do not imply that you can edit or ship repository changes. " +
		"Never put a time, interval, or cron expression in the watch list -- anything that should happen at a particular time is a schedule. " +
		"If heartbeat_respond is unavailable, reply with exactly " + HeartbeatSentinel + " and nothing else when there is nothing worth saying."}
}

// WatchDocumentMessage carries the watch list into a heartbeat turn. It is a
// per-turn Extra rather than a slot in BuildInstructions so an ordinary owner
// turn neither pays for it nor has its prompt prefix churned by it.
func WatchDocumentMessage(watch string) ports.Message {
	return ports.Message{Role: ports.RoleSystem, Content: "Watch list (memories/WATCH.md), the owner's standing list of what to keep an eye on:\n" + watch}
}
```

- [ ] **Step 4: Wire the turn**

In `internal/kernel/turns/turns.go`, add the policy field beside `SuppressSilentReply`:

```go
	// IncludeWatchDocument appends the watch list as a per-turn system
	// message. Only the heartbeat sets it: the document is the heartbeat's
	// own working memory and has no bearing on a turn the owner is present
	// for.
	IncludeWatchDocument bool
```

Add the allowlist constructor beside `ReadOnlyTools`:

```go
// heartbeatTools is the read-only floor plus the heartbeat's own reply
// channel. heartbeat_respond stays out of ReadOnlyTools because a scheduled
// turn has no beat to end and must always deliver.
func heartbeatTools() agent.RunOptions {
	options := ReadOnlyTools()
	options.AllowedTools[services.HeartbeatRespondToolName] = true
	return options
}
```

Replace `HeartbeatTurn`. The response is attached to the context and
deliberately *not* stored on the `Service` — one `Service` instance serves
every surface, so a field would be shared mutable state across concurrent
turns. `run` reads it back off the context instead:

```go
// HeartbeatTurn is a periodic check-in the owner is not present for. Its
// isolation is ScheduledTurn's, unchanged -- the same read-only allowlist and
// no ambient conversation history -- and the only difference is that it is
// allowed to conclude there is nothing worth saying.
//
// It carries the watch list and heartbeat_respond, which together are what
// let it conclude that it has already said this.
func (s *Service) HeartbeatTurn(ctx context.Context, text string) error {
	ctx, _ = services.WithHeartbeatResponse(ctx)
	return s.run(ctx, text, heartbeatTools(), Policy{
		Extra:                []ports.Message{agent.HeartbeatTurnMessage()},
		SuppressSilentReply:  true,
		IncludeWatchDocument: true,
	})
}
```

In `run`, append the watch document right after the existing `policy.Extra` append (currently line 323):

```go
	history = append(history, policy.Extra...)
	if policy.IncludeWatchDocument && strings.TrimSpace(agentContext.Watch) != "" {
		history = append(history, agent.WatchDocumentMessage(agentContext.Watch))
	}
```

Then replace the suppression check (currently lines 388-392) with the structured-first version:

```go
	// A structured decision wins over the text reply: a model that calls
	// heartbeat_respond has already said what it wants delivered, and its
	// prose is working notes. The sentinel stays as the fallback for a model
	// that answered without calling the tool.
	//
	// Checked before the thinking block, not just before the reply: a silent
	// heartbeat that still pushed its reasoning would be the notification the
	// silence protocol exists to prevent.
	if policy.SuppressSilentReply {
		if response := services.HeartbeatResponseFromContext(ctx); response != nil && response.Responded {
			if !response.Notify {
				return nil
			}
			return s.channel.Deliver(ctx, response.Text)
		}
		if silentReply(result.Message.Content) {
			return nil
		}
	}
```

Note the context used for the lookup must be the one `HeartbeatTurn` created, so confirm `run` is reading its own `ctx` parameter and not `turnContext`.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/kernel/turns/ -v`
Expected: PASS.

- [ ] **Step 6: Run the full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w internal/kernel/agent/prompt.go internal/kernel/turns/turns.go
git add internal/kernel/agent/prompt.go internal/kernel/turns/turns.go internal/kernel/turns/turns_test.go
git commit -m "Give the heartbeat its watch list and reply channel

The watch list rides as a per-turn Extra rather than a prompt slot, so
an owner turn neither pays for it nor has its prefix churned. A
structured heartbeat_respond wins over the text sentinel, which stays as
the fallback for a model that answers in prose."
```

---

### Task 5: Skip a beat with an empty watch list

Closes the loop: an owner who never writes a watch list pays for no model calls, and is told why once.

**Files:**
- Modify: `internal/bootstrap/app_events.go:108-170`
- Test: `internal/bootstrap/heartbeat_test.go`

**Interfaces:**
- Consumes: `ports.AgentContext.Watch` (Task 1), `App.context` (existing, `internal/bootstrap/app.go:68`).
- Produces: `App.watchListIsEmpty(ctx) bool`, `App.warnedEmptyWatch bool`.

- [ ] **Step 1: Write the failing test**

Add to `internal/bootstrap/heartbeat_test.go`:

```go
func TestWatchListEmptiness(t *testing.T) {
	for _, tt := range []struct {
		name  string
		watch string
		empty bool
	}{
		{"absent", "", true},
		{"whitespace", "   \n\n\t\n", true},
		{"headings only", "# Eggy Watch\n\n## Deploys\n", true},
		{"one entry", "# Eggy Watch\n\nPR #18 open since Aug 20\n", false},
		{"entry under a heading", "# Eggy Watch\n\n## Deploys\nRailway settles slowly\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app := &App{context: &stubContextStore{watch: tt.watch}}
			if got := app.watchListIsEmpty(context.Background()); got != tt.empty {
				t.Fatalf("watchListIsEmpty=%v want %v", got, tt.empty)
			}
		})
	}
}

// A silent no-op is indistinguishable from a broken heartbeat, so the first
// skip says so -- but only the first, or a deployment that never adopts the
// watch list warns forever.
func TestEmptyWatchListWarnsOnceAndRunsNoTurn(t *testing.T) {
	app := &App{context: &stubContextStore{watch: "# Eggy Watch\n"}}

	if !app.shouldWarnEmptyWatch() {
		t.Fatal("the first empty-watch skip did not warn")
	}
	if app.shouldWarnEmptyWatch() {
		t.Fatal("a consecutive empty-watch skip warned again")
	}
}

// Re-arming matters: an owner who writes a list, empties it, and forgets
// should be told again.
func TestEmptyWatchWarningRearmsAfterANonEmptyList(t *testing.T) {
	app := &App{context: &stubContextStore{watch: "# Eggy Watch\n"}}
	app.shouldWarnEmptyWatch()
	app.warnedEmptyWatch = false
	if !app.shouldWarnEmptyWatch() {
		t.Fatal("the warning did not re-arm")
	}
}
```

Add the stub to the same file:

```go
type stubContextStore struct{ watch string }

func (s *stubContextStore) Load(context.Context) (ports.AgentContext, error) {
	return ports.AgentContext{Watch: s.watch}, nil
}
func (s *stubContextStore) AddEntry(context.Context, ports.ContextDocument, string) error { return nil }
func (s *stubContextStore) ReplaceEntry(context.Context, ports.ContextDocument, string, string) error {
	return nil
}
func (s *stubContextStore) RemoveEntry(context.Context, ports.ContextDocument, string) error {
	return nil
}
func (s *stubContextStore) ReplaceDocument(context.Context, ports.ContextDocument, string) error {
	return nil
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bootstrap/ -run 'WatchList|EmptyWatch' -v`
Expected: FAIL — `app.watchListIsEmpty undefined`, `app.shouldWarnEmptyWatch undefined`.

- [ ] **Step 3: Implement**

Add the field to `App` in `internal/bootstrap/app.go`, beside `readyLog`:

```go
	// warnedEmptyWatch keeps the empty-watch-list warning to the transition
	// into that state. A deployment that never writes a watch list would
	// otherwise log a warn line every interval forever.
	warnedEmptyWatch bool
```

In `internal/bootstrap/app_events.go`, add beside `heartbeatInstruction`:

```go
// watchListIsEmpty reports whether the watch list holds nothing to check.
//
// Blank lines and Markdown headings do not count: a document that is only its
// own title is what a store returns before anyone has written to it, and
// beating on it would run a model call to look at nothing. An unreadable
// document is treated as non-empty so a store failure degrades into a beat
// rather than into silence.
func (a *App) watchListIsEmpty(ctx context.Context) bool {
	if a.context == nil {
		return true
	}
	agentContext, err := a.context.Load(ctx)
	if err != nil {
		slog.Error("watch list unreadable; beating anyway", "error", err)
		return false
	}
	for _, line := range strings.Split(agentContext.Watch, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return false
	}
	return true
}

// shouldWarnEmptyWatch reports whether this skip is the transition into the
// empty state, and records that it warned.
func (a *App) shouldWarnEmptyWatch() bool {
	if a.warnedEmptyWatch {
		return false
	}
	a.warnedEmptyWatch = true
	return true
}
```

Add the guard to `onHeartbeatTick`, before the worker is started:

```go
func (a *App) onHeartbeatTick(ctx context.Context) {
	// The Active() guard does two things for one line: ticks cannot pile up
	// when a heartbeat outlasts its interval, and a heartbeat never
	// interrupts a live owner conversation.
	if a.turnService.Active() {
		return
	}
	// An empty watch list means the owner has asked for nothing to be
	// watched, so there is nothing to check and no model call to justify.
	// Warned once on the way in, for the same reason the missing-Telegram
	// case warns: a silent no-op is indistinguishable from a broken
	// heartbeat.
	if a.watchListIsEmpty(ctx) {
		if a.shouldWarnEmptyWatch() {
			slog.Warn("heartbeat is configured but memories/WATCH.md is empty; add what Eggy should keep an eye on, or unset heartbeat.interval")
		}
		return
	}
	a.warnedEmptyWatch = false
	// ... existing worker goroutine unchanged
}
```

Confirm `strings` and `context` are imported in `app_events.go`; `strings` already is (used by `heartbeatInstruction`).

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bootstrap/ -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Update the docs**

`internal/bootstrap/docs_consistency_test.go` pins documentation against behaviour — run it and follow any failure it reports.

Add `WATCH.md` to `config.example.yaml`'s `heartbeat:` comment block, and to the docs site wherever `MEMORY.md` and `USER.md` are described:

Run: `grep -rln "MEMORY.md" docs/src/content/docs config.example.yaml README.md`

For each hit, describe `WATCH.md` as: the heartbeat's watch list, holding what to keep an eye on and what Eggy has already said about each item; things to look at, never things with their own schedule.

- [ ] **Step 7: Commit**

```bash
gofmt -w internal/bootstrap/app_events.go internal/bootstrap/app.go
git add internal/bootstrap/app_events.go internal/bootstrap/app.go internal/bootstrap/heartbeat_test.go config.example.yaml docs/ README.md
git commit -m "Skip a beat with an empty watch list

An owner who sets an interval and writes no watch list should pay for no
model calls -- but silence is indistinguishable from a bug, so the
transition into that state warns once, the way the missing-Telegram case
already does."
```

---

## After Stage 1: the soak

Do not start Stage 2 straight away. The spec's staging section explains why, and it is the most important paragraph in this plan.

`notify: false` is a behavioural property, not a code property: every test above can pass while the model writes annotations too mushy to suppress a repeat ("checked, nothing new"), and the anti-repetition then fails silently.

- [ ] Set `heartbeat.interval` to something short (`30m`) in the local config.
- [ ] Write three or four real items into `memories/WATCH.md`.
- [ ] Let it run for a few days against a live model.
- [ ] Read `memories/WATCH.md`. Are the annotations specific enough that a later beat can tell "already reported" from "new"? Did anything get reported twice?
- [ ] If the annotations are mushy, fix `HeartbeatTurnMessage()` in `internal/kernel/agent/prompt.go` — prompt work, not a redesign — and soak again.
- [ ] Only then plan Stage 2 (`active_hours`, `include_recent_history`).
