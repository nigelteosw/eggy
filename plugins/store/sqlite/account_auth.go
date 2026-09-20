package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ErrLocalLoginMigrationRequired reports a home from before local accounts
// that has not completed the offline cutover. The daemon refuses to start on
// it rather than upgrading in place, because the cutover also rewrites
// config.yaml and takes backups of both, and those steps belong to one
// explicit command with the daemon stopped.
var ErrLocalLoginMigrationRequired = errors.New("this home needs the local login migration: stop eggyd and run `eggyd --home <home> --migrate-local-login --password-account <id>`")

// localAuthVersion is the machine-state version at which credentials and
// browser login links became SQLite records.
const localAuthVersion = 10

// accountAuthSchema holds credentials and single-use browser login links.
// account_auth is a lifecycle row, not a membership directory: membership
// stays in YAML, and a row outliving its YAML entry is exactly what keeps a
// removed ID from being reissued. NOCASE on the key is what makes "Third"
// and "third" one ID.
const accountAuthSchema = `
CREATE TABLE IF NOT EXISTS account_auth (
    account_id    TEXT PRIMARY KEY COLLATE NOCASE,
    password_hash TEXT NOT NULL DEFAULT '',
    generation    INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0),
    retired       INTEGER NOT NULL DEFAULT 0 CHECK (retired IN (0, 1))
);
CREATE TABLE IF NOT EXISTS web_login_links (
    hash       TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    sender_id  TEXT NOT NULL,
    generation INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_web_login_links_account ON web_login_links(account_id);
`

// Cutover phases recorded in schema_meta while the offline migration runs.
// They live there rather than in a marker file so the database carries its
// own answer to "is this home half migrated?".
const (
	localLoginCutoverKey = "local_login_cutover"
	CutoverPrepared      = "prepared"
	CutoverDatabaseReady = "database_ready"
	CutoverComplete      = "complete"
)

// LocalLoginCutover is the durable progress marker of the offline migration:
// which phase it reached, the digest of the config it started from, and
// where it put the backups, so a rerun after interruption can verify the
// backups it finds rather than silently trusting or overwriting them.
type LocalLoginCutover struct {
	Phase          string `json:"phase"`
	ConfigDigest   string `json:"config_digest,omitempty"`
	ConfigBackup   string `json:"config_backup,omitempty"`
	DatabaseBackup string `json:"database_backup,omitempty"`
}

// LocalLoginCutover reads the marker; found is false when no cutover was
// ever started on this database.
func (s *Store) LocalLoginCutover(ctx context.Context) (LocalLoginCutover, bool, error) {
	return readLocalLoginCutover(ctx, s.db)
}

func readLocalLoginCutover(ctx context.Context, db *sql.DB) (LocalLoginCutover, bool, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key = ?`, localLoginCutoverKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return LocalLoginCutover{}, false, nil
	}
	if err != nil {
		return LocalLoginCutover{}, false, err
	}
	var marker LocalLoginCutover
	if err := json.Unmarshal([]byte(raw), &marker); err != nil {
		return LocalLoginCutover{}, false, fmt.Errorf("local login cutover marker: %w", err)
	}
	return marker, true, nil
}

// RecordLocalLoginCutover writes the marker.
func (s *Store) RecordLocalLoginCutover(ctx context.Context, marker LocalLoginCutover) error {
	switch marker.Phase {
	case CutoverPrepared, CutoverDatabaseReady, CutoverComplete:
	default:
		return fmt.Errorf("unknown cutover phase %q", marker.Phase)
	}
	raw, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO schema_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, localLoginCutoverKey, string(raw))
	return err
}

// storedMachineStateVersion returns the stamped version, or 0 for a database
// that has never been stamped.
func storedMachineStateVersion(db *sql.DB) (int, error) {
	var stored string
	err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, machineStateVersionKey).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	version, err := strconv.Atoi(stored)
	if err != nil {
		return 0, fmt.Errorf("machine state version %q is not a number", stored)
	}
	return version, nil
}

// requireLocalLoginCutover is the gate ordinary Open applies once the older
// in-place upgrades have run: a database stamped before local auth, or one
// whose cutover is still in progress, is refused until the migration command
// has finished with it. A never-stamped database is fresh and needs nothing.
func requireLocalLoginCutover(db *sql.DB) error {
	version, err := storedMachineStateVersion(db)
	if err != nil {
		return err
	}
	if version != 0 && version < localAuthVersion {
		return ErrLocalLoginMigrationRequired
	}
	marker, found, err := readLocalLoginCutover(context.Background(), db)
	if err != nil {
		return err
	}
	if found && marker.Phase != CutoverComplete {
		return fmt.Errorf("%w (cutover interrupted at %q; rerun the command to finish it)", ErrLocalLoginMigrationRequired, marker.Phase)
	}
	return nil
}

// OpenForLocalAuthMigration opens an existing database for the offline
// cutover: every older in-place upgrade runs as usual, but the local auth
// migration does not, and the version is not advanced past what the
// pre-auth upgrades reach. The command then takes backups, calls
// MigrateLocalAuth, and records the cutover phases itself.
func OpenForLocalAuthMigration(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := openDatabase(path)
	if err != nil {
		return nil, err
	}
	if err := upgradeBeforeLocalAuth(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	version, err := storedMachineStateVersion(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if version < localAuthVersion-1 {
		if _, err := db.Exec(`INSERT INTO schema_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, machineStateVersionKey, strconv.Itoa(localAuthVersion-1)); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	store := &Store{db: db, path: path}
	if err := store.tightenPrivateFiles(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// MigrateLocalAuth performs the versioned upgrade to local auth exactly
// once: it creates the credential and link tables and invalidates every
// session and login transaction issued under the retired login mechanism.
// A database already at or past the auth version is left alone, which is
// what lets an interrupted cutover be rerun without signing everybody out
// a second time.
func (s *Store) MigrateLocalAuth(ctx context.Context) error {
	version, err := storedMachineStateVersion(s.db)
	if err != nil {
		return err
	}
	if version >= localAuthVersion {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, accountAuthSchema); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return err
	}
	// The retired inbound login tables go away entirely: the identities and
	// login transactions they held have no reader any more, and the migration
	// is the one place with the version stamp to make dropping them a single
	// transaction with the auth schema it replaces them with.
	for _, drop := range []string{`DROP TABLE IF EXISTS login_transactions`, `DROP TABLE IF EXISTS identities`} {
		if _, err := tx.ExecContext(ctx, drop); err != nil && !strings.Contains(err.Error(), "no such table") {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, machineStateVersionKey, strconv.Itoa(localAuthVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// BackupBeforeLocalAuth writes a consistent copy of the database to path
// with VACUUM INTO, which is the only correct copy of a WAL-mode database
// that may have pages not yet checkpointed into the main file. It refuses
// to overwrite: a backup that already exists is evidence of an earlier
// attempt the caller has to reason about, not something to replace.
func (s *Store) BackupBeforeLocalAuth(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("backup %s already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.ContainsAny(path, "'") {
		return errors.New("backup path may not contain a quote")
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO '`+path+`'`); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return VerifyDatabaseBackup(ctx, path)
}

// VerifyDatabaseBackup opens a backup read-only and checks that it is intact
// and was taken before local auth, so a rerun after interruption can trust
// what it finds beside the home.
func VerifyDatabaseBackup(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var check string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil {
		return fmt.Errorf("backup %s: %w", path, err)
	}
	if check != "ok" {
		return fmt.Errorf("backup %s failed integrity check: %s", path, check)
	}
	version, err := storedMachineStateVersion(db)
	if err != nil {
		return fmt.Errorf("backup %s: %w", path, err)
	}
	if version >= localAuthVersion {
		return fmt.Errorf("backup %s was taken after the local login migration", path)
	}
	return nil
}

func normalizeAccountID(accountID string) (string, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", errors.New("account id is required")
	}
	return accountID, nil
}

// RegisterAccountAuth inserts the lifecycle row for a new account. Any
// existing row under the same case-insensitive ID -- live, pending, or
// retired -- is ErrAccountIDUsed.
func (s *Store) RegisterAccountAuth(ctx context.Context, accountID string) error {
	accountID, err := normalizeAccountID(accountID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO account_auth (account_id, password_hash, generation, retired) VALUES (?, '', 1, 0)`, accountID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ports.ErrAccountIDUsed
	}
	return err
}

// AccountAuth reads one account's row. An absent row is ErrAuthDenied, the
// same answer as every other way of not being allowed in.
func (s *Store) AccountAuth(ctx context.Context, accountID string) (ports.AccountAuth, error) {
	accountID, err := normalizeAccountID(accountID)
	if err != nil {
		return ports.AccountAuth{}, err
	}
	var record ports.AccountAuth
	var retired int
	err = s.db.QueryRowContext(ctx, `SELECT account_id, password_hash, generation, retired FROM account_auth WHERE account_id = ?`, accountID).
		Scan(&record.AccountID, &record.PasswordHash, &record.Generation, &retired)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.AccountAuth{}, ports.ErrAuthDenied
	}
	if err != nil {
		return ports.AccountAuth{}, err
	}
	record.Retired = retired == 1
	return record, nil
}

// AccountAuthRecords lists every row, for startup reconciliation against
// YAML membership. The result stays server-side.
func (s *Store) AccountAuthRecords(ctx context.Context) ([]ports.AccountAuth, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, password_hash, generation, retired FROM account_auth ORDER BY account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []ports.AccountAuth
	for rows.Next() {
		var record ports.AccountAuth
		var retired int
		if err := rows.Scan(&record.AccountID, &record.PasswordHash, &record.Generation, &retired); err != nil {
			return nil, err
		}
		record.Retired = retired == 1
		records = append(records, record)
	}
	return records, rows.Err()
}

// SetAccountPassword replaces the hash and advances the generation, then
// drops every session and link issued under the old one, all conditional on
// the row still being at expectedGeneration and live.
func (s *Store) SetAccountPassword(ctx context.Context, accountID, encoded string, expectedGeneration int64) error {
	accountID, err := normalizeAccountID(accountID)
	if err != nil {
		return err
	}
	if encoded == "" {
		return errors.New("password hash is required")
	}
	return s.advanceGeneration(ctx, accountID, `UPDATE account_auth SET password_hash = ?, generation = generation + 1 WHERE account_id = ? AND generation = ? AND retired = 0`, encoded, accountID, expectedGeneration)
}

// RevokeAccountAuth advances the generation without changing the password:
// every session and link is invalidated, the password still works.
func (s *Store) RevokeAccountAuth(ctx context.Context, accountID string) error {
	accountID, err := normalizeAccountID(accountID)
	if err != nil {
		return err
	}
	return s.advanceGeneration(ctx, accountID, `UPDATE account_auth SET generation = generation + 1 WHERE account_id = ? AND retired = 0`, accountID)
}

// RetireAccountAuth ends the account for good: hash cleared, retired set,
// generation advanced, sessions and links gone. Retiring an ID with no row
// or one already retired is not an error; the caller asked for it to be
// unusable, and it is.
func (s *Store) RetireAccountAuth(ctx context.Context, accountID string) error {
	accountID, err := normalizeAccountID(accountID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE account_auth SET password_hash = '', generation = generation + 1, retired = 1 WHERE account_id = ?`, accountID); err != nil {
		return err
	}
	if err := revokeIssuedTx(ctx, tx, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) advanceGeneration(ctx context.Context, accountID, update string, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, update, args...)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ports.ErrAuthDenied
	}
	if err := revokeIssuedTx(ctx, tx, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

// revokeIssuedTx deletes the account's sessions and links. Sessions match
// the stored ID exactly, links the same; both were written from the row's
// own spelling, so no collation gymnastics are needed here.
func revokeIssuedTx(ctx context.Context, tx *sql.Tx, accountID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE account_id IN (SELECT account_id FROM account_auth WHERE account_id = ?)`, accountID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM web_login_links WHERE account_id IN (SELECT account_id FROM account_auth WHERE account_id = ?)`, accountID)
	return err
}

// CreateAuthenticatedSession issues a session for the account only while it
// is still at the generation the caller verified a credential against. The
// insert selects from account_auth, so the check and the write are one
// statement and a reset in between yields zero rows.
func (s *Store) CreateAuthenticatedSession(ctx context.Context, hash, accountID string, generation int64, expiresAt time.Time) error {
	accountID, err := normalizeAccountID(accountID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.pruneAuth(ctx, now); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertAuthenticatedSessionTx(ctx, tx, hash, accountID, generation, now, expiresAt); err != nil {
		return err
	}
	return tx.Commit()
}

func insertAuthenticatedSessionTx(ctx context.Context, tx *sql.Tx, hash, accountID string, generation int64, now, expiresAt time.Time) error {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (hash, account_id, created_at, expires_at)
		SELECT ?, account_id, ?, ? FROM account_auth
		WHERE account_id = ? AND generation = ? AND retired = 0`,
		hash, formatTime(now), formatTime(expiresAt.UTC()), accountID, generation)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ports.ErrAuthDenied
	}
	return nil
}

// CreateWebLoginLink stores one link for the account, replacing any older
// outstanding one, only while the account is still at link.Generation.
// Expired rows are pruned on the way.
func (s *Store) CreateWebLoginLink(ctx context.Context, hash string, link ports.WebLoginLink, now time.Time) error {
	accountID, err := normalizeAccountID(link.AccountID)
	if err != nil {
		return err
	}
	if hash == "" || strings.TrimSpace(link.SenderID) == "" || !link.ExpiresAt.After(now) {
		return errors.New("link hash, sender and future expiry are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM web_login_links WHERE expires_at <= ?`, now.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM web_login_links WHERE account_id IN (SELECT account_id FROM account_auth WHERE account_id = ?)`, accountID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO web_login_links (hash, account_id, sender_id, generation, expires_at)
		SELECT ?, account_id, ?, ?, ? FROM account_auth
		WHERE account_id = ? AND generation = ? AND retired = 0`,
		hash, link.SenderID, link.Generation, link.ExpiresAt.UnixMilli(), accountID, link.Generation)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ports.ErrAuthDenied
	}
	return tx.Commit()
}

// WebLoginLink returns an unexpired link's metadata; the raw token is never
// stored, so there is nothing here that could be replayed.
func (s *Store) WebLoginLink(ctx context.Context, hash string, now time.Time) (ports.WebLoginLink, error) {
	var link ports.WebLoginLink
	var expires int64
	err := s.db.QueryRowContext(ctx, `SELECT account_id, sender_id, generation, expires_at FROM web_login_links WHERE hash = ? AND expires_at > ?`, hash, now.UnixMilli()).
		Scan(&link.AccountID, &link.SenderID, &link.Generation, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.WebLoginLink{}, ports.ErrAuthDenied
	}
	if err != nil {
		return ports.WebLoginLink{}, err
	}
	link.ExpiresAt = time.UnixMilli(expires).UTC()
	return link, nil
}

// RedeemWebLoginLink consumes the link and issues the session in one
// transaction. The delete is the read: it matches hash, account, sender,
// generation, and expiry, so a link minted before a reset or for a sender
// since reassigned deletes nothing and denies. The session insert is the
// same generation-conditional statement as a password login, and a failure
// there rolls the consumption back so the link is still there for a retry.
func (s *Store) RedeemWebLoginLink(ctx context.Context, linkHash, sessionHash string, expected ports.WebLoginLink, now, sessionExpiry time.Time) error {
	accountID, err := normalizeAccountID(expected.AccountID)
	if err != nil {
		return err
	}
	if linkHash == "" || sessionHash == "" {
		return errors.New("link and session hashes are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `DELETE FROM web_login_links WHERE hash = ? AND account_id = ? AND sender_id = ? AND generation = ? AND expires_at > ?`,
		linkHash, accountID, expected.SenderID, expected.Generation, now.UnixMilli())
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ports.ErrAuthDenied
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM web_login_links WHERE expires_at <= ?`, now.UnixMilli()); err != nil {
		return err
	}
	if err := insertAuthenticatedSessionTx(ctx, tx, sessionHash, accountID, expected.Generation, now.UTC(), sessionExpiry); err != nil {
		return err
	}
	return tx.Commit()
}
