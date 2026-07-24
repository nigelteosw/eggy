// Package sqlite provides SQLite-backed durable conversation storage.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/nigelteosw/eggy/internal/ports"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT    NOT NULL DEFAULT 'owner',
    role            TEXT    NOT NULL,
    content         TEXT    NOT NULL,
    source          TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    embedding       BLOB,
    embedding_profile TEXT
);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);
CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id, id);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content, content='messages', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;

CREATE TABLE IF NOT EXISTS threads (
    id         TEXT PRIMARY KEY,
    title      TEXT,
    channel    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_threads_channel_updated_at ON threads(channel, updated_at DESC);

CREATE TABLE IF NOT EXISTS conversation_resets (
    conversation_id TEXT    PRIMARY KEY,
    cleared_at      INTEGER NOT NULL
);
`

const similarityProfileIndex = `
CREATE INDEX IF NOT EXISTS idx_messages_embedding_profile_created_at
ON messages(embedding_profile, created_at DESC, id DESC)
WHERE embedding IS NOT NULL
`

const defaultCandidateLimit = 5000

// Store is a SQLite-backed durable message store.
type Store struct {
	db             *sql.DB
	path           string
	candidateLimit int
	profile        string
}

// Open creates a Store at path and initializes its schema.
func Open(path string, candidateLimit int) (*Store, error) {
	return OpenWithProfile(path, candidateLimit, "")
}

// OpenWithProfile creates a Store whose embeddings are associated with the
// supplied opaque profile.
func OpenWithProfile(path string, candidateLimit int, profile string) (*Store, error) {
	if candidateLimit < 0 {
		return nil, errors.New("candidate limit must not be negative")
	}
	if candidateLimit == 0 {
		candidateLimit = defaultCandidateLimit
	}
	if err := prepareDatabaseFile(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureEmbeddingProfileColumn(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(similarityProfileIndex); err != nil {
		_ = db.Close()
		return nil, err
	}

	store := &Store{db: db, path: path, candidateLimit: candidateLimit, profile: profile}
	if err := store.tightenPrivateFiles(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func prepareDatabaseFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (s *Store) tightenPrivateFiles() error {
	for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func ensureEmbeddingProfileColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "embedding_profile" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE messages ADD COLUMN embedding_profile TEXT`)
	return err
}

// Close closes the underlying database pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// WriteMessage persists one durable conversation message, scoped to
// message.ConversationID (a web thread's own ID, or Telegram's fixed
// thread). Best-effort bumps the owning thread's updated_at for sidebar
// ordering; a no-op when ConversationID doesn't match a threads row (e.g.
// Telegram's fixed thread, which is never listed there).
func (s *Store) WriteMessage(ctx context.Context, message ports.StoredMessage) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (conversation_id, role, content, source, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, message.ConversationID, message.Role, message.Content, message.Source, message.CreatedAt.UnixNano())
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE threads SET updated_at = ? WHERE id = ?`, message.CreatedAt.UnixNano(), message.ConversationID); err != nil {
		return err
	}
	return s.tightenPrivateFiles()
}

// RecentMessages returns conversationID's most recent messages, oldest
// first, bounded to limit, excluding anything at or before the
// conversation's last reset (see ResetConversation).
func (s *Store) RecentMessages(ctx context.Context, conversationID string, limit int) ([]ports.StoredMessage, error) {
	if limit <= 0 {
		return nil, errors.New("recent messages limit must be positive")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.role, m.content, m.source, m.created_at
		FROM messages m
		LEFT JOIN conversation_resets r ON r.conversation_id = m.conversation_id
		WHERE m.conversation_id = ? AND (r.cleared_at IS NULL OR m.created_at > r.cleared_at)
		ORDER BY m.id DESC
		LIMIT ?
	`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ports.StoredMessage
	for rows.Next() {
		var message ports.StoredMessage
		var createdAt int64
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &message.Source, &createdAt); err != nil {
			return nil, err
		}
		message.ConversationID = conversationID
		message.CreatedAt = time.Unix(0, createdAt).UTC()
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	return messages, nil
}

// ResetConversation clears conversationID's live turn-context window as of
// at: later RecentMessages calls only see messages recorded after this
// point. Durable history is untouched -- SearchText/SearchSimilar keep
// finding everything.
func (s *Store) ResetConversation(ctx context.Context, conversationID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_resets (conversation_id, cleared_at) VALUES (?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET cleared_at = excluded.cleared_at
	`, conversationID, at.UnixNano())
	return err
}

// Thread is one web sidebar conversation, or Telegram's single fixed
// thread. Title is empty until auto-titled from the thread's first
// exchange.
type Thread struct {
	ID        string
	Title     string
	Channel   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateThread persists a new, untitled thread.
func (s *Store) CreateThread(ctx context.Context, id, channel string, at time.Time) (Thread, error) {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO threads (id, title, channel, created_at, updated_at) VALUES (?, NULL, ?, ?, ?)
	`, id, channel, at.UnixNano(), at.UnixNano()); err != nil {
		return Thread{}, err
	}
	return Thread{ID: id, Channel: channel, CreatedAt: at, UpdatedAt: at}, nil
}

// ListThreads returns channel's threads, most-recently-active first.
func (s *Store) ListThreads(ctx context.Context, channel string) ([]Thread, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, channel, created_at, updated_at FROM threads
		WHERE channel = ?
		ORDER BY updated_at DESC
	`, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var threads []Thread
	for rows.Next() {
		thread, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		threads = append(threads, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return threads, nil
}

// GetThread looks up one thread by ID. found is false, with a nil error,
// when no such thread exists.
func (s *Store) GetThread(ctx context.Context, id string) (thread Thread, found bool, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, channel, created_at, updated_at FROM threads WHERE id = ?
	`, id)
	thread, err = scanThread(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, false, nil
	}
	if err != nil {
		return Thread{}, false, err
	}
	return thread, true, nil
}

// SetThreadTitle auto-titles a thread from its first exchange: a no-op
// once the thread already has a title, so a later call never overwrites an
// owner's or a previous exchange's title.
func (s *Store) SetThreadTitle(ctx context.Context, id, title string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE threads SET title = ? WHERE id = ? AND title IS NULL`, title, id)
	return err
}

type rowScanner interface {
	Scan(...any) error
}

func scanThread(row rowScanner) (Thread, error) {
	var thread Thread
	var title sql.NullString
	var createdAt, updatedAt int64
	if err := row.Scan(&thread.ID, &title, &thread.Channel, &createdAt, &updatedAt); err != nil {
		return Thread{}, err
	}
	thread.Title = title.String
	thread.CreatedAt = time.Unix(0, createdAt).UTC()
	thread.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return thread, nil
}

// SearchText returns keyword matches ordered by FTS5 relevance, then newest
// message for equal relevance.
func (s *Store) SearchText(ctx context.Context, query string, limit int) ([]ports.StoredMessage, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("memory text search query is required")
	}
	if limit <= 0 {
		return nil, errors.New("memory text search limit must be positive")
	}
	ftsQuery := literalFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.role, m.content, m.source, m.created_at
		FROM messages_fts
		JOIN messages AS m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ?
		ORDER BY bm25(messages_fts), m.created_at DESC
		LIMIT ?
	`, ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ports.StoredMessage
	for rows.Next() {
		var message ports.StoredMessage
		var createdAt int64
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &message.Source, &createdAt); err != nil {
			return nil, err
		}
		message.CreatedAt = time.Unix(0, createdAt).UTC()
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func literalFTSQuery(query string) string {
	var tokens []string
	var token strings.Builder
	hasBase := false
	flush := func() {
		if token.Len() > 0 && hasBase {
			escaped := strings.ReplaceAll(token.String(), `"`, `""`)
			tokens = append(tokens, `"`+escaped+`"`)
		}
		token.Reset()
		hasBase = false
	}
	for _, value := range query {
		switch {
		case unicode.IsLetter(value), unicode.IsNumber(value):
			token.WriteRune(value)
			hasBase = true
		case unicode.IsMark(value):
			token.WriteRune(value)
		default:
			flush()
		}
	}
	flush()
	return strings.Join(tokens, " AND ")
}
