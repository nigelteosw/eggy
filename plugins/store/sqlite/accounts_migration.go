package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// legacyAccountKey is the schema_meta entry recording which account received
// the records written before accounts existed. It is the identity of that
// import forever after: a later boot naming a different account is a
// conflicting instruction, not a second migration.
const legacyAccountKey = "legacy_account"

// privateTables is every table whose rows belong to one account. The auth
// records are deliberately absent: the Google and MCP grants are shared, and
// the upgrade must not touch their ciphertext or keys.
var privateTables = []string{"messages", "threads", "conversation_resets", "traces", "machine_state", "approvals", "processed_events", "proactive_messages", "schedules"}

// refuseNewerMachineState is the half of the version check that must run
// before any upgrade: a database a newer binary wrote is refused outright.
func refuseNewerMachineState(db *sql.DB) error {
	var stored string
	err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, machineStateVersionKey).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	version, err := strconv.Atoi(stored)
	if err != nil {
		return fmt.Errorf("machine state version %q is not a number", stored)
	}
	if version > MachineStateVersion {
		return fmt.Errorf("home was written by a newer Eggy: machine state version %d, this build understands %d", version, MachineStateVersion)
	}
	return nil
}

// upgradeToAccounts gives every private table an account_id in place. It is
// driven by the tables' actual shape rather than by the stored version, so a
// database from before machine state was versioned at all is upgraded by the
// same path as a version-6 one, and a crash halfway is repaired by running
// it again: every step is a no-op once its column exists.
//
// Rows gain account_id ” -- unowned -- and stay unreadable until
// MigrateAccounts names their owner. The tables whose primary key has to
// widen (a reset per account per conversation, a dedup entry per account per
// event) are rebuilt and their rows copied across; the rest take a column.
func upgradeToAccounts(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range []string{"messages", "threads", "traces", "approvals", "schedules"} {
		if err := ensureColumnTx(tx, table, "account_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("upgrade %s: %w", table, err)
		}
	}
	rebuilt := []struct{ table, create, copy string }{
		{"conversation_resets",
			`CREATE TABLE conversation_resets_v7 (account_id TEXT NOT NULL DEFAULT '', conversation_id TEXT NOT NULL, cleared_at INTEGER NOT NULL, PRIMARY KEY (account_id, conversation_id))`,
			`INSERT INTO conversation_resets_v7 (account_id, conversation_id, cleared_at) SELECT '', conversation_id, cleared_at FROM conversation_resets`},
		{"processed_events",
			`CREATE TABLE processed_events_v7 (account_id TEXT NOT NULL DEFAULT '', id TEXT NOT NULL, seen_at TEXT NOT NULL, PRIMARY KEY (account_id, id))`,
			`INSERT INTO processed_events_v7 (account_id, id, seen_at) SELECT '', id, seen_at FROM processed_events`},
		{"proactive_messages",
			`CREATE TABLE proactive_messages_v7 (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id TEXT NOT NULL DEFAULT '', sent_at TEXT NOT NULL)`,
			`INSERT INTO proactive_messages_v7 (id, account_id, sent_at) SELECT id, '', sent_at FROM proactive_messages`},
		{"machine_state",
			`CREATE TABLE machine_state_v7 (account_id TEXT PRIMARY KEY, version INTEGER NOT NULL, approval_mode TEXT NOT NULL DEFAULT '', approval_auto_mode INTEGER NOT NULL DEFAULT 0, agent TEXT NOT NULL DEFAULT '{}', repositories TEXT NOT NULL DEFAULT '{}')`,
			`INSERT INTO machine_state_v7 (account_id, version, approval_mode, approval_auto_mode, agent, repositories) SELECT '', version, approval_mode, approval_auto_mode, agent, repositories FROM machine_state`},
	}
	for _, item := range rebuilt {
		has, err := hasColumnTx(tx, item.table, "account_id")
		if err != nil {
			return err
		}
		if has {
			continue
		}
		for _, statement := range []string{item.create, item.copy, `DROP TABLE ` + item.table, `ALTER TABLE ` + item.table + `_v7 RENAME TO ` + item.table} {
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("rebuild %s: %w", item.table, err)
			}
		}
	}
	// Indexes are created here rather than in the CREATE TABLE schema block
	// because on an old database that block runs before the column exists.
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS idx_messages_account_conversation ON messages(account_id, conversation_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_threads_account_channel_updated_at ON threads(account_id, channel, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_traces_account_started_at ON traces(account_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_approvals_account ON approvals(account_id)`,
		`CREATE INDEX IF NOT EXISTS idx_proactive_messages_account ON proactive_messages(account_id, id)`,
		`DROP INDEX IF EXISTS idx_messages_conversation_id`,
		`DROP INDEX IF EXISTS idx_threads_channel_updated_at`,
		`DROP INDEX IF EXISTS idx_traces_started_at`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// HasUnownedRecords reports whether any private row still carries no
// account: records written before accounts existed that MigrateAccounts has
// not yet assigned. Bootstrap asks so it can refuse to serve traffic over a
// home whose history has no owner yet.
func (s *Store) HasUnownedRecords(ctx context.Context) bool {
	for _, table := range privateTables {
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE account_id = '' LIMIT 1`).Scan(&one)
		if err == nil {
			return true
		}
	}
	return false
}

// MigrateAccounts assigns every unowned private record to accountID and
// records that mapping. It is the one explicit step in the accounts upgrade:
// the schema upgrade leaves rows unowned, and nothing reads an unowned row,
// so history is invisible until whoever operates the deployment has said whose
// it is.
//
// Run again with the same account it does nothing; with a different one it
// refuses, because the first mapping already moved the rows and the second
// would be a silent transfer. A database that never had unowned rows records
// no mapping at all.
//
// invalidatePending drops the pending approvals that came across. A legacy
// approval was requested before there was an account or an integration
// generation to bind it to; when the deployment is being converted to
// accounts the person who asked may no longer be the one who would answer, so
// it is discarded and has to be requested again. A single owner keeping the
// legacy shape keeps their approvals.
func (s *Store) MigrateAccounts(ctx context.Context, accountID string, invalidatePending bool) error {
	if !namePattern.MatchString(accountID) {
		return fmt.Errorf("invalid migration account id %q", accountID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var recorded string
	switch err := tx.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key = ?`, legacyAccountKey).Scan(&recorded); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case recorded != accountID:
		return fmt.Errorf("legacy records were already assigned to account %q; they cannot be reassigned to %q", recorded, accountID)
	}
	moved := int64(0)
	for _, table := range privateTables {
		var statement string
		switch {
		case table == "approvals" && invalidatePending:
			statement = `DELETE FROM approvals WHERE account_id = ''`
		case table == "machine_state":
			// A state row for the account can only pre-exist on a retry that
			// died between the commit and the marker, in which case it is
			// the migrated row itself; the unowned copy is the stale one.
			if _, err := tx.ExecContext(ctx, `DELETE FROM machine_state WHERE account_id = '' AND EXISTS (SELECT 1 FROM machine_state WHERE account_id = ?)`, accountID); err != nil {
				return err
			}
			statement = `UPDATE machine_state SET account_id = ? WHERE account_id = ''`
		default:
			statement = `UPDATE ` + table + ` SET account_id = ? WHERE account_id = ''`
		}
		var result sql.Result
		if table == "approvals" && invalidatePending {
			result, err = tx.ExecContext(ctx, statement)
		} else {
			result, err = tx.ExecContext(ctx, statement, accountID)
		}
		if err != nil {
			return fmt.Errorf("assign %s: %w", table, err)
		}
		affected, _ := result.RowsAffected()
		moved += affected
	}
	if moved > 0 && recorded == "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_meta (key, value) VALUES (?, ?)`, legacyAccountKey, accountID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LegacyAccount reports which account received the pre-accounts records, or
// "" when no such records ever existed.
func (s *Store) LegacyAccount(ctx context.Context) (string, error) {
	var recorded string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key = ?`, legacyAccountKey).Scan(&recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return recorded, err
}

// execer is the write half shared by *sql.DB and *sql.Tx.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

func hasColumnTx(db execer, table, column string) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func ensureColumnTx(db execer, table, column, columnType string) error {
	has, err := hasColumnTx(db, table, column)
	if err != nil || has {
		return err
	}
	// table and column are package-local literals, never user input.
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + columnType)
	return err
}
