package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// identityLinkSchema holds the pending linking tokens an owner creates in
// the panel and redeems from a chat surface: one per account per
// connection. The connection column is what generalises the table beyond
// Telegram -- a Discord token can never redeem a Telegram binding, because
// the claim is keyed on both.
//
// Only the token's hash is stored, never the token; the claim_id marks a
// token mid-redemption so a concurrent second redemption finds nothing.
const identityLinkSchema = `
CREATE TABLE IF NOT EXISTS identity_links (
    account_id TEXT NOT NULL,
    connection TEXT NOT NULL,
    code_hash  BLOB UNIQUE NOT NULL,
    expires_at INTEGER NOT NULL,
    claim_id   BLOB UNIQUE,
    PRIMARY KEY (account_id, connection)
);
`

// upgradeIdentityLinks carries pending Telegram pairings into the
// generalised table under connection=telegram and drops the old table. It
// runs inside one transaction so a crash leaves either the old table intact
// or the new rows in place, never half of each; and it is a no-op once the
// old table is gone, so a reopen after a crash simply runs it again.
func upgradeIdentityLinks(db *sql.DB) error {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'telegram_pairings'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO identity_links (account_id, connection, code_hash, expires_at, claim_id)
		SELECT account_id, 'telegram', code_hash, expires_at, claim_id FROM telegram_pairings
	`); err != nil {
		return fmt.Errorf("migrate telegram pairings: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE telegram_pairings`); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateIdentityLink stores a fresh token hash for the account on the
// connection, replacing an unclaimed earlier one. A token that is being
// redeemed right now is not replaced: the redemption in flight must finish
// against the row it claimed.
func (s *Store) CreateIdentityLink(ctx context.Context, accountID, connection string, codeHash [32]byte, expiresAt time.Time) error {
	if connection == "" {
		return errors.New("identity link needs a connection")
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO identity_links (account_id, connection, code_hash, expires_at, claim_id)
		VALUES (?, ?, ?, ?, NULL)
		ON CONFLICT(account_id, connection) DO UPDATE SET code_hash=excluded.code_hash, expires_at=excluded.expires_at, claim_id=NULL
		WHERE identity_links.claim_id IS NULL
	`, accountID, connection, codeHash[:], expiresAt.UTC().Unix())
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("a linking token is already being claimed")
	}
	return nil
}

// ClaimIdentityLink marks the unexpired token with this hash on this
// connection as being redeemed and returns the account it belongs to. Exactly
// one caller wins a concurrent claim; a token on another connection is not
// found at all.
func (s *Store) ClaimIdentityLink(ctx context.Context, connection string, codeHash [32]byte, now time.Time) (string, [16]byte, bool, error) {
	var empty [16]byte
	claim := [16]byte{}
	if _, err := rand.Read(claim[:]); err != nil {
		return "", empty, false, fmt.Errorf("generate identity link claim: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", empty, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM identity_links WHERE expires_at <= ? AND claim_id IS NULL`, now.UTC().Unix()); err != nil {
		return "", empty, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE identity_links SET claim_id = ? WHERE connection = ? AND code_hash = ? AND claim_id IS NULL`, claim[:], connection, codeHash[:])
	if err != nil {
		return "", empty, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return "", empty, false, err
	}
	if changed != 1 {
		if err := tx.Commit(); err != nil {
			return "", empty, false, err
		}
		return "", empty, false, nil
	}
	var accountID string
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM identity_links WHERE claim_id = ?`, claim[:]).Scan(&accountID); err != nil {
		return "", empty, false, err
	}
	if err := tx.Commit(); err != nil {
		return "", empty, false, err
	}
	return accountID, claim, true, nil
}

// FinishIdentityLink consumes a claimed token on success or releases it on
// failure so the owner can retry the same token.
func (s *Store) FinishIdentityLink(ctx context.Context, claimID [16]byte, success bool) error {
	var err error
	if success {
		_, err = s.db.ExecContext(ctx, `DELETE FROM identity_links WHERE claim_id = ?`, claimID[:])
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE identity_links SET claim_id = NULL WHERE claim_id = ?`, claimID[:])
	}
	return err
}

// DeleteIdentityLinks cancels the account's unclaimed token on the
// connection, or on every connection when connection is empty (the account
// is being removed).
func (s *Store) DeleteIdentityLinks(ctx context.Context, accountID, connection string) error {
	if connection == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM identity_links WHERE account_id = ? AND claim_id IS NULL`, accountID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM identity_links WHERE account_id = ? AND connection = ? AND claim_id IS NULL`, accountID, connection)
	return err
}
