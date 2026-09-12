package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrIdentityBound reports a binding that would collide: the account already
// has an identity, or the identity already belongs to an account. Both are
// refused the same way because both mean "this is not a first login".
var ErrIdentityBound = errors.New("identity already bound")

// identitySchema is one row per enrolled account: the issuer/subject pair
// the provider promises is stable for a person. The email an account is
// configured with only decides who may enroll; after that, this row is what
// identifies them, which is why a changed address cannot re-enroll someone
// else without an explicit reset.
const identitySchema = `
CREATE TABLE IF NOT EXISTS identities (
    account_id TEXT PRIMARY KEY,
    issuer     TEXT NOT NULL,
    subject    TEXT NOT NULL,
    UNIQUE (issuer, subject)
);
`

// BindIdentity records accountID's verified identity. It succeeds only for
// an unbound account and an unbound identity; the two UNIQUE constraints are
// what make a concurrent first login safe, since the second insert fails
// inside SQLite rather than in a check that raced.
func (s *Store) BindIdentity(ctx context.Context, accountID, issuer, subject string) error {
	if !namePattern.MatchString(accountID) || issuer == "" || subject == "" {
		return errors.New("account id, issuer and subject are required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO identities (account_id, issuer, subject) VALUES (?, ?, ?)`, accountID, issuer, subject)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrIdentityBound
	}
	return err
}

// IdentityOf returns the account's bound identity, if any.
func (s *Store) IdentityOf(ctx context.Context, accountID string) (issuer, subject string, bound bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT issuer, subject FROM identities WHERE account_id = ?`, accountID).Scan(&issuer, &subject)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return issuer, subject, true, nil
}

// AccountForIdentity resolves an identity to its account, for logins after
// the first: the binding, not the address, is what says who this is.
func (s *Store) AccountForIdentity(ctx context.Context, issuer, subject string) (string, bool, error) {
	var accountID string
	err := s.db.QueryRowContext(ctx, `SELECT account_id FROM identities WHERE issuer = ? AND subject = ?`, issuer, subject).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return accountID, true, nil
}

// ResetIdentity unbinds the account and ends every session it has: the
// person is signed out and must enroll again with the configured address.
// Done in one transaction so there is no moment where a session lives on
// for an identity that no longer binds.
func (s *Store) ResetIdentity(ctx context.Context, accountID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM identities WHERE account_id = ?`, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE account_id = ?`, accountID); err != nil {
		return err
	}
	return tx.Commit()
}
