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

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content, content='messages', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;
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

// WriteMessage persists one durable conversation message.
func (s *Store) WriteMessage(ctx context.Context, message ports.StoredMessage) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (role, content, source, created_at)
		VALUES (?, ?, ?, ?)
	`, message.Role, message.Content, message.Source, message.CreatedAt.UnixNano())
	if err != nil {
		return err
	}
	return s.tightenPrivateFiles()
}

// SearchText returns keyword matches ordered by FTS5 relevance, then newest
// message for equal relevance.
func (s *Store) SearchText(ctx context.Context, query string, limit int) ([]ports.StoredMessage, error) {
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
