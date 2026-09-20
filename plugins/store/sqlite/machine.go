package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// MachineStateVersion is the schema version of the machine-managed tables
// below: operational state, schedules, and sealed auth records. It is stored
// in schema_meta so a home opened by an older binary is refused loudly rather
// than read with the wrong shape, and so a future migration has a number to
// branch on.
//
// It started at 6 rather than 1 because it continues state.json's own schema
// series, which had reached 5 when these records moved into SQLite. Two
// numbering schemes for one body of state is exactly the ambiguity a version
// exists to remove.
//
// 7 is accounts: every private table carries an account_id, machine state is
// one row per account, and conversation resets and processed events are keyed
// by account as well as by their old key. A version-6 binary refuses a 7
// database, which is deliberate -- it would read every account's records as
// one owner's.
//
// 9 generalises Telegram pairing tokens into identity_links keyed by
// connection, so a Discord linking token and a Telegram one are the same
// record with a different connection.
//
// 10 is local accounts: credentials and single-use browser login links are
// SQLite records (account_auth, web_login_links), and every session issued
// under the retired Google Sign-In is invalidated. This one is not applied
// in place by Open: it is the offline cutover eggyd --migrate-local-login
// runs after backing up the home, because it goes together with a rewrite
// of config.yaml.
const MachineStateVersion = localAuthVersion

const machineStateVersionKey = "machine_state_version"

// machineSchema holds every machine-managed table that is not conversation
// history. Times are stored as RFC3339Nano text with the offset preserved:
// a recurring schedule's next run is computed in the owner's location, and a
// round trip through a UTC integer would silently move a "09:00 daily" job.
const machineSchema = `
CREATE TABLE IF NOT EXISTS schema_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS imports (
    name       TEXT PRIMARY KEY,
    imported_at INTEGER NOT NULL,
    records    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS machine_state (
    account_id    TEXT PRIMARY KEY,
    version       INTEGER NOT NULL,
    approval_mode TEXT    NOT NULL DEFAULT '',
    approval_auto_mode INTEGER NOT NULL DEFAULT 0,
    agent         TEXT    NOT NULL DEFAULT '{}',
    repositories  TEXT    NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS approvals (
    id         TEXT PRIMARY KEY,
    account_id TEXT NOT NULL DEFAULT '',
    record     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS processed_events (
    account_id TEXT NOT NULL DEFAULT '',
    id         TEXT NOT NULL,
    seen_at    TEXT NOT NULL,
    PRIMARY KEY (account_id, id)
);

CREATE TABLE IF NOT EXISTS proactive_messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id TEXT    NOT NULL DEFAULT '',
    sent_at    TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS schedules (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL,
    execution   TEXT NOT NULL DEFAULT '',
    instruction TEXT NOT NULL,
    expression  TEXT NOT NULL DEFAULT '',
    next_run    TEXT NOT NULL DEFAULT '',
    last_run    TEXT NOT NULL DEFAULT '',
    pending_run TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS auth_records (
    section TEXT NOT NULL,
    key     TEXT NOT NULL,
    record  TEXT NOT NULL,
    PRIMARY KEY (section, key)
);
`

// namePattern bounds every owner- or provider-supplied identifier that
// becomes a row key: schedule ids, auth sections, and auth record keys. It is
// the same bound the files these tables replaced enforced through their file
// names, kept so a migrated home cannot suddenly hold a key the old form
// would have refused.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// recordMachineStateVersion refuses a database written by a newer binary and
// stamps the current version once every in-place upgrade has run. It runs
// after those upgrades rather than before, so a crash mid-upgrade leaves the
// old stamp and the next open runs the (idempotent) upgrade again.
func recordMachineStateVersion(db *sql.DB) error {
	var stored string
	err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, machineStateVersionKey).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		_, err := db.Exec(`INSERT INTO schema_meta (key, value) VALUES (?, ?)`, machineStateVersionKey, strconv.Itoa(MachineStateVersion))
		return err
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
	if version < MachineStateVersion {
		_, err := db.Exec(`UPDATE schema_meta SET value = ? WHERE key = ?`, strconv.Itoa(MachineStateVersion), machineStateVersionKey)
		return err
	}
	return nil
}

// formatTime and parseTime are the one encoding for a stored instant. The
// zero time is the empty string rather than a sentinel date, so "never ran"
// stays distinguishable from "ran at the epoch".
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not an RFC3339 time", value)
	}
	return parsed, nil
}
