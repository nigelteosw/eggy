package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"path/filepath"
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

func TestSearchTextRejectsEmptyQueryAndNonPositiveLimit(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, 100)
	if _, err := store.SearchText(context.Background(), "", 1); err == nil {
		t.Fatal("SearchText empty query error = nil, want validation error")
	}
	if _, err := store.SearchText(context.Background(), "durable", 0); err == nil {
		t.Fatal("SearchText zero limit error = nil, want validation error")
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

func newTestStore(t *testing.T, candidateLimit int) *Store {
	t.Helper()

	store, err := Open(filepath.Join(t.TempDir(), "eggy.db"), candidateLimit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
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
