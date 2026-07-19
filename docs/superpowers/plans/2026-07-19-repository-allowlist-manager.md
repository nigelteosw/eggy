# Repository Allowlist Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner add/remove trusted repositories from Telegram, with additions approval-gated and immediately live — no shell access, no restart.

**Architecture:** The repository allowlist moves from `config.yaml` (static, startup-only) into `state.json` (live, versioned, mutable at runtime), exactly like `Schedules`/`CodingRuns`/`Agent.SelectedModel` already work. `config.yaml`'s `repositories:` list becomes a first-boot-only seed. A new `RepositoriesService` in `internal/kernel/services` owns add (approval-gated, validated against the real remote) and remove (immediate). Every consumer that currently reads a static `map[string]ports.Repository` snapshot built once at boot (`ShippingService`, the `repository_list`/`repository_inspect`/`repository_modify` LLM tools, `ApprovalService`'s protected-branch check, the capability manifest shown to the model) is changed to read `state.Repositories` fresh on every call instead.

**Tech Stack:** Go 1.26, existing ports-and-adapters layering, no new dependencies.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral — no GitHub/YAML-specific types leak out of the `github` adapter package.
- Register the new service only through `internal/bootstrap`.
- Never weaken independent approval checks; protected branches remain unpushable even with approval, and this plan tightens that guarantee rather than loosening it.
- Add or change behavior test-first; run the focused test before the full suite.
- `make fmt vet test race build` must pass before this is done; run `make smoke` if Docker is available.
- Prefer the standard library and small interfaces; no new frameworks or abstractions beyond what's specified below.

---

### Task 1: `RemoteChecker` port, `Repositories` state field, `AddRepository` approval action

**Files:**
- Modify: `internal/ports/ports.go`
- Modify: `internal/kernel/approvals/approvals.go`
- Test: `internal/kernel/approvals/service_test.go` (compile-check only via later tasks; no new test here)

**Interfaces:**
- Produces: `ports.RemoteChecker` interface, `ports.State.Repositories map[string]ports.Repository`, `approvals.AddRepository` action constant — every later task depends on these three.

This task is pure scaffolding with no independently testable behavior of its own, so it's verified by `go build ./...` succeeding, then folded into and exercised by Task 2's real test.

- [ ] **Step 1: Add the `Repositories` field to `ports.State`**

In `internal/ports/ports.go`, add one field to the `State` struct (around line 111-125):

```go
type State struct {
	SchemaVersion       int                           `json:"schema_version"`
	Version             uint64                        `json:"version"`
	RecentMessages      []Message                     `json:"recent_messages,omitempty"`
	ConversationSummary string                        `json:"conversation_summary,omitempty"`
	SelectedRepository  string                        `json:"selected_repository,omitempty"`
	Tasks               map[string]tasks.Task         `json:"tasks,omitempty"`
	Approvals           map[string]approvals.Approval `json:"approvals,omitempty"`
	Schedules           map[string]Schedule           `json:"schedules,omitempty"`
	CodingRuns          map[string]CodingRun          `json:"coding_runs,omitempty"`
	Repositories        map[string]Repository         `json:"repositories,omitempty"`
	ProcessedEvents     map[string]time.Time          `json:"processed_events,omitempty"`
	ProactiveMessages   []time.Time                   `json:"proactive_messages,omitempty"`
	Calendar            CalendarAuth                  `json:"calendar,omitempty"`
	Agent               AgentRuntimeState             `json:"agent,omitempty"`
}
```

- [ ] **Step 2: Add the `RemoteChecker` port**

In `internal/ports/ports.go`, add this near the `RepositoryProvider` interface (after it, around line 254):

```go
type RemoteChecker interface {
	CheckRemote(context.Context, Repository, string) error
}
```

- [ ] **Step 3: Add the `AddRepository` approval action**

In `internal/kernel/approvals/approvals.go`, add one constant to the `Action` block:

```go
const (
	Commit         Action = "commit"
	Push           Action = "push"
	CreatePR       Action = "create_pull_request"
	CalendarCreate Action = "calendar_create"
	CalendarUpdate Action = "calendar_update"
	CalendarDelete Action = "calendar_delete"
	AddRepository  Action = "add_repository"
)
```

- [ ] **Step 4: Verify it compiles**

Run: `go build ./...`
Expected: succeeds (nothing consumes the new field/interface/constant yet, so nothing else can break).

- [ ] **Step 5: Commit**

```bash
git add internal/ports/ports.go internal/kernel/approvals/approvals.go
git commit -m "feat: add repository state field, remote checker port, and add-repository approval action"
```

---

### Task 2: `CheckRemote` on the GitHub adapter

**Files:**
- Modify: `internal/adapters/repositories/github/github.go`
- Test: `internal/adapters/repositories/github/repository_test.go`

**Interfaces:**
- Consumes: `ports.RemoteChecker` (Task 1), existing `Adapter` struct/`askpass` helper/`validBranch` helper already in `github.go`.
- Produces: `(*Adapter) CheckRemote(context.Context, ports.Repository, string) error` — Task 3 depends on this satisfying `ports.RemoteChecker`.

- [ ] **Step 1: Write the failing test**

Add to `internal/adapters/repositories/github/repository_test.go` (uses the existing `createRemote`/`git` helpers already in this file):

```go
func TestCheckRemoteValidatesReachabilityAndBaseBranch(t *testing.T) {
	remote := createRemote(t)
	root := filepath.Join(t.TempDir(), "runs")
	runner, err := localprocess.New(root, []string{"PATH", "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT"}, 10*time.Second, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	adapter := New(runner, "sensitive-token", "https://api.github.test", http.DefaultClient)

	workspace, _ := runner.Create(context.Background(), "check-1")
	if err := adapter.CheckRemote(context.Background(), ports.Repository{Name: "repo", CloneURL: remote, BaseBranch: "main"}, workspace); err != nil {
		t.Fatalf("expected reachable remote with main branch, got %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, ".eggy-askpass-*"))
	if len(matches) != 0 {
		t.Fatalf("askpass leaked: %v", matches)
	}

	workspace, _ = runner.Create(context.Background(), "check-2")
	if err := adapter.CheckRemote(context.Background(), ports.Repository{Name: "repo", CloneURL: remote, BaseBranch: "does-not-exist"}, workspace); err == nil {
		t.Fatal("expected error for missing base branch")
	}

	workspace, _ = runner.Create(context.Background(), "check-3")
	if err := adapter.CheckRemote(context.Background(), ports.Repository{Name: "repo", CloneURL: filepath.Join(t.TempDir(), "nowhere"), BaseBranch: "main"}, workspace); err == nil {
		t.Fatal("expected error for unreachable remote")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/repositories/github/... -run TestCheckRemoteValidatesReachabilityAndBaseBranch -v`
Expected: FAIL with `adapter.CheckRemote undefined (type *Adapter has no field or method CheckRemote)`

- [ ] **Step 3: Implement `CheckRemote`**

Add to `internal/adapters/repositories/github/github.go`, after the existing `RemoteHead` method:

```go
func (a *Adapter) CheckRemote(ctx context.Context, repository ports.Repository, workspace string) error {
	if a.runner == nil {
		return errors.New("repository runner is unavailable")
	}
	if !validBranch(repository.BaseBranch) {
		return errors.New("invalid base branch")
	}
	cleanup, environment, err := a.askpass(filepath.Dir(workspace))
	if err != nil {
		return err
	}
	defer cleanup()
	result, err := a.runner.Execute(ctx, ports.Command{
		Argv: []string{"git", "ls-remote", "--exit-code", "--heads", repository.CloneURL, repository.BaseBranch},
		Dir:  workspace, Env: environment,
	})
	if err != nil {
		return fmt.Errorf("repository is not reachable: %w", err)
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return fmt.Errorf("base branch %q not found in %q", repository.BaseBranch, repository.Name)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/repositories/github/... -run TestCheckRemoteValidatesReachabilityAndBaseBranch -v`
Expected: PASS

- [ ] **Step 5: Run the full adapter package test suite**

Run: `go test ./internal/adapters/repositories/github/... -v`
Expected: all tests PASS (existing `TestGitRepositoryCloneInspectDiffCommitAndPush`, `TestGitHubCreatesPullRequestWithHeaderOnlyCredential`, `TestDiffRejectsTruncatedApprovalMaterial` still pass unchanged)

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/repositories/github/github.go internal/adapters/repositories/github/repository_test.go
git commit -m "feat: add CheckRemote reachability validation to the GitHub adapter"
```

---

### Task 3: `RepositoriesService`

**Files:**
- Create: `internal/kernel/services/repositories.go`
- Create: `internal/kernel/services/repositories_test.go`

**Interfaces:**
- Consumes: `ports.StateStore`, `ports.Runner`, `ports.RemoteChecker` (Task 1/2), `ApprovalRequester` (already defined in `calendar.go`: `Request(context.Context, approvals.Action, any, string) (approvals.Approval, error)`), `ports.ApprovalPolicy` (already in `ports.go`: `Authorize(context.Context, approvals.Action, any, string) error`). Reuses the existing `newMemoryStore()` test fake from `dispatcher_test.go`, `fakeWorkspaceRunner` from `coding_test.go`, and `fakeShippingGateway` from `shipping_test.go` (all in the same `services` package already).
- Produces: `func NewRepositoriesService(store ports.StateStore, runner ports.Runner, checker ports.RemoteChecker, requester ApprovalRequester, policy ports.ApprovalPolicy, newRunID func() string) *RepositoriesService`, methods `List(ctx) (map[string]ports.Repository, error)`, `Get(ctx, name string) (ports.Repository, bool, error)`, `RequestAdd(ctx, name, cloneURL, baseBranch string, protectedBranches []string) (approvals.Approval, error)`, `Remove(ctx, name string) error`, `ExecuteApproved(ctx, approval approvals.Approval) (any, error)`. Task 6 (commands) and Task 7 (app wiring, approval dispatch) depend on this exact signature set.

- [ ] **Step 1: Write the failing tests**

Create `internal/kernel/services/repositories_test.go`:

```go
package services

import (
	"context"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRepositoriesRequestAddValidatesStagesAndPersistsOnApproval(t *testing.T) {
	store := newMemoryStore()
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/check-1"}
	checker := &fakeRemoteChecker{}
	gateway := &fakeShippingGateway{}
	service := NewRepositoriesService(store, runner, checker, gateway, gateway, func() string { return "check-1" })

	approval, err := service.RequestAdd(context.Background(), "eggy", "https://github.com/nigelteosw/eggy.git", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !checker.called || checker.repository.Name != "eggy" || checker.repository.BaseBranch != "main" {
		t.Fatalf("checker=%#v", checker)
	}
	if !runner.created || !runner.destroyed {
		t.Fatalf("scratch workspace not created/destroyed: runner=%#v", runner)
	}

	approval.Payload, _ = jsonMarshal(gateway.payload)
	result, err := service.ExecuteApproved(context.Background(), approval)
	if err != nil {
		t.Fatal(err)
	}
	repository, ok := result.(ports.Repository)
	if !ok || repository.Name != "eggy" || repository.BaseBranch != "main" || len(repository.ProtectedBranches) != 1 || repository.ProtectedBranches[0] != "main" {
		t.Fatalf("result=%#v", result)
	}

	state, _ := store.Load(context.Background())
	if state.Repositories["eggy"].CloneURL != "https://github.com/nigelteosw/eggy.git" {
		t.Fatalf("state=%#v", state.Repositories)
	}
	if gateway.authorized != approvals.AddRepository {
		t.Fatalf("gateway=%#v", gateway)
	}
}

func TestRepositoriesRequestAddRejectsDuplicateNameAndUnreachableRemote(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/check-1"}
	gateway := &fakeShippingGateway{}

	service := NewRepositoriesService(store, runner, &fakeRemoteChecker{}, gateway, gateway, func() string { return "check-1" })
	if _, err := service.RequestAdd(context.Background(), "eggy", "https://github.com/nigelteosw/eggy.git", "main", nil); err == nil {
		t.Fatal("expected duplicate name rejection")
	}

	unreachable := &fakeRemoteChecker{err: errors.New("not reachable")}
	service = NewRepositoriesService(store, runner, unreachable, gateway, gateway, func() string { return "check-1" })
	if _, err := service.RequestAdd(context.Background(), "other", "https://github.com/nigelteosw/other.git", "main", nil); err == nil {
		t.Fatal("expected unreachable remote rejection")
	}
}

func TestRepositoriesExecuteApprovedRequiresAuthorization(t *testing.T) {
	store := newMemoryStore()
	policy := &fakePolicy{err: approvals.ErrExpired}
	service := NewRepositoriesService(store, &fakeWorkspaceRunner{}, &fakeRemoteChecker{}, &fakeShippingGateway{}, policy, func() string { return "id" })

	approval := approvals.Approval{ID: "approval-1", Action: approvals.AddRepository, Payload: mustMarshal(t, addRepositoryPayload{Name: "eggy", CloneURL: "https://github.com/nigelteosw/eggy.git", BaseBranch: "main", ProtectedBranches: []string{"main"}})}
	if _, err := service.ExecuteApproved(context.Background(), approval); !errors.Is(err, approvals.ErrExpired) {
		t.Fatalf("error=%v", err)
	}
	state, _ := store.Load(context.Background())
	if len(state.Repositories) != 0 {
		t.Fatalf("repository persisted despite failed authorization: %#v", state.Repositories)
	}
}

func TestRepositoriesRemoveAppliesImmediatelyUnlessRunActive(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}, "busy": {Name: "busy"}}
	store.state.CodingRuns = map[string]ports.CodingRun{"run-1": {ID: "run-1", Repository: "busy", Status: "running"}}
	service := NewRepositoriesService(store, &fakeWorkspaceRunner{}, &fakeRemoteChecker{}, &fakeShippingGateway{}, &fakeShippingGateway{}, func() string { return "id" })

	if err := service.Remove(context.Background(), "eggy"); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(context.Background())
	if _, ok := state.Repositories["eggy"]; ok {
		t.Fatal("eggy was not removed")
	}

	if err := service.Remove(context.Background(), "busy"); err == nil {
		t.Fatal("expected removal to be blocked by the active run")
	}
	if err := service.Remove(context.Background(), "missing"); err == nil {
		t.Fatal("expected error removing an unconfigured repository")
	}
}

type fakeRemoteChecker struct {
	called     bool
	repository ports.Repository
	err        error
}

func (c *fakeRemoteChecker) CheckRemote(_ context.Context, repository ports.Repository, _ string) error {
	c.called = true
	c.repository = repository
	return c.err
}
```

Add two tiny test-only helpers used above (`jsonMarshal` avoids importing `encoding/json` into the test just for one line twice; `mustMarshal` builds a raw approval payload) at the bottom of `repositories_test.go`:

```go
func jsonMarshal(value any) ([]byte, error) { return json.Marshal(value) }

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
```

And add `"encoding/json"` to the test file's imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/kernel/services/... -run TestRepositories -v`
Expected: FAIL with `undefined: NewRepositoriesService` (and `undefined: addRepositoryPayload`)

- [ ] **Step 3: Implement `RepositoriesService`**

Create `internal/kernel/services/repositories.go`:

```go
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

var (
	repositoryNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	repositoryBranchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

type RepositoriesService struct {
	store     ports.StateStore
	runner    ports.Runner
	checker   ports.RemoteChecker
	requester ApprovalRequester
	policy    ports.ApprovalPolicy
	newRunID  func() string
}

func NewRepositoriesService(store ports.StateStore, runner ports.Runner, checker ports.RemoteChecker, requester ApprovalRequester, policy ports.ApprovalPolicy, newRunID func() string) *RepositoriesService {
	return &RepositoriesService{store: store, runner: runner, checker: checker, requester: requester, policy: policy, newRunID: newRunID}
}

type addRepositoryPayload struct {
	Name              string
	CloneURL          string
	BaseBranch        string
	ProtectedBranches []string
}

func (s *RepositoriesService) List(ctx context.Context) (map[string]ports.Repository, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return state.Repositories, nil
}

func (s *RepositoriesService) Get(ctx context.Context, name string) (ports.Repository, bool, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return ports.Repository{}, false, err
	}
	repository, ok := state.Repositories[name]
	return repository, ok, nil
}

func (s *RepositoriesService) RequestAdd(ctx context.Context, name, cloneURL, baseBranch string, protectedBranches []string) (approvals.Approval, error) {
	if !repositoryNamePattern.MatchString(name) {
		return approvals.Approval{}, errors.New("repository name is invalid")
	}
	if cloneURL == "" {
		return approvals.Approval{}, errors.New("clone_url is required")
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	if !repositoryBranchPattern.MatchString(baseBranch) {
		return approvals.Approval{}, fmt.Errorf("base branch %q is invalid", baseBranch)
	}
	if len(protectedBranches) == 0 {
		protectedBranches = []string{baseBranch}
	}
	for _, branch := range protectedBranches {
		if !repositoryBranchPattern.MatchString(branch) {
			return approvals.Approval{}, fmt.Errorf("protected branch %q is invalid", branch)
		}
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return approvals.Approval{}, err
	}
	if _, exists := state.Repositories[name]; exists {
		return approvals.Approval{}, fmt.Errorf("repository %q already exists", name)
	}
	repository := ports.Repository{Name: name, CloneURL: cloneURL, BaseBranch: baseBranch, ProtectedBranches: protectedBranches}
	workspace, err := s.runner.Create(ctx, s.newRunID())
	if err != nil {
		return approvals.Approval{}, err
	}
	defer s.runner.Destroy(context.Background(), workspace)
	if err := s.checker.CheckRemote(ctx, repository, workspace); err != nil {
		return approvals.Approval{}, err
	}
	payload := addRepositoryPayload{Name: name, CloneURL: cloneURL, BaseBranch: baseBranch, ProtectedBranches: protectedBranches}
	return s.requester.Request(ctx, approvals.AddRepository, payload, "Add repository "+name)
}

func (s *RepositoriesService) ExecuteApproved(ctx context.Context, approval approvals.Approval) (any, error) {
	if approval.Action != approvals.AddRepository {
		return nil, errors.New("approval is not a repositories action")
	}
	var payload addRepositoryPayload
	if err := json.Unmarshal(approval.Payload, &payload); err != nil {
		return nil, err
	}
	if err := s.policy.Authorize(ctx, approvals.AddRepository, payload, approval.ID); err != nil {
		return nil, err
	}
	repository := ports.Repository{Name: payload.Name, CloneURL: payload.CloneURL, BaseBranch: payload.BaseBranch, ProtectedBranches: payload.ProtectedBranches}
	state, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		if state.Repositories == nil {
			state.Repositories = map[string]ports.Repository{}
		}
		if _, exists := state.Repositories[repository.Name]; exists {
			return fmt.Errorf("repository %q already exists", repository.Name)
		}
		state.Repositories[repository.Name] = repository
		return nil
	})
	return repository, err
}

func (s *RepositoriesService) Remove(ctx context.Context, name string) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if _, ok := state.Repositories[name]; !ok {
		return fmt.Errorf("repository %q is not configured", name)
	}
	for _, run := range state.CodingRuns {
		if run.Repository == name && run.Status == "running" {
			return fmt.Errorf("repository %q has an active coding run %q", name, run.ID)
		}
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		if _, ok := state.Repositories[name]; !ok {
			return fmt.Errorf("repository %q is not configured", name)
		}
		delete(state.Repositories, name)
		return nil
	})
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/kernel/services/... -run TestRepositories -v`
Expected: PASS

- [ ] **Step 5: Run the full services package test suite**

Run: `go test ./internal/kernel/services/... -v`
Expected: all tests PASS, including untouched `shipping_test.go`/`coding_test.go`/`dispatcher_test.go` tests (fakes are reused, not modified, in this task)

- [ ] **Step 6: Commit**

```bash
git add internal/kernel/services/repositories.go internal/kernel/services/repositories_test.go
git commit -m "feat: add RepositoriesService for approval-gated repository add/remove"
```

---

### Task 4: `ApprovalService` reads protected branches from live state

**Files:**
- Modify: `internal/kernel/services/approval_service.go`
- Modify: `internal/kernel/approvals/service_test.go`

**Interfaces:**
- Consumes: `ports.State.Repositories` (Task 1).
- Produces: `func NewApprovalService(store ports.StateStore, now func() time.Time, ttl time.Duration) *ApprovalService` (drops the `protectedBranches []string` parameter) — Task 7 depends on this new signature.

- [ ] **Step 1: Update the failing tests first**

In `internal/kernel/approvals/service_test.go`, change both call sites and seed `Repositories` on the fake store instead of passing a static list.

Replace:
```go
	service := services.NewApprovalService(store, func() time.Time { return now }, 10*time.Minute, []string{"main"})
```
with:
```go
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", ProtectedBranches: []string{"main"}}}
	service := services.NewApprovalService(store, func() time.Time { return now }, 10*time.Minute)
```

Replace:
```go
	service := services.NewApprovalService(store, time.Now, time.Hour, []string{"main", "production"})
```
with:
```go
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", ProtectedBranches: []string{"main", "production"}}}
	service := services.NewApprovalService(store, time.Now, time.Hour)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/kernel/approvals/... -v`
Expected: FAIL — compile error, `NewApprovalService` called with 4 arguments but 3 expected (won't compile until Step 3)

- [ ] **Step 3: Update `ApprovalService`**

In `internal/kernel/services/approval_service.go`, change the struct, constructor, and `Authorize`:

```go
type ApprovalService struct {
	store ports.StateStore
	now   func() time.Time
	ttl   time.Duration
}

func NewApprovalService(store ports.StateStore, now func() time.Time, ttl time.Duration) *ApprovalService {
	return &ApprovalService{store: store, now: now, ttl: ttl}
}
```

Remove the old `protected map[string]bool` field and its constructor-time construction entirely. Change `Authorize`:

```go
func (s *ApprovalService) Authorize(ctx context.Context, action approvals.Action, payload any, approvalID string) error {
	if action == approvals.Push {
		state, err := s.store.Load(ctx)
		if err != nil {
			return err
		}
		if branch := payloadBranch(payload); branch != "" && isProtectedBranch(state.Repositories, branch) {
			return approvals.ErrProtectedBranch
		}
	}
	_, digest, err := canonicalPayload(payload)
	if err != nil {
		return err
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		approval, ok := state.Approvals[approvalID]
		if !ok || approval.Action != action || approval.Status != approvals.Approved {
			return approvals.ErrNotAuthorized
		}
		if !s.now().Before(approval.ExpiresAt) {
			approval.Status = approvals.Expired
			state.Approvals[approvalID] = approval
			return approvals.ErrExpired
		}
		if approval.PayloadDigest != digest {
			return approvals.ErrPayloadChanged
		}
		approval.Status = approvals.Used
		state.Approvals[approvalID] = approval
		return nil
	})
	return err
}

func isProtectedBranch(repositories map[string]ports.Repository, branch string) bool {
	for _, repository := range repositories {
		for _, protected := range repository.ProtectedBranches {
			if protected == branch {
				return true
			}
		}
	}
	return false
}
```

(`Request` and `Decide` are unchanged; only the struct fields, constructor, and the top of `Authorize` change. Note `Authorize` now loads state twice — once for the protected-branch check, once inside the existing `Update` closure — which is acceptable since both reads are cheap local file reads under the same call and this mirrors the existing pattern of loading state before mutating it elsewhere in the codebase.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/kernel/approvals/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kernel/services/approval_service.go internal/kernel/approvals/service_test.go
git commit -m "fix: compute protected-branch denial from live repository state instead of a boot-time snapshot"
```

---

### Task 5: `ShippingService` and repository tools read live state

**Files:**
- Modify: `internal/kernel/services/shipping.go`
- Modify: `internal/kernel/services/shipping_test.go`
- Modify: `internal/kernel/services/repository_tools.go`
- Modify: `internal/kernel/services/repository_tools_test.go`

**Interfaces:**
- Consumes: `ports.State.Repositories` (Task 1).
- Produces: `func NewShippingService(store ports.StateStore, policy ports.ApprovalPolicy, provider ports.RepositoryProvider) *ShippingService` (drops the `repositories map[string]ports.Repository` parameter); `func NewRepositoryTools(store ports.StateStore, inspector RepositoryInspector, modifier RepositoryModifier, approvalRequester CommitApprovalRequester, newRunID func() string, progress func(ports.CodingProgress), deliverApproval func(context.Context, approvals.Approval) error) []ports.Tool` (drops the `repositories map[string]ports.Repository` parameter, adds `store ports.StateStore` as the first parameter). Task 7 depends on both new signatures.

- [ ] **Step 1: Update `shipping.go`'s `run` helper**

In `internal/kernel/services/shipping.go`, remove the `repositories map[string]ports.Repository` field and constructor parameter:

```go
type ShippingService struct {
	store     ports.StateStore
	policy    ports.ApprovalPolicy
	provider  ports.RepositoryProvider
	requester ApprovalRequester
}

func NewShippingService(store ports.StateStore, policy ports.ApprovalPolicy, provider ports.RepositoryProvider) *ShippingService {
	return &ShippingService{store: store, policy: policy, provider: provider}
}
```

Change the `run` helper to look the repository up in the freshly-loaded state instead of the removed field:

```go
func (s *ShippingService) run(ctx context.Context, id string) (ports.CodingRun, ports.Repository, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return ports.CodingRun{}, ports.Repository{}, err
	}
	run, ok := state.CodingRuns[id]
	if !ok {
		return ports.CodingRun{}, ports.Repository{}, errors.New("coding run not found")
	}
	repository, ok := state.Repositories[run.Repository]
	if !ok {
		return ports.CodingRun{}, ports.Repository{}, errors.New("repository is not registered")
	}
	return run, repository, nil
}
```

Everything else in `shipping.go` (`RequestCommit`, `RequestPush`, `RequestPullRequest`, `ExecuteApproved`, `Commit`, `Push`, `CreatePullRequest`) is unchanged — they already call `s.run(ctx, ...)` and don't touch the old field directly.

- [ ] **Step 2: Update `shipping_test.go` call sites**

In `internal/kernel/services/shipping_test.go`, every `NewShippingService(store, policy, repository, map[string]ports.Repository{...})` call drops the trailing map argument, and the store is seeded with `Repositories` directly instead. For example, change:

```go
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{"run-1": {ID: "run-1", Repository: "eggy", Workspace: "/tmp/run", Branch: "feature", Diff: "diff"}}
	policy := &fakePolicy{}
	repository := &fakeRepository{}
	service := NewShippingService(store, policy, repository, map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main"}}})
```
to:
```go
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{"run-1": {ID: "run-1", Repository: "eggy", Workspace: "/tmp/run", Branch: "feature", Diff: "diff"}}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main"}}}
	policy := &fakePolicy{}
	repository := &fakeRepository{}
	service := NewShippingService(store, policy, repository)
```

Apply the same mechanical change (drop the trailing map argument from `NewShippingService(...)`, add the equivalent `store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}` line right after `store := newMemoryStore()`) to the other four `NewShippingService(...)` call sites in this file: `TestShippingStopsBeforeSideEffectWhenApprovalFails`, `TestShippingInvalidatesCommitApprovalWhenWorkspaceDiffChanged`, `TestShippingRejectsMovedLocalOrRemoteCommit`, `TestShippingPersistsAndResumesApprovedAction`.

- [ ] **Step 3: Run shipping tests to verify they fail, then pass**

Run: `go test ./internal/kernel/services/... -run TestShipping -v`
Expected: first FAIL (signature mismatch) before Step 1/2 land together, then PASS once both are applied.

- [ ] **Step 4: Update `repository_tools.go` to read live state**

In `internal/kernel/services/repository_tools.go`, change the constructor signature and remove the once-built `registered` map, replacing every use with a fresh `store.Load(ctx)` inside each tool's `execute`:

```go
func NewRepositoryTools(
	store ports.StateStore,
	inspector RepositoryInspector,
	modifier RepositoryModifier,
	approvalRequester CommitApprovalRequester,
	newRunID func() string,
	progress func(ports.CodingProgress),
	deliverApproval func(context.Context, approvals.Approval) error,
) []ports.Tool {
	list := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_list", Description: "List repositories actually configured at runtime; never infer repository configuration from memory", Schema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}}
	list.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		if err := decodeStrict(raw, &struct{}{}); err != nil {
			return nil, err
		}
		registered, err := loadRepositories(ctx, store)
		if err != nil {
			return nil, err
		}
		if len(registered) == 0 {
			return json.Marshal(map[string]any{"status": "not_configured", "repositories": []any{}, "message": "No repositories are configured. Configure repositories in Eggy's persisted configuration; do not send credentials in chat."})
		}
		type safeRepository struct {
			Name              string   `json:"name"`
			BaseBranch        string   `json:"base_branch"`
			ProtectedBranches []string `json:"protected_branches"`
		}
		names := make([]string, 0, len(registered))
		for name := range registered {
			names = append(names, name)
		}
		sort.Strings(names)
		result := make([]safeRepository, 0, len(names))
		for _, name := range names {
			repository := registered[name]
			result = append(result, safeRepository{Name: repository.Name, BaseBranch: repository.BaseBranch, ProtectedBranches: append([]string(nil), repository.ProtectedBranches...)})
		}
		return json.Marshal(map[string]any{"status": "configured", "repositories": result})
	}

	inspect := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_inspect", Description: "Answer a read-only question using Codex in an isolated checkout; creates no branch or approval and must be used before claiming repository implementation facts", Schema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"question":{"type":"string","minLength":1}},"required":["repository","question"],"additionalProperties":false}`),
	}}
	inspect.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
			Question   string `json:"question"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		registered, err := loadRepositories(ctx, store)
		if err != nil {
			return nil, err
		}
		repository, ok := registered[input.Repository]
		if !ok {
			return nil, fmt.Errorf("repository %q is not configured", input.Repository)
		}
		if inspector == nil || newRunID == nil {
			return nil, errors.New("repository inspection is unavailable")
		}
		result, err := inspector.Inspect(ctx, "inspect-"+newRunID(), repository, input.Question)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"repository": repository.Name, "summary": result.Summary, "validation": result.Validation})
	}

	modify := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_modify", Description: "Use only for an explicit owner request to change a configured repository; runs Codex and requests commit approval without committing, pushing, or creating a pull request automatically", Schema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"instruction":{"type":"string","minLength":1}},"required":["repository","instruction"],"additionalProperties":false}`),
	}}
	modify.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository  string `json:"repository"`
			Instruction string `json:"instruction"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		registered, err := loadRepositories(ctx, store)
		if err != nil {
			return nil, err
		}
		repository, ok := registered[input.Repository]
		if !ok {
			return nil, fmt.Errorf("repository %q is not configured", input.Repository)
		}
		if modifier == nil || approvalRequester == nil || newRunID == nil {
			return nil, errors.New("repository modification is unavailable")
		}
		runID := newRunID()
		run, result, err := modifier.Start(ctx, runID, repository, input.Instruction, progress)
		if err != nil {
			return nil, err
		}
		approval, err := approvalRequester.RequestCommit(ctx, run.ID, result.CommitMessage)
		if err != nil {
			return nil, err
		}
		if deliverApproval != nil {
			if err := deliverApproval(ctx, approval); err != nil {
				return nil, err
			}
		}
		return json.Marshal(map[string]string{"status": "awaiting_owner", "run_id": run.ID, "approval_id": approval.ID, "summary": result.Summary, "validation": result.Validation})
	}
	return []ports.Tool{list, inspect, modify}
}

func loadRepositories(ctx context.Context, store ports.StateStore) (map[string]ports.Repository, error) {
	if store == nil {
		return nil, nil
	}
	state, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return state.Repositories, nil
}
```

(The `registered := make(map[string]ports.Repository, len(repositories)); for name, repository := range repositories { registered[name] = repository }` copy loop from the old constructor is deleted entirely — `loadRepositories` returns the live map straight from state on every call instead.)

- [ ] **Step 5: Update `repository_tools_test.go` call sites**

In `internal/kernel/services/repository_tools_test.go`, replace the static `repositories := map[string]ports.Repository{...}` + passing it as the first constructor argument with a `*memoryStore` seeded with `Repositories`. For example, change:

```go
	repositories := map[string]ports.Repository{
		"zeta": {Name: "zeta", BaseBranch: "trunk", ProtectedBranches: []string{"trunk"}},
		"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main"}},
	}
	worker := &fakeRepositoryWorker{}
	requester := &fakeCommitRequester{approval: approvals.Approval{ID: "approval-1", Action: approvals.Commit, Status: approvals.Pending}}
	var delivered approvals.Approval
	tools := NewRepositoryTools(repositories, worker, worker, requester, func() string { return "run-1" }, nil, func(_ context.Context, approval approvals.Approval) error {
		delivered = approval
		return nil
	})
```
to:
```go
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{
		"zeta": {Name: "zeta", BaseBranch: "trunk", ProtectedBranches: []string{"trunk"}},
		"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main"}},
	}
	worker := &fakeRepositoryWorker{}
	requester := &fakeCommitRequester{approval: approvals.Approval{ID: "approval-1", Action: approvals.Commit, Status: approvals.Pending}}
	var delivered approvals.Approval
	tools := NewRepositoryTools(store, worker, worker, requester, func() string { return "run-1" }, nil, func(_ context.Context, approval approvals.Approval) error {
		delivered = approval
		return nil
	})
```

Apply the same pattern to `TestRepositoryModifyStampsRunIDOnProgressEvents` (seed `store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}`, pass `store` as the first argument).

For `TestRepositoryListReportsNotConfigured`, change `NewRepositoryTools(nil, &fakeRepositoryWorker{}, ...)` to `NewRepositoryTools(newMemoryStore(), &fakeRepositoryWorker{}, ...)` — an empty freshly-seeded store has a nil `Repositories` map, which still exercises the "not configured" path.

- [ ] **Step 6: Run tests to verify they fail, then pass**

Run: `go test ./internal/kernel/services/... -run TestRepositoryTools\|TestRepositoryModify\|TestRepositoryListReportsNotConfigured -v`
Expected: FAIL (signature mismatch) before the edits are complete, then PASS once Step 4/5 both land.

- [ ] **Step 7: Run the full services package suite**

Run: `go test ./internal/kernel/services/... -v`
Expected: all PASS

- [ ] **Step 8: Commit**

```bash
git add internal/kernel/services/shipping.go internal/kernel/services/shipping_test.go internal/kernel/services/repository_tools.go internal/kernel/services/repository_tools_test.go
git commit -m "refactor: read repositories from live state instead of a boot-time snapshot in shipping and repository tools"
```

---

### Task 6: `/repositories add` and `/repositories remove` commands

**Files:**
- Modify: `internal/bootstrap/commands.go`
- Modify: `internal/bootstrap/commands_test.go`
- Modify: `internal/adapters/channels/telegram/commands.go`

**Interfaces:**
- Consumes: `*services.RepositoriesService` (Task 3) — `List`, `RequestAdd`, `Remove`.
- Produces: `CommandService` gains `repositories *services.RepositoriesService`, `channel ports.Channel`, `owner string` fields — Task 7 wires these in `app.go`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/bootstrap/commands_test.go`:

```go
func TestCommandRepositoriesListsAddsAndRemoves(t *testing.T) {
	store := jsonfile.Open(t.TempDir() + "/state.json")
	runner := &commandTestRunner{workspace: "/tmp/runs/check-1"}
	checker := &commandTestChecker{}
	gateway := &commandTestApprovalGateway{approval: approvals.Approval{ID: "approval-1", Action: approvals.AddRepository}}
	repositories := services.NewRepositoriesService(store, runner, checker, gateway, gateway, func() string { return "check-1" })
	var delivered approvals.Approval
	channel := &commandTestChannel{onApproval: func(approval approvals.Approval) { delivered = approval }}
	commands := &CommandService{store: store, repositories: repositories, channel: channel, owner: "42"}
	ctx := context.Background()

	output, handled, err := commands.Execute(ctx, "/repositories")
	if err != nil || !handled || output != "No repositories configured." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories add eggy https://github.com/nigelteosw/eggy.git")
	if err != nil || !handled || !strings.Contains(output, "awaiting approval") || delivered.ID != "approval-1" {
		t.Fatalf("output=%q handled=%v err=%v delivered=%#v", output, handled, err, delivered)
	}

	approval := delivered
	approval.Status = approvals.Approved
	if _, err := repositories.ExecuteApproved(ctx, approval); err != nil {
		t.Fatal(err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories")
	if err != nil || !handled || output != "eggy" {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories remove eggy")
	if err != nil || !handled || output != "Removed eggy." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories remove eggy")
	if err != nil || !handled || !strings.Contains(output, "not configured") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/repositories add")
	if err != nil || !handled || !strings.Contains(output, "Usage:") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}

type commandTestRunner struct{ workspace string }

func (r *commandTestRunner) Create(context.Context, string) (string, error) { return r.workspace, nil }
func (r *commandTestRunner) Execute(context.Context, ports.Command) (ports.CommandResult, error) {
	return ports.CommandResult{}, nil
}
func (r *commandTestRunner) Destroy(context.Context, string) error { return nil }

type commandTestChecker struct{}

func (commandTestChecker) CheckRemote(context.Context, ports.Repository, string) error { return nil }

type commandTestApprovalGateway struct {
	approval   approvals.Approval
	authorized approvals.Action
}

func (g *commandTestApprovalGateway) Request(_ context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error) {
	data, _ := json.Marshal(payload)
	g.approval.Payload = data
	return g.approval, nil
}
func (g *commandTestApprovalGateway) Authorize(_ context.Context, action approvals.Action, _ any, _ string) error {
	g.authorized = action
	return nil
}

type commandTestChannel struct{ onApproval func(approvals.Approval) }

func (c *commandTestChannel) Deliver(context.Context, string, string) error { return nil }
func (c *commandTestChannel) DeliverApproval(_ context.Context, _ string, approval approvals.Approval) error {
	if c.onApproval != nil {
		c.onApproval(approval)
	}
	return nil
}
func (c *commandTestChannel) DeliverTrackable(context.Context, string, string) (string, error) {
	return "", nil
}
func (c *commandTestChannel) EditText(context.Context, string, string, string) error { return nil }
func (c *commandTestChannel) AnswerCallback(context.Context, string) error           { return nil }
func (c *commandTestChannel) SendTyping(context.Context, string) error               { return nil }
```

Add `"github.com/nigelteosw/eggy/internal/kernel/approvals"` and `"github.com/nigelteosw/eggy/internal/kernel/services"` to `commands_test.go`'s imports if not already present (`services` is already imported; `approvals` needs adding).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestCommandRepositoriesListsAddsAndRemoves -v`
Expected: FAIL — compile error (`repositories`/`channel`/`owner` fields don't exist on `CommandService` yet, `/repositories add`/`remove` unhandled)

- [ ] **Step 3: Implement the command**

In `internal/bootstrap/commands.go`, add fields to `CommandService`:

```go
type CommandService struct {
	config       Config
	store        ports.StateStore
	context      ports.ContextStore
	conversation *services.ConversationService
	coding       *services.CodingService
	repositories *services.RepositoriesService
	agentRuntime *services.AgentRuntime
	channel      ports.Channel
	owner        string
	defaultModel string
	modelAliases []string
	now          func() time.Time
}
```

Replace the existing `case "/repositories":` block with:

```go
	case "/repositories":
		if s.repositories == nil {
			return "Repositories are not configured.", true, nil
		}
		if len(fields) == 1 {
			registered, err := s.repositories.List(ctx)
			if err != nil {
				return "", true, err
			}
			names := make([]string, 0, len(registered))
			for name := range registered {
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return "No repositories configured.", true, nil
			}
			return strings.Join(names, "\n"), true, nil
		}
		switch fields[1] {
		case "add":
			if len(fields) < 4 || len(fields) > 6 {
				return "Usage: /repositories add <name> <clone_url> [base_branch] [protected_branches]", true, nil
			}
			name, cloneURL := fields[2], fields[3]
			baseBranch := ""
			if len(fields) >= 5 {
				baseBranch = fields[4]
			}
			var protectedBranches []string
			if len(fields) == 6 {
				for _, branch := range strings.Split(fields[5], ",") {
					if trimmed := strings.TrimSpace(branch); trimmed != "" {
						protectedBranches = append(protectedBranches, trimmed)
					}
				}
			}
			approval, err := s.repositories.RequestAdd(ctx, name, cloneURL, baseBranch, protectedBranches)
			if err != nil {
				return err.Error(), true, nil
			}
			if s.channel != nil {
				if err := s.channel.DeliverApproval(ctx, s.owner, approval); err != nil {
					return "", true, err
				}
			}
			return "Add request for " + name + " staged, awaiting approval.", true, nil
		case "remove":
			if len(fields) != 3 {
				return "Usage: /repositories remove <name>", true, nil
			}
			if err := s.repositories.Remove(ctx, fields[2]); err != nil {
				return err.Error(), true, nil
			}
			return "Removed " + fields[2] + ".", true, nil
		default:
			return "Usage: /repositories [add <name> <clone_url> [base_branch] [protected_branches]|remove <name>]", true, nil
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -run TestCommandRepositoriesListsAddsAndRemoves -v`
Expected: PASS

- [ ] **Step 5: Update the Telegram command description**

In `internal/adapters/channels/telegram/commands.go`, update the description to mention the subcommands:

```go
		{Name: "repositories", Description: "List, add, or remove configured repositories"},
```

- [ ] **Step 6: Run the full bootstrap package suite**

Run: `go test ./internal/bootstrap/... -v`
Expected: some existing tests will now fail to compile because `CommandService` literals elsewhere in this package don't set the new fields — that's expected and fixed structurally in Task 7, not here. Confirm the only failures are compile errors in `app_test.go`/`app.go` (not `commands_test.go` itself), then proceed to Task 7 immediately.

- [ ] **Step 7: Commit**

```bash
git add internal/bootstrap/commands.go internal/bootstrap/commands_test.go internal/adapters/channels/telegram/commands.go
git commit -m "feat: add /repositories add and /repositories remove Telegram commands"
```

---

### Task 7: Wire it all into `app.go` — live seeding, executor registry, manifest

**Files:**
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/app_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1-6.
- Produces: a working `NewApp` that seeds `state.Repositories` from `config.Repositories` once on first boot, builds `RepositoriesService`, dispatches approvals through a small executor map instead of a hardcoded switch, and shows live repository names/`codex_ready` to the model on every turn.

- [ ] **Step 1: Remove the static `repositories` field and seed state on first boot**

In `internal/bootstrap/app.go`, remove `repositories map[string]ports.Repository` from the `App` struct and add `repositoriesService *services.RepositoriesService` and `approvalExecutors map[approvals.Action]ApprovalExecutor` instead:

```go
type App struct {
	config              Config
	store               ports.StateStore
	context             ports.ContextStore
	channel             ports.Channel
	dispatcher          *services.Dispatcher
	httpHandler         http.Handler
	loop                *agent.Loop
	agentRuntime        *services.AgentRuntime
	manifest            agent.CapabilityManifest
	commands            *CommandService
	scheduler           *schedulerlocal.Scheduler
	heartbeat           *services.HeartbeatPolicy
	approvals           *services.ApprovalService
	approvalExecutors   map[approvals.Action]ApprovalExecutor
	coding              *services.CodingService
	shipping            *services.ShippingService
	calendar            *services.CalendarService
	repositoriesService *services.RepositoriesService
	conversation        *services.ConversationService
	now                 func() time.Time
	eventQueue          chan events.Event
	workers             sync.WaitGroup
	readyLog            sync.Once
	logger              *slog.Logger
	timezone            string
	location            *time.Location
}

type ApprovalExecutor interface {
	ExecuteApproved(context.Context, approvals.Approval) (any, error)
}
```

Replace the state-store construction and old `app.repositories` seeding block:

```go
	statePath := filepath.Join(config.DataDir, "state.json")
	_, statErr := os.Stat(statePath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat state: %w", statErr)
	}
	stateStore := jsonfile.Open(statePath)
	contextStore := contextmarkdown.Open(config.DataDir, 64<<10)
	app := &App{config: config, store: stateStore, context: contextStore, scheduler: schedulerlocal.New(stateStore), now: options.Now, eventQueue: make(chan events.Event, 64), logger: options.Logger, timezone: timezone, location: location}
	if errors.Is(statErr, os.ErrNotExist) && len(config.Repositories) > 0 {
		seeded := map[string]ports.Repository{}
		for _, configured := range config.Repositories {
			seeded[configured.Name] = ports.Repository{Name: configured.Name, CloneURL: configured.CloneURL, BaseBranch: configured.BaseBranch, ProtectedBranches: configured.ProtectedBranches}
		}
		initial, err := stateStore.Load(context.Background())
		if err != nil {
			return nil, err
		}
		if _, err := stateStore.Update(context.Background(), initial.Version, func(state *ports.State) error {
			state.Repositories = seeded
			return nil
		}); err != nil {
			return nil, fmt.Errorf("seed first-boot repositories: %w", err)
		}
	}
```

This replaces the old block that built `app := &App{..., repositories: map[string]ports.Repository{}, ...}` followed by `for _, configured := range config.Repositories { app.repositories[configured.Name] = ... }`.

- [ ] **Step 2: Drop the static protected-branches computation**

Remove this block entirely (no longer needed — `ApprovalService` now computes it live):

```go
	protected := make([]string, 0)
	for _, repository := range app.repositories {
		protected = append(protected, repository.ProtectedBranches...)
	}
```

Change the `NewApprovalService` call:

```go
	app.approvals = services.NewApprovalService(stateStore, options.Now, 30*time.Minute)
```

- [ ] **Step 3: Update `ShippingService` construction and build `RepositoriesService`**

Change:

```go
	app.shipping = services.NewShippingService(stateStore, app.approvals, repositoryAdapter)
```

Add, right after `app.shipping.SetApprovalRequester(app.approvals)`:

```go
	app.repositoriesService = services.NewRepositoriesService(stateStore, runner, repositoryAdapter, app.approvals, app.approvals, newRunID)
	app.approvalExecutors = map[approvals.Action]ApprovalExecutor{
		approvals.Commit:        app.shipping,
		approvals.Push:          app.shipping,
		approvals.CreatePR:      app.shipping,
		approvals.AddRepository: app.repositoriesService,
	}
```

- [ ] **Step 4: Update `NewRepositoryTools` call**

Change:

```go
	for _, tool := range services.NewRepositoryTools(stateStore, app.coding, app.coding, app.shipping, newRunID,
		progress.Deliver,
		func(ctx context.Context, approval approvals.Approval) error {
			return app.channel.DeliverApproval(ctx, owner, approval)
		},
	) {
```

(only the first argument changes, from `app.repositories` to `stateStore`)

- [ ] **Step 5: Register Calendar's executors in the map when enabled**

Inside the existing `if config.Calendar.Enabled { ... }` block, right after `app.calendar = services.NewCalendarService(...)`, add:

```go
		app.approvalExecutors[approvals.CalendarCreate] = app.calendar
		app.approvalExecutors[approvals.CalendarUpdate] = app.calendar
		app.approvalExecutors[approvals.CalendarDelete] = app.calendar
```

- [ ] **Step 6: Wire `CommandService` with the new fields**

Change:

```go
	app.commands = &CommandService{config: config, store: stateStore, context: contextStore, conversation: app.conversation, coding: app.coding, repositories: app.repositoriesService, agentRuntime: app.agentRuntime, channel: app.channel, owner: owner, defaultModel: config.Agent.DefaultModel, modelAliases: aliases, now: options.Now}
```

- [ ] **Step 7: Compute the capability manifest's repository fields live**

Remove this block (it no longer has `app.repositories` to read from):

```go
	repositoryNames := make([]string, 0, len(app.repositories))
	for name := range app.repositories {
		repositoryNames = append(repositoryNames, name)
	}
```

Change the manifest literal to drop the now-stale fields:

```go
	app.manifest = agent.CapabilityManifest{Tools: toolNames, CalendarEnabled: config.Calendar.Enabled}
```

In `handleMessage`, right after `state, err := a.store.Load(ctx)`, compute the live repository fields onto the per-request manifest copy:

```go
	manifest := a.manifest
	manifest.ActiveModel = alias
	manifest.Repositories = repositoryNamesFromState(state)
	manifest.CodexReady = len(state.Repositories) > 0
```

(replacing the existing `manifest := a.manifest; manifest.ActiveModel = alias` two-liner with these four lines)

Apply the identical change in `handleHeartbeat` (which also already calls `state, err := a.store.Load(ctx)` at its top):

```go
	manifest := a.manifest
	manifest.ActiveModel = alias
	manifest.Repositories = repositoryNamesFromState(state)
	manifest.CodexReady = len(state.Repositories) > 0
```

Add the helper function near `newRunID` at the bottom of `app.go`:

```go
func repositoryNamesFromState(state ports.State) []string {
	names := make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		names = append(names, name)
	}
	return names
}
```

- [ ] **Step 8: Update `Ready()` to log live repository names**

Change:

```go
func (a *App) Ready() error {
	state, err := a.store.Load(context.Background())
	if err != nil {
		return err
	}
	if _, err := a.context.Load(context.Background()); err != nil {
		return err
	}
	a.readyLog.Do(func() {
		alias := a.config.Agent.DefaultModel
		provider := a.config.ModelAliases[alias].Provider
		repositories := repositoryNamesFromState(state)
		sort.Strings(repositories)
		integrations := []string{"telegram", "model_provider"}
		if len(state.Repositories) > 0 {
			integrations = append(integrations, "codex", "github")
		}
		if a.config.Calendar.Enabled {
			integrations = append(integrations, "google_calendar")
		}
		sort.Strings(integrations)
		a.logger.Info("agent runtime ready", "model_alias", alias, "provider", provider, "repositories", repositories, "integrations", integrations, "context_files", []string{"SOUL.md", "USER.md", "MEMORY.md"})
	})
	return nil
}
```

(only the first two lines and the `repositories`/`if len(...)` lines inside `readyLog.Do` change — capture `state` from the existing `Load` call instead of discarding it, and read from `state.Repositories` instead of `a.repositories`)

- [ ] **Step 9: Simplify `handleApproval`'s dispatch**

Replace:

```go
	approval := state.Approvals[decision.ApprovalID]
	var result any
	switch approval.Action {
	case approvals.Commit, approvals.Push, approvals.CreatePR:
		result, err = a.shipping.ExecuteApproved(ctx, approval)
	case approvals.CalendarCreate, approvals.CalendarUpdate, approvals.CalendarDelete:
		if a.calendar == nil {
			return errors.New("Calendar is unavailable")
		}
		result, err = a.calendar.ExecuteApproved(ctx, approval)
	default:
		return errors.New("unknown approval action")
	}
	if err != nil {
		return err
	}
```

with:

```go
	approval := state.Approvals[decision.ApprovalID]
	executor, ok := a.approvalExecutors[approval.Action]
	if !ok {
		return errors.New("unknown approval action")
	}
	result, err := executor.ExecuteApproved(ctx, approval)
	if err != nil {
		return err
	}
```

- [ ] **Step 10: Fix compile errors in `app_test.go`**

Run: `go build ./... 2>&1 | head -50` to see remaining errors first (there should be none touching `app_test.go` itself, since it only constructs `App` through `NewApp`, not struct literals directly — but re-run the full test suite next to be sure).

- [ ] **Step 11: Add an end-to-end test proving the approval executor map actually reaches `RepositoriesService`**

Every other test in this plan exercises `RepositoriesService` or `CommandService` directly — none drives a real `/repositories add` through `app.go`'s actual `handleApproval`/`approvalExecutors` dispatch, which is the part Step 9 changed. Add this to `app_test.go`, following the same local-bare-git-remote pattern already used in `internal/adapters/repositories/github/repository_test.go` (so it needs no network access):

```go
func TestRepositoriesAddApprovalFlowReachesLiveState(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	secrets := appTestSecrets("provider-secret")
	secrets.GitHubToken = "github-secret"
	remote := createLocalGitRemote(t)
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		return appJSON(200, `{"ok":true,"result":{}}`), nil
	})}
	app, err := NewApp(cfg, secrets, AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}, CodexExecutable: "/usr/bin/true"})
	if err != nil {
		t.Fatal(err)
	}

	output, handled, err := app.commands.Execute(context.Background(), "/repositories add eggy "+remote)
	if err != nil || !handled || !strings.Contains(output, "awaiting approval") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	state, err := app.store.Load(context.Background())
	if err != nil || len(state.Approvals) != 1 {
		t.Fatalf("approvals=%#v err=%v", state.Approvals, err)
	}
	var approvalID string
	for id := range state.Approvals {
		approvalID = id
	}

	decisionPayload, _ := json.Marshal(events.ApprovalDecision{ApprovalID: approvalID, Approved: true})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "decide-1", Type: events.TypeApproval, Owner: "42", Payload: decisionPayload}); err != nil {
		t.Fatal(err)
	}

	state, err = app.store.Load(context.Background())
	if err != nil || state.Repositories["eggy"].CloneURL != remote {
		t.Fatalf("repositories=%#v err=%v", state.Repositories, err)
	}
}

func createLocalGitRemote(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "-b", "main", source)
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", ".")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, "", "clone", "--bare", source, remote)
	return remote
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
```

Add `"os"` and `"os/exec"` to `app_test.go`'s import block (`"os"` is likely already absent, `"os/exec"` definitely is).

Run: `go test ./internal/bootstrap/... -run TestRepositoriesAddApprovalFlowReachesLiveState -v`
Expected: PASS — this proves `approvalExecutors[approvals.AddRepository]` is wired to the real `app.repositoriesService`, not just compiling.

- [ ] **Step 12: Run the full test suite**

Run: `go test ./... -v 2>&1 | tail -100`
Expected: all tests PASS, including `TestUnifiedAgentDefectTranscript` (verifies `repository_list` still shows the config-seeded `eggy` repository after the first-boot seeding path), `TestCommandServiceHandlesEveryRegisteredTelegramCommand`, `TestCommandServiceSupportsOperationalShortcuts`, `TestRepositoriesAddApprovalFlowReachesLiveState`, and every test touched in Tasks 1-6.

If `TestUnifiedAgentDefectTranscript` fails, check that the seeding block in Step 1 runs before `runner`/`repositoryAdapter` are constructed but doesn't require them (it only needs `stateStore` and `config.Repositories`), and that `os.Stat(statePath)` is checked before `stateStore.Load` is ever called elsewhere in `NewApp`.

- [ ] **Step 13: Run race detector and vet**

Run: `make fmt vet test race build`
Expected: all pass

- [ ] **Step 14: Commit**

```bash
git add internal/bootstrap/app.go internal/bootstrap/app_test.go
git commit -m "feat: wire live repository allowlist into app composition, seeding, and approval dispatch"
```

---

### Task 8: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Run the complete verification suite**

Run: `make fmt vet test race build`
Expected: all pass with no diffs from `fmt`, no `vet` warnings, all tests green, race detector clean, binary builds.

- [ ] **Step 2: Run the smoke test if Docker is available**

Run: `make smoke`
Expected: passes, or skip with a note if Docker isn't available in this environment.

- [ ] **Step 3: Manually sanity-check the new command surface**

This can't be exercised without a live Telegram bot and GitHub token, so it's out of scope for automated verification here — flag to the user that `/repositories add <name> <clone_url>` end-to-end (including the real Telegram approval button and `git ls-remote` against a real GitHub URL) should be tried against the actual deployment once this ships, the same way the earlier Codex `auth.json` fix was verified live.

- [ ] **Step 4: Update README if the `/repositories` command list needs it**

Check `README.md`'s "Operational shortcuts are `/status`, `/repositories`, ..." line — it already lists `/repositories` without enumerating its arguments, consistent with how `/model <alias>` and `/usage reset` are also folded into their base command names without separate listing. No change needed unless review turns up an actual mismatch.
