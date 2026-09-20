package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrSessionNotFound reports a session hash with no live row behind it:
// never issued, expired, or revoked. The three are deliberately one answer,
// so a caller cannot tell a revoked token from a forged one.
var ErrSessionNotFound = errors.New("session not found")

// sessionSchema holds browser sessions. Both sides of a session store only
// a hash of what the browser holds: a database read yields nothing that can
// be presented as a cookie.
//
// Neither table carries private records, so neither takes a principal: a
// session is what establishes the principal, and revoking every session of
// an account is an operation on the account, not one made as it.
const sessionSchema = `
CREATE TABLE IF NOT EXISTS sessions (
    hash       TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_account ON sessions(account_id);
`

// CreateSession records a session for accountID under the token's hash.
// Expired sessions are pruned on the way, which keeps the table bounded
// without a sweeper.
func (s *Store) CreateSession(ctx context.Context, hash, accountID string, expiresAt time.Time) error {
	now := time.Now().UTC()
	if err := s.pruneAuth(ctx, now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (hash, account_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hash, accountID, formatTime(now), formatTime(expiresAt.UTC()))
	return err
}

// SessionAccount resolves a live session to its account as of now.
func (s *Store) SessionAccount(ctx context.Context, hash string, now time.Time) (string, error) {
	var accountID, expires string
	err := s.db.QueryRowContext(ctx, `SELECT account_id, expires_at FROM sessions WHERE hash = ?`, hash).Scan(&accountID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSessionNotFound
	}
	if err != nil {
		return "", err
	}
	expiresAt, err := parseTime(expires)
	if err != nil {
		return "", err
	}
	if !now.Before(expiresAt) {
		return "", ErrSessionNotFound
	}
	return accountID, nil
}

// RevokeSession ends one session. Revoking one that is already gone is not
// an error: the caller asked for it to be absent, and it is.
func (s *Store) RevokeSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE hash = ?`, hash)
	return err
}

// RevokeAccountSessions ends every session of one account: on removal, on
// identity reset, or on any other reason the account must sign in again.
func (s *Store) RevokeAccountSessions(ctx context.Context, accountID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE account_id = ?`, accountID)
	return err
}

// ActiveSessions counts an account's unexpired sessions, for the accounts
// card's "signed in" indicator.
func (s *Store) ActiveSessions(ctx context.Context, accountID string, now time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE account_id = ? AND expires_at > ?`, accountID, formatTime(now.UTC())).Scan(&count)
	return count, err
}

// pruneAuth drops expired sessions and web login links. It runs inside
// the operations that add rows, so the tables stay bounded by live traffic
// rather than by a goroutine nobody wants.
func (s *Store) pruneAuth(ctx context.Context, now time.Time) error {
	stamp := formatTime(now)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, stamp); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_login_links WHERE expires_at <= ?`, now.UnixMilli())
	return err
}
