package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

func webThread(id string) context.Context {
	return destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: id})
}

func TestWorkspaceOpenAttachesOneCheckoutPerThreadAndSurvivesCalls(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	runner := &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}
	reader := &fakeRepositoryReader{}
	sessions := NewWorkspaceSessions(store, runner, reader, func() string { return "1" })
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

func TestWorkspaceBindingIsScopedToItsOwnThread(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	sessions := NewWorkspaceSessions(store, &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}, &fakeRepositoryReader{}, func() string { return "1" })
	if _, err := sessions.Open(webThread("thread-a"), "eggy"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Resolve(webThread("thread-b")); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("err=%v, want ErrNoWorkspace for an unrelated thread", err)
	}
}

func TestRunWorkspaceOutranksTheThreadCheckoutAndIsWritable(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	sessions := NewWorkspaceSessions(store, &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}, &fakeRepositoryReader{}, func() string { return "1" })
	ctx := webThread("thread-a")
	if _, err := sessions.Open(ctx, "eggy"); err != nil {
		t.Fatal(err)
	}
	binding, err := sessions.Resolve(withWorkspace(ctx, "/tmp/runs/run-7"))
	if err != nil || binding.Path != "/tmp/runs/run-7" || !binding.Writable {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
}

func TestWorkspaceOpenRejectsAnUnconfiguredRepository(t *testing.T) {
	sessions := NewWorkspaceSessions(newMemoryStore(), &fakeReadWorkspaceRunner{}, &fakeRepositoryReader{}, func() string { return "1" })
	if _, err := sessions.Open(webThread("thread-a"), "missing"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err=%v", err)
	}
}

func TestWorkspaceCloseOnAThreadWithNoWorkspaceIsNotAnError(t *testing.T) {
	sessions := NewWorkspaceSessions(newMemoryStore(), &fakeReadWorkspaceRunner{}, &fakeRepositoryReader{}, func() string { return "1" })
	if err := sessions.Close(webThread("thread-a")); err != nil {
		t.Fatal(err)
	}
}
