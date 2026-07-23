// Package sqlite provides SQLite-backed durable conversation storage.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

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
    embedding       BLOB
);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content, content='messages', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;
`

// Store is a SQLite-backed durable message store.
type Store struct {
	db             *sql.DB
	candidateLimit int
}

// Open creates a Store at path and initializes its schema.
func Open(path string, candidateLimit int) (*Store, error) {
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

	return &Store{db: db, candidateLimit: candidateLimit}, nil
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
	return err
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

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.role, m.content, m.source, m.created_at
		FROM messages_fts
		JOIN messages AS m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ?
		ORDER BY bm25(messages_fts), m.created_at DESC
		LIMIT ?
	`, query, limit)
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
