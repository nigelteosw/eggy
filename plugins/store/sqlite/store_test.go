package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestOpenCreatesFTS5Schema(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "eggy.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.db.ExecContext(context.Background(), `
		INSERT INTO messages (role, content, source, created_at)
		VALUES (?, ?, ?, ?)
	`, ports.RoleUser, "durable memory", "telegram", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'durable'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("FTS match count = %d, want 1", count)
	}
}

func TestStoreWriteMessageAndSearchTextRoundTrips(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	createdAt := time.Date(2026, time.July, 23, 12, 30, 0, 123, time.UTC)
	stored := ports.StoredMessage{
		Role:      ports.RoleUser,
		Content:   "durable memory phrase",
		Source:    "telegram",
		CreatedAt: createdAt,
	}
	if err := store.WriteMessage(context.Background(), stored); err != nil {
		t.Fatal(err)
	}

	results, err := store.SearchText(context.Background(), "durable", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("result count = %d, want 1", len(results))
	}
	got := results[0]
	if got.ID == 0 {
		t.Fatal("stored message ID = 0, want database ID")
	}
	if got.Role != stored.Role || got.Content != stored.Content || got.Source != stored.Source || !got.CreatedAt.Equal(stored.CreatedAt) {
		t.Fatalf("stored message = %#v, want role/content/source/created_at from %#v", got, stored)
	}
}

func TestStoreMessagesAndFTSSearchPersistAcrossCloseAndReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "eggy.db")
	store, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMessage(context.Background(), ports.StoredMessage{
		Role: ports.RoleAssistant, Content: "persistent searchable phrase", Source: "web", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	results, err := reopened.SearchText(context.Background(), "searchable", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Content != "persistent searchable phrase" || results[0].Role != ports.RoleAssistant || results[0].Source != "web" {
		t.Fatalf("reopened results = %#v", results)
	}
}

func TestSearchTextRejectsEmptyQueryAndNonPositiveLimit(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	for _, query := range []string{"", " \t "} {
		if _, err := store.SearchText(context.Background(), query, 1); err == nil {
			t.Fatalf("SearchText(%q) error = nil, want validation error", query)
		}
	}
	if _, err := store.SearchText(context.Background(), "durable", 0); err == nil {
		t.Fatal("SearchText zero limit error = nil, want validation error")
	}
}

func TestSearchTextReturnsNoMatchesForPunctuationOnlyQuery(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	results, err := store.SearchText(context.Background(), `":-++'"`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("punctuation-only results = %#v, want empty", results)
	}
}

func TestSearchTextTreatsPunctuationAsLiteralTokenSeparators(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	if err := store.WriteMessage(context.Background(), ports.StoredMessage{
		Role: ports.RoleUser, Content: "title quoted well known owner's C++ café", Source: "web", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`"quoted"`, "title:quoted", "well-known", "owner's", "C++", "cafe\u0301"} {
		results, err := store.SearchText(context.Background(), query, 5)
		if err != nil {
			t.Fatalf("SearchText(%q) error = %v", query, err)
		}
		if len(results) != 1 {
			t.Fatalf("SearchText(%q) results = %#v, want one literal-token match", query, results)
		}
	}
}

func TestSearchTextRanksStrongestFTSMatchFirst(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	ctx := context.Background()
	for index, content := range []string{
		"durable durable durable memory",
		"durable memory",
		"durable note",
	} {
		if err := store.WriteMessage(ctx, ports.StoredMessage{
			Role:      ports.RoleUser,
			Content:   content,
			Source:    "telegram",
			CreatedAt: time.Date(2026, time.July, 23, 12, 0, index, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := store.SearchText(ctx, "durable", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("result count = %d, want 3", len(results))
	}
	if got, want := results[0].Content, "durable durable durable memory"; got != want {
		t.Fatalf("strongest FTS match = %q, want %q", got, want)
	}
}

func TestOpenTightensDatabaseAndSidecarPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not supported on Windows")
	}
	t.Parallel()

	path := filepath.Join(t.TempDir(), "eggy.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			if err := os.Chmod(sidecar, 0o644); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if err := store.WriteMessage(context.Background(), ports.StoredMessage{
		Role: ports.RoleUser, Content: "private files", Source: "web", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err != nil {
			if candidate != path && errors.Is(err, os.ErrNotExist) {
				continue
			}
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s permissions = %#o, want 0600", filepath.Base(candidate), got)
		}
	}
}

func TestCreateThreadIsUntitledAndListThreadsOrdersByMostRecentlyUpdated(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateThread(context.Background(), "thread-1", "web", base); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateThread(context.Background(), "thread-2", "web", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	threads, err := store.ListThreads(context.Background(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 || threads[0].ID != "thread-2" || threads[1].ID != "thread-1" {
		t.Fatalf("threads=%#v, want thread-2 first (most recently updated)", threads)
	}
	if threads[0].Title != "" {
		t.Fatalf("title=%q, want untitled thread", threads[0].Title)
	}
}

func TestListThreadsOnlyReturnsMatchingChannel(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	now := time.Now()
	if _, err := store.CreateThread(context.Background(), "web-thread", "web", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateThread(context.Background(), "telegram", "telegram", now); err != nil {
		t.Fatal(err)
	}

	threads, err := store.ListThreads(context.Background(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != "web-thread" {
		t.Fatalf("threads=%#v, want only the web thread", threads)
	}
}

func TestGetThreadReportsNotFoundForAnUnknownID(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	if _, found, err := store.GetThread(context.Background(), "missing"); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("expected found=false for an unknown thread ID")
	}
}

func TestSetThreadTitleNeverOverwritesAnAlreadyTitledThread(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	if _, err := store.CreateThread(context.Background(), "thread-1", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadTitle(context.Background(), "thread-1", "First title"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadTitle(context.Background(), "thread-1", "Second title"); err != nil {
		t.Fatal(err)
	}

	thread, found, err := store.GetThread(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if !found || thread.Title != "First title" {
		t.Fatalf("thread=%#v, want title unchanged after a second SetThreadTitle call", thread)
	}
}

func TestRenameThreadOverwritesAnExistingTitle(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	if _, err := store.CreateThread(context.Background(), "thread-1", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetThreadTitle(context.Background(), "thread-1", "Auto title"); err != nil {
		t.Fatal(err)
	}
	if err := store.RenameThread(context.Background(), "thread-1", "Owner title"); err != nil {
		t.Fatal(err)
	}

	thread, found, err := store.GetThread(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if !found || thread.Title != "Owner title" {
		t.Fatalf("thread=%#v, want the renamed title", thread)
	}
}

func TestDeleteThreadRemovesItsMessagesAndResetMarkerButLeavesOtherThreads(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"thread-1", "thread-2"} {
		if _, err := store.CreateThread(context.Background(), id, "web", now); err != nil {
			t.Fatal(err)
		}
		if err := store.WriteMessage(context.Background(), ports.StoredMessage{
			ConversationID: id, Role: ports.RoleUser, Content: "hi from " + id, Source: "web", CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ResetConversation(context.Background(), "thread-1", now); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteThread(context.Background(), "thread-1"); err != nil {
		t.Fatal(err)
	}

	if _, found, err := store.GetThread(context.Background(), "thread-1"); err != nil || found {
		t.Fatalf("thread-1 still present: found=%v err=%v", found, err)
	}
	// SearchText reads the messages table directly, so it proves the rows
	// are gone rather than merely hidden behind a reset marker.
	found, err := store.SearchText(context.Background(), "hi from", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Content != "hi from thread-2" {
		t.Fatalf("messages=%#v, want only thread-2's to survive", found)
	}
	// The reset marker is keyed by conversation ID, so a recycled ID must
	// not inherit the deleted thread's cleared_at.
	if _, err := store.CreateThread(context.Background(), "thread-1", "web", now); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMessage(context.Background(), ports.StoredMessage{
		ConversationID: "thread-1", Role: ports.RoleUser, Content: "second life", Source: "web", CreatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := store.RecentMessages(context.Background(), "thread-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages=%#v, want the reset marker gone with the thread", messages)
	}
}

func TestRecentMessagesIsScopedToOneConversationOldestFirstAndBounded(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	for index, text := range []string{"one", "two", "three"} {
		if err := store.WriteMessage(context.Background(), ports.StoredMessage{
			ConversationID: "thread-a", Role: ports.RoleUser, Content: text, Source: "web",
			CreatedAt: base.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMessage(context.Background(), ports.StoredMessage{
		ConversationID: "thread-b", Role: ports.RoleUser, Content: "other thread", Source: "web", CreatedAt: base,
	}); err != nil {
		t.Fatal(err)
	}

	messages, err := store.RecentMessages(context.Background(), "thread-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content != "two" || messages[1].Content != "three" {
		t.Fatalf("messages=%#v, want the last 2 of thread-a, oldest first", messages)
	}
}

func TestResetConversationHidesEarlierMessagesButLeavesThemSearchable(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	if err := store.WriteMessage(context.Background(), ports.StoredMessage{
		ConversationID: "thread-a", Role: ports.RoleUser, Content: "before reset unique-phrase", Source: "web", CreatedAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ResetConversation(context.Background(), "thread-a", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMessage(context.Background(), ports.StoredMessage{
		ConversationID: "thread-a", Role: ports.RoleUser, Content: "after reset", Source: "web", CreatedAt: base.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	messages, err := store.RecentMessages(context.Background(), "thread-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "after reset" {
		t.Fatalf("messages=%#v, want only the post-reset message", messages)
	}

	found, err := store.SearchText(context.Background(), "unique-phrase", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("search results=%#v, want the pre-reset message still searchable", found)
	}
}

func TestWriteMessageTouchesItsThreadsUpdatedAt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateThread(context.Background(), "thread-a", "web", base); err != nil {
		t.Fatal(err)
	}
	written := base.Add(time.Hour)
	if err := store.WriteMessage(context.Background(), ports.StoredMessage{
		ConversationID: "thread-a", Role: ports.RoleUser, Content: "hi", Source: "web", CreatedAt: written,
	}); err != nil {
		t.Fatal(err)
	}

	thread, found, err := store.GetThread(context.Background(), "thread-a")
	if err != nil {
		t.Fatal(err)
	}
	if !found || !thread.UpdatedAt.Equal(written) {
		t.Fatalf("thread=%#v, want updated_at bumped to %v", thread, written)
	}
}

func newTestStore(t *testing.T, _ int) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
