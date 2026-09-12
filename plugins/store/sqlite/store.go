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
	"strings"
	"time"
	"unicode"

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

// Open creates a Store at path and initializes its schema.
func Open(path string, _ ...int) (*Store, error) {
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
	if _, err := db.Exec(machineSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(sessionSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Refuse a newer database before touching it; the stamp itself is
	// written again below, after every upgrade has run.
	if err := refuseNewerMachineState(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureThreadWorkspaceColumns(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Traces recorded before /clear started a new one all carry the empty
	// session, which is exactly right: they are the one stretch that ran
	// before anybody could separate them.
	if err := ensureColumn(db, "traces", "session", "TEXT NOT NULL DEFAULT ''"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := upgradeToAccounts(db); err != nil {
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

// WriteMessage persists one durable conversation message, scoped to the
// acting account and message.ConversationID (a web thread's own ID, or
// Telegram's fixed thread). Best-effort bumps the owning thread's updated_at
// for sidebar ordering; a no-op when ConversationID doesn't match one of the
// account's threads rows (e.g. Telegram's fixed thread, which is never listed
// there).
func (s *Store) WriteMessage(ctx context.Context, message ports.StoredMessage) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO messages (account_id, conversation_id, role, content, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, account, message.ConversationID, message.Role, message.Content, message.Source, message.CreatedAt.UnixNano())
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE threads SET updated_at = ? WHERE account_id = ? AND id = ?`, message.CreatedAt.UnixNano(), account, message.ConversationID); err != nil {
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
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.role, m.content, m.source, m.created_at
		FROM messages m
		LEFT JOIN conversation_resets r ON r.account_id = m.account_id AND r.conversation_id = m.conversation_id
		WHERE m.account_id = ? AND m.conversation_id = ? AND (r.cleared_at IS NULL OR m.created_at > r.cleared_at)
		ORDER BY m.id DESC
		LIMIT ?
	`, account, conversationID, limit)
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
// point. Durable history is untouched -- SearchText keeps
// finding everything.
func (s *Store) ResetConversation(ctx context.Context, conversationID string, at time.Time) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO conversation_resets (account_id, conversation_id, cleared_at) VALUES (?, ?, ?)
		ON CONFLICT(account_id, conversation_id) DO UPDATE SET cleared_at = excluded.cleared_at
	`, account, conversationID, at.UnixNano())
	return err
}

// ConversationResetAt reports when conversationID was last cleared. It reads
// the same row ResetConversation writes, so "which stretch of the
// conversation is this" has one answer whether the asker is the context
// window or the traces panel.
func (s *Store) ConversationResetAt(ctx context.Context, conversationID string) (time.Time, bool, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	var clearedAt int64
	err = s.db.QueryRowContext(ctx, `
		SELECT cleared_at FROM conversation_resets WHERE account_id = ? AND conversation_id = ?
	`, account, conversationID).Scan(&clearedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(0, clearedAt).UTC(), true, nil
}

const threadColumns = `id, account_id, title, channel, created_at, updated_at, workspace, workspace_repository, workspace_branch, workspace_session`

// CreateThread persists a new, untitled thread with no workspace attached.
// Thread IDs are global -- the web surface generates them randomly -- so a
// collision with another account's thread is an error rather than a merge.
func (s *Store) CreateThread(ctx context.Context, id, channel string, at time.Time) (ports.Thread, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return ports.Thread{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO threads (id, account_id, title, channel, created_at, updated_at) VALUES (?, ?, NULL, ?, ?, ?)
	`, id, account, channel, at.UnixNano(), at.UnixNano()); err != nil {
		return ports.Thread{}, err
	}
	return ports.Thread{ID: id, Owner: account, Channel: channel, CreatedAt: at, UpdatedAt: at}, nil
}

// ListThreads returns the account's threads on channel, most-recently-active
// first.
func (s *Store) ListThreads(ctx context.Context, channel string) ([]ports.Thread, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	return s.queryThreads(ctx, `
		SELECT `+threadColumns+` FROM threads
		WHERE account_id = ? AND channel = ?
		ORDER BY updated_at DESC
	`, account, channel)
}

// ThreadsWithWorkspace returns every thread that currently has a checkout
// attached, across accounts, oldest activity first so a reaper walks the
// stalest first. It is the one thread read that takes no principal: the
// reaper is housekeeping, and each thread carries its Owner so the reaper
// can act as that account when it detaches.
func (s *Store) ThreadsWithWorkspace(ctx context.Context) ([]ports.Thread, error) {
	return s.queryThreads(ctx, `
		SELECT `+threadColumns+` FROM threads
		WHERE workspace IS NOT NULL AND workspace <> ''
		ORDER BY updated_at ASC
	`)
}

func (s *Store) queryThreads(ctx context.Context, query string, args ...any) ([]ports.Thread, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var threads []ports.Thread
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

// GetThread looks up one of the account's threads by ID. found is false,
// with a nil error, when the account has no such thread -- including when
// another account does, which is not this caller's business to learn.
func (s *Store) GetThread(ctx context.Context, id string) (thread ports.Thread, found bool, err error) {
	account, err := accountOf(ctx)
	if err != nil {
		return ports.Thread{}, false, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+threadColumns+` FROM threads WHERE account_id = ? AND id = ?`, account, id)
	thread, err = scanThread(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.Thread{}, false, nil
	}
	if err != nil {
		return ports.Thread{}, false, err
	}
	return thread, true, nil
}

// AttachWorkspace records a checkout on a thread. It upserts the thread row
// because Telegram's fixed thread never goes through CreateThread: it has
// no sidebar entry to create, but it can still open a workspace. The upsert
// only updates a row the account owns; another account's thread of the same
// ID is a primary-key collision and fails.
func (s *Store) AttachWorkspace(ctx context.Context, id, channel, repository, workspace string, at time.Time) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO threads (id, account_id, title, channel, created_at, updated_at, workspace, workspace_repository)
		VALUES (?, ?, NULL, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace = excluded.workspace,
			workspace_repository = excluded.workspace_repository,
			workspace_branch = NULL,
			workspace_session = NULL,
			updated_at = excluded.updated_at
		WHERE threads.account_id = excluded.account_id
	`, id, account, channel, at.UnixNano(), at.UnixNano(), workspace, repository)
	return err
}

// DetachWorkspace clears a thread's attached workspace. Detaching a thread
// that has none, or that does not exist, is not an error.
func (s *Store) DetachWorkspace(ctx context.Context, id string) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE threads SET workspace = NULL, workspace_repository = NULL, workspace_branch = NULL, workspace_session = NULL
		WHERE account_id = ? AND id = ?
	`, account, id)
	return err
}

// SetThreadTitle auto-titles a thread from its first exchange: a no-op
// once the thread already has a title, so a later call never overwrites an
// owner's or a previous exchange's title.
func (s *Store) SetThreadTitle(ctx context.Context, id, title string) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE threads SET title = ? WHERE account_id = ? AND id = ? AND title IS NULL`, title, account, id)
	return err
}

// RenameThread sets a thread's title outright, unlike SetThreadTitle: this
// is the owner naming their own conversation, so it overwrites whatever
// auto-titling produced. Renaming a thread that does not exist is not an
// error -- the handler has already established the thread is there, and a
// racing delete should not surface as a failure.
func (s *Store) RenameThread(ctx context.Context, id, title string) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE threads SET title = ? WHERE account_id = ? AND id = ?`, title, account, id)
	return err
}

// DeleteThread removes a thread and everything keyed to it: its messages
// and its reset marker. The three statements run in one transaction so a
// failure never leaves messages orphaned behind a deleted thread row, where
// nothing would ever list or clean them up.
//
// An attached workspace is not removed from disk here; the store does not
// own checkouts. Callers that can delete a thread with a workspace should
// detach it first.
func (s *Store) DeleteThread(ctx context.Context, id string) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, statement := range []string{
		`DELETE FROM messages WHERE account_id = ? AND conversation_id = ?`,
		`DELETE FROM conversation_resets WHERE account_id = ? AND conversation_id = ?`,
		`DELETE FROM threads WHERE account_id = ? AND id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, account, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type rowScanner interface {
	Scan(...any) error
}

func scanThread(row rowScanner) (ports.Thread, error) {
	var thread ports.Thread
	var title, workspace, workspaceRepository, workspaceBranch, workspaceSession sql.NullString
	var createdAt, updatedAt int64
	if err := row.Scan(&thread.ID, &thread.Owner, &title, &thread.Channel, &createdAt, &updatedAt, &workspace, &workspaceRepository, &workspaceBranch, &workspaceSession); err != nil {
		return ports.Thread{}, err
	}
	thread.Title = title.String
	thread.Workspace = workspace.String
	thread.WorkspaceRepository = workspaceRepository.String
	thread.CreatedAt = time.Unix(0, createdAt).UTC()
	thread.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return thread, nil
}

// SearchText returns the account's keyword matches ordered by FTS5
// relevance, then newest message for equal relevance. The account predicate
// sits on the joined messages row: the FTS index is shared, and what makes
// recall private is that no row of another account's survives the join.
func (s *Store) SearchText(ctx context.Context, query string, limit int) ([]ports.StoredMessage, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("memory text search query is required")
	}
	if limit <= 0 {
		return nil, errors.New("memory text search limit must be positive")
	}
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	ftsQuery := literalFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.role, m.content, m.source, m.created_at
		FROM messages_fts
		JOIN messages AS m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ? AND m.account_id = ?
		ORDER BY bm25(messages_fts), m.created_at DESC
		LIMIT ?
	`, ftsQuery, account, limit)
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
