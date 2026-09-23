// Package sqlite is Eggy's machine-state authority: the one database at
// <home>/eggy.db holding everything the daemon manages for itself --
// conversation history and traces, operational state, schedules, and sealed
// OAuth grants. It is the third of the three durable forms (YAML for startup
// config, Markdown for owner-facing documents, SQLite for everything
// machine-managed), and the reason a home no longer carries state.json,
// cron/, or auth.json beside it.
//
// One *sql.DB with a single connection backs every store handed out here, so
// a state update, a schedule claim, and a message append are serialized
// against each other rather than racing for the same file lock.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"github.com/nigelteosw/eggy/internal/ports"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      TEXT    NOT NULL DEFAULT '',
    conversation_id TEXT    NOT NULL DEFAULT 'owner',
    role            TEXT    NOT NULL,
    content         TEXT    NOT NULL,
    source          TEXT    NOT NULL,
    created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content, content='messages', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;

CREATE TABLE IF NOT EXISTS threads (
    id                   TEXT PRIMARY KEY,
    account_id           TEXT    NOT NULL DEFAULT '',
    title                TEXT,
    channel              TEXT    NOT NULL,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    workspace            TEXT,
    workspace_repository TEXT,
    workspace_branch     TEXT,
    workspace_session    TEXT
);

CREATE TABLE IF NOT EXISTS conversation_resets (
    account_id      TEXT    NOT NULL DEFAULT '',
    conversation_id TEXT    NOT NULL,
    cleared_at      INTEGER NOT NULL,
    PRIMARY KEY (account_id, conversation_id)
);

CREATE TABLE IF NOT EXISTS traces (
    id              TEXT    PRIMARY KEY,
    account_id      TEXT    NOT NULL DEFAULT '',
    conversation_id TEXT    NOT NULL,
    session         TEXT    NOT NULL DEFAULT '',
    channel         TEXT    NOT NULL,
    source          TEXT    NOT NULL,
    kind            TEXT    NOT NULL,
    model           TEXT    NOT NULL,
    effort          TEXT    NOT NULL,
    input           TEXT    NOT NULL,
    output          TEXT    NOT NULL,
    error           TEXT    NOT NULL,
    usage           TEXT    NOT NULL,
    started_at      INTEGER NOT NULL,
    duration_ns     INTEGER NOT NULL,
    complete        INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS trace_spans (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id    TEXT    NOT NULL,
    sequence    INTEGER NOT NULL,
    kind        TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    call_id     TEXT    NOT NULL,
    request     TEXT    NOT NULL,
    response    TEXT    NOT NULL,
    error       TEXT    NOT NULL,
    usage       TEXT    NOT NULL,
    started_at  INTEGER NOT NULL,
    duration_ns INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trace_spans_trace ON trace_spans(trace_id, sequence);
`

// Store is a SQLite-backed durable message store.
type Store struct {
	db   *sql.DB
	path string
}

// Open creates a Store at path and initializes its schema. A fresh database
// gets the current schema directly; an existing one runs every in-place
// upgrade and is then refused if it still needs the offline local login
// cutover (see OpenForLocalAuthMigration).
func Open(path string, _ ...int) (*Store, error) {
	db, err := openDatabase(path)
	if err != nil {
		return nil, err
	}
	if err := upgradeBeforeLocalAuth(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := requireLocalLoginCutover(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(accountAuthSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := recordMachineStateVersion(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{db: db, path: path}
	if err := store.tightenPrivateFiles(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// openDatabase opens the file and applies the base schema, which is safe on
// any database: every statement is IF NOT EXISTS.
func openDatabase(path string) (*sql.DB, error) {
	if err := prepareDatabaseFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	for _, stmt := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000`, schema, machineSchema, sessionSchema, identityLinkSchema} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

// upgradeBeforeLocalAuth runs the in-place upgrades that predate local auth.
// Each is driven by the tables' actual shape and idempotent, so a crash
// halfway is repaired by running them again.
func upgradeBeforeLocalAuth(db *sql.DB) error {
	// Refuse a newer database before touching it; the stamp itself is
	// written again by the caller, after every upgrade has run.
	if err := refuseNewerMachineState(db); err != nil {
		return err
	}
	if err := ensureThreadWorkspaceColumns(db); err != nil {
		return err
	}
	// Traces recorded before /clear started a new one all carry the empty
	// session, which is exactly right: they are the one stretch that ran
	// before anybody could separate them.
	if err := ensureColumn(db, "traces", "session", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := upgradeToAccounts(db); err != nil {
		return err
	}
	return upgradeIdentityLinks(db)
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

// ensureThreadWorkspaceColumns migrates a threads table created before
// workspaces were attachable to a thread. Fresh databases get the columns
// from the schema; existing ones get them added here, defaulting to NULL
// (no workspace attached, no branch), so no /data/memory.db needs
// replacing.
func ensureThreadWorkspaceColumns(db *sql.DB) error {
	if err := ensureColumn(db, "threads", "workspace", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "threads", "workspace_repository", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "threads", "workspace_branch", "TEXT"); err != nil {
		return err
	}
	return ensureColumn(db, "threads", "workspace_session", "TEXT")
}

// ensureColumn adds column to table when it is absent, so an existing
// database migrates in place rather than failing to open.
func ensureColumn(db *sql.DB, table, column, columnType string) error {
	return ensureColumnTx(db, table, column, columnType)
}

// Close closes the underlying database pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// accountOf is the one place a private operation learns who it acts for.
// It fails closed: no principal, no query.
func accountOf(ctx context.Context) (string, error) {
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", err
	}
	return principal.AccountID, nil
}
