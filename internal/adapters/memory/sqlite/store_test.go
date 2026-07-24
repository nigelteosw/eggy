package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestOpenUsesDefaultCandidateLimit(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "eggy.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got, want := store.candidateLimit, 5000; got != want {
		t.Fatalf("candidate limit = %d, want default %d", got, want)
	}
}

func TestOpenRejectsNegativeCandidateLimit(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "eggy.db"), -1)
	if err == nil {
		_ = store.Close()
		t.Fatal("Open negative candidate limit error = nil, want validation error")
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

func TestPendingEmbeddingsReturnsOnlyMessagesWithoutEmbeddings(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	ctx := context.Background()
	firstID := writeTestMessage(t, store, "first pending", time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC))
	secondID := writeTestMessage(t, store, "embedded", time.Date(2026, time.July, 23, 12, 0, 1, 0, time.UTC))
	thirdID := writeTestMessage(t, store, "third pending", time.Date(2026, time.July, 23, 12, 0, 2, 0, time.UTC))
	if err := store.SetEmbedding(ctx, secondID, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}

	results, err := store.PendingEmbeddings(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("pending message count = %d, want 2", len(results))
	}
	if got, want := []int64{results[0].ID, results[1].ID}, []int64{thirdID, firstID}; got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("pending message IDs = %v, want %v", got, want)
	}
}

func TestEmbeddingProfilePersistsAndSameProfileRowsRemainSearchable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "eggy.db")
	store, err := OpenWithProfile(path, 100, "opaque-profile-a")
	if err != nil {
		t.Fatal(err)
	}
	id := writeTestMessage(t, store, "profiled memory", time.Now())
	if err := store.SetEmbedding(context.Background(), id, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenWithProfile(path, 100, "opaque-profile-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	pending, err := reopened.PendingEmbeddings(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("same-profile pending = %#v, want none", pending)
	}
	results, err := reopened.SearchSimilar(context.Background(), []float32{1, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != id {
		t.Fatalf("same-profile results = %#v, want message %d", results, id)
	}
}

func TestSearchSimilarUsesProfileLeadingIndex(t *testing.T) {
	t.Parallel()

	store := newTestStoreWithProfile(t, 100, "opaque-profile")
	assertSimilarityProfileIndexUsed(t, store)
}

func TestChangedEmbeddingProfileRequeuesAndFiltersOldVectors(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "eggy.db")
	first, err := OpenWithProfile(path, 100, "opaque-profile-a")
	if err != nil {
		t.Fatal(err)
	}
	id := writeTestMessage(t, first, "profile changed", time.Now())
	if err := first.SetEmbedding(context.Background(), id, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	changed, err := OpenWithProfile(path, 100, "opaque-profile-b")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = changed.Close() })
	pending, err := changed.PendingEmbeddings(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != id {
		t.Fatalf("changed-profile pending = %#v, want message %d", pending, id)
	}
	results, err := changed.SearchSimilar(context.Background(), []float32{1, 0, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("changed-profile results = %#v, want old vector filtered", results)
	}
	if err := changed.SetEmbedding(context.Background(), id, []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	results, err = changed.SearchSimilar(context.Background(), []float32{0, 1, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != id {
		t.Fatalf("re-embedded results = %#v, want message %d", results, id)
	}
}

func TestOpenWithProfileMigratesDatabaseWithoutEmbeddingProfile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "eggy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL DEFAULT 'owner',
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			source TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			embedding BLOB
		);
		INSERT INTO messages (role, content, source, created_at, embedding)
		VALUES ('user', 'legacy vector', 'telegram', 1, X'0000803F00000000');
	`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenWithProfile(path, 100, "opaque-profile-new")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	pending, err := store.PendingEmbeddings(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Content != "legacy vector" {
		t.Fatalf("migrated pending = %#v", pending)
	}
	var profile sql.NullString
	if err := store.db.QueryRow(`SELECT embedding_profile FROM messages WHERE content = 'legacy vector'`).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	if profile.Valid {
		t.Fatalf("legacy embedding profile = %q, want NULL until re-embedded", profile.String)
	}
	assertSimilarityProfileIndexUsed(t, store)
}

func TestSetEmbeddingStoresLittleEndianFloat32Blob(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	id := writeTestMessage(t, store, "embedded message", time.Now())
	want := []float32{1.5, -2}
	if err := store.SetEmbedding(context.Background(), id, want); err != nil {
		t.Fatal(err)
	}

	var got []byte
	if err := store.db.QueryRow(`SELECT embedding FROM messages WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want)*4 {
		t.Fatalf("embedding bytes = %d, want %d", len(got), len(want)*4)
	}
	for index, value := range want {
		if actual := math.Float32frombits(binary.LittleEndian.Uint32(got[index*4:])); actual != value {
			t.Fatalf("embedding value %d = %v, want %v", index, actual, value)
		}
	}
}

func TestSetEmbeddingRejectsEmptyAndNonFiniteVectors(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	id := writeTestMessage(t, store, "embedded message", time.Now())
	for _, embedding := range [][]float32{{}, {float32(math.NaN())}, {float32(math.Inf(1))}} {
		if err := store.SetEmbedding(context.Background(), id, embedding); err == nil {
			t.Fatalf("SetEmbedding(%v) error = nil, want validation error", embedding)
		}
	}
}

func TestSearchSimilarRanksNewestCandidatesByCosineSimilarity(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 2)
	ctx := context.Background()
	oldestID := writeTestMessage(t, store, "oldest perfect", time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC))
	middleID := writeTestMessage(t, store, "middle orthogonal", time.Date(2026, time.July, 23, 12, 0, 1, 0, time.UTC))
	newestID := writeTestMessage(t, store, "newest partial", time.Date(2026, time.July, 23, 12, 0, 2, 0, time.UTC))
	for id, embedding := range map[int64][]float32{
		oldestID: {1, 0},
		middleID: {0, 1},
		newestID: {1, 1},
	} {
		if err := store.SetEmbedding(ctx, id, embedding); err != nil {
			t.Fatal(err)
		}
	}

	results, err := store.SearchSimilar(ctx, []float32{1, 0}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("similar message count = %d, want 2 bounded candidates", len(results))
	}
	if got, want := []int64{results[0].ID, results[1].ID}, []int64{newestID, middleID}; got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("similar message IDs = %v, want %v", got, want)
	}
}

func TestSearchSimilarRejectsMismatchedDimensions(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	id := writeTestMessage(t, store, "two dimensions", time.Now())
	if err := store.SetEmbedding(context.Background(), id, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SearchSimilar(context.Background(), []float32{1, 0, 0}, 1); err == nil {
		t.Fatal("SearchSimilar mismatched dimensions error = nil, want validation error")
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

func newTestStore(t *testing.T, candidateLimit int) *Store {
	t.Helper()

	return newTestStoreWithProfile(t, candidateLimit, "")
}

func newTestStoreWithProfile(t *testing.T, candidateLimit int, profile string) *Store {
	t.Helper()

	store, err := OpenWithProfile(filepath.Join(t.TempDir(), "eggy.db"), candidateLimit, profile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func assertSimilarityProfileIndexUsed(t *testing.T, store *Store) {
	t.Helper()

	rows, err := store.db.Query(`
		EXPLAIN QUERY PLAN
		SELECT id, role, content, source, created_at, embedding
		FROM messages
		WHERE embedding IS NOT NULL
		  AND embedding_profile = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, store.profile, store.candidateLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "idx_messages_embedding_profile_created_at") {
		t.Fatalf("similarity query plan = %q, want profile-leading index", plan)
	}
}

func writeTestMessage(t *testing.T, store *Store, content string, createdAt time.Time) int64 {
	t.Helper()
	if err := store.WriteMessage(context.Background(), ports.StoredMessage{
		Role:      ports.RoleUser,
		Content:   content,
		Source:    "telegram",
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}

	var id int64
	if err := store.db.QueryRow(`SELECT id FROM messages WHERE content = ?`, content).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			t.Fatalf("message %q was not stored", content)
		}
		t.Fatal(err)
	}
	return id
}
