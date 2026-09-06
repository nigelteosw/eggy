package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Store must satisfy the neutral port, not just its own shape: web.go and
// the kernel both depend on ports.ThreadStore now.
var _ ports.ThreadStore = (*Store)(nil)

func openThreadStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "eggy.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestAttachWorkspaceRecordsTheCheckoutAndDetachClearsIt(t *testing.T) {
	t.Parallel()
	store := openThreadStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateThread(ctx, "thread-1", "web", now); err != nil {
		t.Fatal(err)
	}

	if err := store.AttachWorkspace(ctx, "thread-1", "web", "eggy", "/data/runs/workspace-1", now); err != nil {
		t.Fatal(err)
	}
	thread, found, err := store.GetThread(ctx, "thread-1")
	if err != nil || !found || thread.Workspace != "/data/runs/workspace-1" || thread.WorkspaceRepository != "eggy" {
		t.Fatalf("thread=%#v found=%v err=%v", thread, found, err)
	}

	if err := store.DetachWorkspace(ctx, "thread-1"); err != nil {
		t.Fatal(err)
	}
	thread, _, err = store.GetThread(ctx, "thread-1")
	if err != nil || thread.Workspace != "" || thread.WorkspaceRepository != "" {
		t.Fatalf("thread=%#v err=%v", thread, err)
	}
}

// Telegram's fixed thread never goes through CreateThread, so attaching
// must create the row rather than silently updating nothing.
func TestAttachWorkspaceUpsertsAThreadThatWasNeverExplicitlyCreated(t *testing.T) {
	t.Parallel()
	store := openThreadStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	if err := store.AttachWorkspace(ctx, "telegram", "telegram", "eggy", "/data/runs/workspace-1", now); err != nil {
		t.Fatal(err)
	}
	thread, found, err := store.GetThread(ctx, "telegram")
	if err != nil || !found || thread.Channel != "telegram" || thread.Workspace != "/data/runs/workspace-1" {
		t.Fatalf("thread=%#v found=%v err=%v", thread, found, err)
	}
}

func TestAttachWorkspaceReplacesAPreviouslyAttachedCheckout(t *testing.T) {
	t.Parallel()
	store := openThreadStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	if err := store.AttachWorkspace(ctx, "thread-1", "web", "eggy", "/data/runs/workspace-1", now); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachWorkspace(ctx, "thread-1", "web", "other", "/data/runs/workspace-2", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	thread, _, err := store.GetThread(ctx, "thread-1")
	if err != nil || thread.Workspace != "/data/runs/workspace-2" || thread.WorkspaceRepository != "other" {
		t.Fatalf("thread=%#v err=%v", thread, err)
	}
}

func TestThreadsWithWorkspaceReturnsOnlyAttachedThreadsStalestFirst(t *testing.T) {
	t.Parallel()
	store := openThreadStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateThread(ctx, "unattached", "web", now); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachWorkspace(ctx, "recent", "web", "eggy", "/data/runs/workspace-2", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachWorkspace(ctx, "stale", "web", "eggy", "/data/runs/workspace-1", now); err != nil {
		t.Fatal(err)
	}

	attached, err := store.ThreadsWithWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(attached) != 2 {
		t.Fatalf("attached=%#v, want only the two threads with a workspace", attached)
	}
	if attached[0].ID != "stale" || attached[1].ID != "recent" {
		t.Fatalf("attached=%v, want stalest first so a reaper walks it first", []string{attached[0].ID, attached[1].ID})
	}
}

// A /data/eggy.db written before workspaces were attachable must open and
// migrate in place rather than needing replacement.
func TestOpenMigratesAThreadsTableWithoutWorkspaceColumns(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "eggy.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE threads (
		    id         TEXT PRIMARY KEY,
		    title      TEXT,
		    channel    TEXT    NOT NULL,
		    created_at INTEGER NOT NULL,
		    updated_at INTEGER NOT NULL
		);
		INSERT INTO threads (id, title, channel, created_at, updated_at)
		VALUES ('thread-1', 'Existing', 'web', 1, 1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path, 100)
	if err != nil {
		t.Fatalf("an existing database must migrate in place: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	thread, found, err := store.GetThread(context.Background(), "thread-1")
	if err != nil || !found || thread.Title != "Existing" {
		t.Fatalf("thread=%#v found=%v err=%v", thread, found, err)
	}
	if thread.Workspace != "" {
		t.Fatalf("a migrated thread must start with no workspace attached, got %q", thread.Workspace)
	}
	if err := store.AttachWorkspace(context.Background(), "thread-1", "web", "eggy", "/data/runs/workspace-1", time.Now()); err != nil {
		t.Fatalf("the migrated columns must be writable: %v", err)
	}
}
