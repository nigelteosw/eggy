package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeThreadStore is an in-memory ports.ThreadStore. The workspace binding
// is what these tests care about, so title/list behavior is minimal.
type fakeThreadStore struct {
	mu      sync.Mutex
	threads map[string]ports.Thread
}

func newFakeThreadStore() *fakeThreadStore {
	return &fakeThreadStore{threads: map[string]ports.Thread{}}
}

func (s *fakeThreadStore) CreateThread(_ context.Context, id, channel string, at time.Time) (ports.Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	thread := ports.Thread{ID: id, Channel: channel, CreatedAt: at, UpdatedAt: at}
	s.threads[id] = thread
	return thread, nil
}

func (s *fakeThreadStore) ListThreads(_ context.Context, channel string) ([]ports.Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []ports.Thread
	for _, thread := range s.threads {
		if thread.Channel == channel {
			result = append(result, thread)
		}
	}
	return result, nil
}

func (s *fakeThreadStore) GetThread(_ context.Context, id string) (ports.Thread, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	thread, found := s.threads[id]
	return thread, found, nil
}

func (s *fakeThreadStore) SetThreadTitle(_ context.Context, id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	thread := s.threads[id]
	if thread.Title == "" {
		thread.Title = title
		s.threads[id] = thread
	}
	return nil
}

func (s *fakeThreadStore) AttachWorkspace(_ context.Context, id, channel, repository, workspace string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	thread, found := s.threads[id]
	if !found {
		thread = ports.Thread{ID: id, Channel: channel, CreatedAt: at}
	}
	thread.Workspace, thread.WorkspaceRepository, thread.UpdatedAt = workspace, repository, at
	thread.WorkspaceBranch, thread.WorkspaceSession = "", ""
	s.threads[id] = thread
	return nil
}

func (s *fakeThreadStore) DetachWorkspace(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	thread, found := s.threads[id]
	if !found {
		return nil
	}
	thread.Workspace, thread.WorkspaceRepository, thread.WorkspaceBranch, thread.WorkspaceSession = "", "", "", ""
	s.threads[id] = thread
	return nil
}

func (s *fakeThreadStore) SetWorkspaceEdit(_ context.Context, id, branch, session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	thread, found := s.threads[id]
	if !found {
		return nil
	}
	thread.WorkspaceBranch, thread.WorkspaceSession = branch, session
	s.threads[id] = thread
	return nil
}

func (s *fakeThreadStore) ThreadsWithWorkspace(context.Context) ([]ports.Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []ports.Thread
	for _, thread := range s.threads {
		if thread.Workspace != "" {
			result = append(result, thread)
		}
	}
	return result, nil
}

// probingRunner is a Runner that also implements ports.WorkspaceProbe, so
// boot reconciliation can ask whether a recorded checkout still exists.
type probingRunner struct {
	workspace string
	created   bool
	destroyed []string
	existing  map[string]bool
}

func (r *probingRunner) Create(context.Context, string) (string, error) {
	r.created = true
	return r.workspace, nil
}
func (r *probingRunner) Execute(context.Context, ports.Command) (ports.CommandResult, error) {
	return ports.CommandResult{}, nil
}
func (r *probingRunner) Destroy(_ context.Context, workspace string) error {
	r.destroyed = append(r.destroyed, workspace)
	return nil
}
func (r *probingRunner) Exists(_ context.Context, workspace string) (bool, error) {
	return r.existing[workspace], nil
}

func webThread(id string) context.Context {
	return destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: id})
}

func newTestWorkspaceSessions(t *testing.T, runner ports.Runner, threads *fakeThreadStore) *WorkspaceSessions {
	t.Helper()
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	return NewWorkspaceSessions(store, threads, runner, &fakeRepositoryReader{}, func() string { return "1" }, nil, nil)
}

func TestWorkspaceOpenAttachesOneCheckoutPerThreadAndSurvivesCalls(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	runner := &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}
	reader := &fakeRepositoryReader{}
	threads := newFakeThreadStore()
	sessions := NewWorkspaceSessions(store, threads, runner, reader, func() string { return "1" }, nil, nil)
	byName := primitivesByName(sessions.Tools())
	ctx := webThread("thread-a")

	if _, err := byName["workspace_open"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		binding, err := sessions.Resolve(ctx)
		if err != nil || binding.Path != "/tmp/runs/workspace-1" || binding.Repository != "eggy" {
			t.Fatalf("binding=%#v err=%v", binding, err)
		}
		if binding.Writable {
			t.Fatal("an inspection checkout must resolve read-only")
		}
	}
	if reader.cloned != 1 {
		t.Fatalf("expected exactly one clone for the attached checkout, got %d", reader.cloned)
	}
	if runner.destroyed {
		t.Fatal("the checkout must survive until workspace_close")
	}

	if _, err := byName["workspace_close"].Execute(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if !runner.destroyed {
		t.Fatal("workspace_close must destroy the checkout")
	}
	if _, err := sessions.Resolve(ctx); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("err=%v, want ErrNoWorkspace", err)
	}
}

// The binding lives in the store, so a fresh WorkspaceSessions over the
// same store resolves it: this is the restart case.
func TestAnAttachedWorkspaceSurvivesAProcessRestart(t *testing.T) {
	threads := newFakeThreadStore()
	runner := &probingRunner{workspace: "/tmp/runs/workspace-1", existing: map[string]bool{"/tmp/runs/workspace-1": true}}
	ctx := webThread("thread-a")
	if _, err := newTestWorkspaceSessions(t, runner, threads).Open(ctx, "eggy"); err != nil {
		t.Fatal(err)
	}

	restarted := newTestWorkspaceSessions(t, runner, threads)
	dropped, err := restarted.Recover(context.Background())
	if err != nil || dropped != 0 {
		t.Fatalf("dropped=%d err=%v, want the surviving checkout kept", dropped, err)
	}
	binding, err := restarted.Resolve(ctx)
	if err != nil || binding.Path != "/tmp/runs/workspace-1" || binding.Repository != "eggy" {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	if len(runner.destroyed) != 0 {
		t.Fatalf("a restart must not destroy a live checkout, destroyed=%v", runner.destroyed)
	}
}

func TestRecoverDropsABindingWhoseDirectoryIsGone(t *testing.T) {
	threads := newFakeThreadStore()
	// existing is empty: the volume was wiped under the record.
	runner := &probingRunner{workspace: "/tmp/runs/workspace-1", existing: map[string]bool{}}
	ctx := webThread("thread-a")
	if _, err := newTestWorkspaceSessions(t, runner, threads).Open(ctx, "eggy"); err != nil {
		t.Fatal(err)
	}

	restarted := newTestWorkspaceSessions(t, runner, threads)
	dropped, err := restarted.Recover(context.Background())
	if err != nil || dropped != 1 {
		t.Fatalf("dropped=%d err=%v, want the stale binding dropped", dropped, err)
	}
	if _, err := restarted.Resolve(ctx); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("err=%v: a binding whose directory is gone must not resolve", err)
	}
}

// Without a probe there is no way to verify a recorded checkout, so it
// cannot be trusted after a restart.
func TestRecoverDropsEveryBindingWhenTheRunnerCannotProbe(t *testing.T) {
	threads := newFakeThreadStore()
	ctx := webThread("thread-a")
	sessions := newTestWorkspaceSessions(t, &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}, threads)
	if _, err := sessions.Open(ctx, "eggy"); err != nil {
		t.Fatal(err)
	}
	dropped, err := sessions.Recover(context.Background())
	if err != nil || dropped != 1 {
		t.Fatalf("dropped=%d err=%v", dropped, err)
	}
	if _, err := sessions.Resolve(ctx); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("err=%v", err)
	}
}

func TestCleanupIdleReapsOnlyThreadsIdlePastTheCutoff(t *testing.T) {
	threads := newFakeThreadStore()
	runner := &probingRunner{workspace: "/tmp/runs/workspace-1", existing: map[string]bool{}}
	base := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	stale := NewWorkspaceSessions(repositoryState(), threads, runner, &fakeRepositoryReader{}, func() string { return "1" }, func() time.Time { return base }, nil)
	if _, err := stale.Open(webThread("idle-thread"), "eggy"); err != nil {
		t.Fatal(err)
	}
	runner.workspace = "/tmp/runs/workspace-2"
	fresh := NewWorkspaceSessions(repositoryState(), threads, runner, &fakeRepositoryReader{}, func() string { return "2" }, func() time.Time { return base.Add(2 * time.Hour) }, nil)
	if _, err := fresh.Open(webThread("active-thread"), "eggy"); err != nil {
		t.Fatal(err)
	}

	reaped, err := fresh.CleanupIdle(context.Background(), base.Add(time.Hour))
	if err != nil || reaped != 1 {
		t.Fatalf("reaped=%d err=%v, want only the idle thread reaped", reaped, err)
	}
	if len(runner.destroyed) != 1 || runner.destroyed[0] != "/tmp/runs/workspace-1" {
		t.Fatalf("destroyed=%v, want only the idle thread's checkout", runner.destroyed)
	}
	if _, err := fresh.Resolve(webThread("idle-thread")); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("err=%v, want the reaped thread to have no workspace", err)
	}
	binding, err := fresh.Resolve(webThread("active-thread"))
	if err != nil || binding.Path != "/tmp/runs/workspace-2" {
		t.Fatalf("binding=%#v err=%v, want the active thread untouched", binding, err)
	}
}

func repositoryState() ports.StateStore {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	return store
}

func TestWorkspaceBindingIsScopedToItsOwnThread(t *testing.T) {
	threads := newFakeThreadStore()
	sessions := newTestWorkspaceSessions(t, &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}, threads)
	if _, err := sessions.Open(webThread("thread-a"), "eggy"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Resolve(webThread("thread-b")); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("err=%v, want ErrNoWorkspace for an unrelated thread", err)
	}
}

// A run branches the thread's own checkout instead of cloning its own, so
// the same path keeps resolving -- writable only once the branch exists.
func TestMarkEditingMakesTheThreadCheckoutWritableInPlace(t *testing.T) {
	threads := newFakeThreadStore()
	runner := &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}
	sessions := newTestWorkspaceSessions(t, runner, threads)
	ctx := webThread("thread-a")
	if _, err := sessions.Open(ctx, "eggy"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.MarkEditing(ctx, "eggy/run-7", "run-7"); err != nil {
		t.Fatal(err)
	}
	binding, err := sessions.Resolve(ctx)
	if err != nil || binding.Path != "/tmp/runs/workspace-1" || !binding.Writable {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
}

// Adopt is the no-re-clone path: a run over the repository the thread
// already has open works in that very checkout.
func TestAdoptReusesTheThreadsOpenCheckoutForTheSameRepository(t *testing.T) {
	threads := newFakeThreadStore()
	reader := &fakeRepositoryReader{}
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	sessions := NewWorkspaceSessions(store, threads, &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}, reader, func() string { return "1" }, nil, nil)
	ctx := webThread("thread-a")
	if _, err := sessions.Open(ctx, "eggy"); err != nil {
		t.Fatal(err)
	}
	binding, err := sessions.Adopt(ctx, "eggy")
	if err != nil || binding.Path != "/tmp/runs/workspace-1" {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	if reader.cloned != 1 {
		t.Fatalf("cloned=%d, want the open checkout adopted without a second clone", reader.cloned)
	}
}

// A checkout already carrying a previous run's branch is not adopted: its
// uncommitted work would land in the new run's diff.
func TestAdoptOpensAFreshCheckoutWhenTheThreadsIsAlreadyBranched(t *testing.T) {
	threads := newFakeThreadStore()
	reader := &fakeRepositoryReader{}
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	sessions := NewWorkspaceSessions(store, threads, &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}, reader, func() string { return "1" }, nil, nil)
	ctx := webThread("thread-a")
	if _, err := sessions.Open(ctx, "eggy"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.MarkEditing(ctx, "eggy/run-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Adopt(ctx, "eggy"); err != nil {
		t.Fatal(err)
	}
	if reader.cloned != 2 {
		t.Fatalf("cloned=%d, want a fresh clone rather than a branched checkout", reader.cloned)
	}
}

// Telegram never calls CreateThread -- it has no sidebar entry -- so
// attaching must upsert rather than assume a row exists.
func TestTelegramsFixedThreadCanAttachAWorkspace(t *testing.T) {
	threads := newFakeThreadStore()
	sessions := newTestWorkspaceSessions(t, &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}, threads)
	if _, err := sessions.Open(context.Background(), "eggy"); err != nil {
		t.Fatal(err)
	}
	binding, err := sessions.Resolve(context.Background())
	if err != nil || binding.Path != "/tmp/runs/workspace-1" {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
}

func TestWorkspaceOpenRejectsAnUnconfiguredRepository(t *testing.T) {
	sessions := NewWorkspaceSessions(newMemoryStore(), newFakeThreadStore(), &fakeReadWorkspaceRunner{}, &fakeRepositoryReader{}, func() string { return "1" }, nil, nil)
	if _, err := sessions.Open(webThread("thread-a"), "missing"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err=%v", err)
	}
}

func TestWorkspaceCloseOnAThreadWithNoWorkspaceIsNotAnError(t *testing.T) {
	sessions := newTestWorkspaceSessions(t, &fakeReadWorkspaceRunner{}, newFakeThreadStore())
	if err := sessions.Close(webThread("thread-a")); err != nil {
		t.Fatal(err)
	}
}
