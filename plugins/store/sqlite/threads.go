package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

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
