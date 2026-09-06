package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// A database written before traces had a session must open, not fail, and its
// existing traces belong to the one stretch that ran before /clear could
// separate them.
func TestTraceSessionColumnIsAddedToAnOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE traces (
		id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, channel TEXT NOT NULL,
		source TEXT NOT NULL, kind TEXT NOT NULL, model TEXT NOT NULL, effort TEXT NOT NULL,
		input TEXT NOT NULL, output TEXT NOT NULL, error TEXT NOT NULL, usage TEXT NOT NULL,
		started_at INTEGER NOT NULL, duration_ns INTEGER NOT NULL, complete INTEGER NOT NULL);
		INSERT INTO traces VALUES ('old', 'telegram', 'telegram', 'telegram', 'owner', 'm', '', 'hi', '', '', '{}', 1, 0, 1);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path, 100)
	if err != nil {
		t.Fatalf("opening a pre-session database: %v", err)
	}
	defer store.Close()

	listed, err := store.ListTraces(context.Background(), 10)
	if err != nil || len(listed) != 1 || listed[0].Session != "" {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	if err := store.StartTrace(context.Background(), ports.Trace{
		ID: "new", ConversationID: "telegram", Session: "42", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("writing after the migration: %v", err)
	}
}
