package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const telegramPairingSchema = `
CREATE TABLE IF NOT EXISTS telegram_pairings (
    account_id TEXT PRIMARY KEY,
    code_hash  BLOB UNIQUE NOT NULL,
    expires_at INTEGER NOT NULL,
    claim_id   BLOB UNIQUE
);
`

func (s *Store) CreateTelegramPairing(ctx context.Context, accountID string, codeHash [32]byte, expiresAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO telegram_pairings (account_id, code_hash, expires_at, claim_id)
		VALUES (?, ?, ?, NULL)
		ON CONFLICT(account_id) DO UPDATE SET code_hash=excluded.code_hash, expires_at=excluded.expires_at, claim_id=NULL
		WHERE telegram_pairings.claim_id IS NULL
	`, accountID, codeHash[:], expiresAt.UTC().Unix())
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("a Telegram pairing is already being claimed")
	}
	return nil
}

func (s *Store) ClaimTelegramPairing(ctx context.Context, codeHash [32]byte, now time.Time) (string, [16]byte, bool, error) {
	var empty [16]byte
	claim := [16]byte{}
	if _, err := rand.Read(claim[:]); err != nil {
		return "", empty, false, fmt.Errorf("generate Telegram pairing claim: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", empty, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_pairings WHERE expires_at <= ?`, now.UTC().Unix()); err != nil {
		return "", empty, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE telegram_pairings SET claim_id = ? WHERE code_hash = ? AND claim_id IS NULL`, claim[:], codeHash[:])
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
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM telegram_pairings WHERE claim_id = ?`, claim[:]).Scan(&accountID); err != nil {
		return "", empty, false, err
	}
	if err := tx.Commit(); err != nil {
		return "", empty, false, err
	}
	return accountID, claim, true, nil
}

func (s *Store) FinishTelegramPairing(ctx context.Context, claimID [16]byte, success bool) error {
	var result sql.Result
	var err error
	if success {
		result, err = s.db.ExecContext(ctx, `DELETE FROM telegram_pairings WHERE claim_id = ?`, claimID[:])
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE telegram_pairings SET claim_id = NULL WHERE claim_id = ?`, claimID[:])
	}
	if err != nil {
		return err
	}
	_, err = result.RowsAffected()
	return err
}

func (s *Store) DeleteTelegramPairings(ctx context.Context, accountID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM telegram_pairings WHERE account_id = ? AND claim_id IS NULL`, accountID)
	return err
}
