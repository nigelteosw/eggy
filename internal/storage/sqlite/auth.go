package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/nigelteosw/eggy/internal/auth/grants"
)

// AuthRecords is the grants.Records container: one row per provider record,
// each already sealed by the caller's grants.Sealer. Nothing here can read a
// record's contents, which is the point -- moving the container from
// auth.json into the database changed where ciphertext is kept and nothing
// about what protects it, including the associated data binding each record
// to the identity it was stored under.
type AuthRecords struct{ db *sql.DB }

func (s *Store) Auth() *AuthRecords { return &AuthRecords{db: s.db} }

func (a *AuthRecords) Read(section, key string) (json.RawMessage, error) {
	if err := validateAuthName(section, key); err != nil {
		return nil, err
	}
	var record string
	err := a.db.QueryRow(`SELECT record FROM auth_records WHERE section = ? AND key = ?`, section, key).Scan(&record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, grants.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(record), nil
}

func (a *AuthRecords) Write(section, key string, record json.RawMessage) error {
	return a.Update(section, key, func(json.RawMessage) (json.RawMessage, error) { return record, nil })
}

// Update reads, mutates, and writes one record inside a transaction, so a
// refresh that races a login cannot lose the token it just stored. A mutate
// returning nil deletes the record, which is how Delete is expressed.
func (a *AuthRecords) Update(section, key string, mutate func(json.RawMessage) (json.RawMessage, error)) error {
	if err := validateAuthName(section, key); err != nil {
		return err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var stored json.RawMessage
	var record string
	switch err := tx.QueryRow(`SELECT record FROM auth_records WHERE section = ? AND key = ?`, section, key).Scan(&record); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		stored = json.RawMessage(record)
	}
	updated, err := mutate(stored)
	if err != nil {
		return err
	}
	if updated == nil {
		if _, err := tx.Exec(`DELETE FROM auth_records WHERE section = ? AND key = ?`, section, key); err != nil {
			return err
		}
		return tx.Commit()
	}
	if _, err := tx.Exec(`
		INSERT INTO auth_records (section, key, record) VALUES (?, ?, ?)
		ON CONFLICT(section, key) DO UPDATE SET record = excluded.record`,
		section, key, string(updated)); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *AuthRecords) Delete(section, key string) error {
	return a.Update(section, key, func(json.RawMessage) (json.RawMessage, error) { return nil, nil })
}

func validateAuthName(section, key string) error {
	if !namePattern.MatchString(section) || !namePattern.MatchString(key) {
		return errors.New("invalid auth record name")
	}
	return nil
}
