package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestPasswordResetRejectsPreviouslyVerifiedGeneration(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	if err := db.RegisterAccountAuth(ctx, "partner"); err != nil {
		t.Fatal(err)
	}
	old, err := db.AccountAuth(ctx, "partner")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAccountPassword(ctx, "partner", "encoded-new", old.Generation); err != nil {
		t.Fatal(err)
	}
	err = db.CreateAuthenticatedSession(ctx, "stale-session", "partner", old.Generation, time.Now().Add(time.Hour))
	if !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("stale login: %v", err)
	}
	current, err := db.AccountAuth(ctx, "partner")
	if err != nil || current.Generation != old.Generation+1 || current.PasswordHash != "encoded-new" {
		t.Fatalf("after reset: %+v err=%v", current, err)
	}
	if err := db.CreateAuthenticatedSession(ctx, "fresh-session", "partner", current.Generation, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("current generation login: %v", err)
	}
	if account, err := db.SessionAccount(ctx, "fresh-session", time.Now()); err != nil || account != "partner" {
		t.Fatalf("session account=%q err=%v", account, err)
	}
	// A second reset against the already-consumed generation is refused.
	if err := db.SetAccountPassword(ctx, "partner", "encoded-newer", old.Generation); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("stale reset: %v", err)
	}
}

func TestRetiredIDCannotRegister(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	if err := db.RegisterAccountAuth(ctx, "Third"); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterAccountAuth(ctx, "third"); !errors.Is(err, ports.ErrAccountIDUsed) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	auth, err := db.AccountAuth(ctx, "Third")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAccountPassword(ctx, "Third", "encoded", auth.Generation); err != nil {
		t.Fatal(err)
	}
	if err := db.RetireAccountAuth(ctx, "Third"); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterAccountAuth(ctx, "THIRD"); !errors.Is(err, ports.ErrAccountIDUsed) {
		t.Fatalf("retired id re-registered: %v", err)
	}
	retired, err := db.AccountAuth(ctx, "Third")
	if err != nil || !retired.Retired || retired.PasswordHash != "" {
		t.Fatalf("retired row: %+v err=%v", retired, err)
	}
	if err := db.CreateAuthenticatedSession(ctx, "s", "Third", retired.Generation, time.Now().Add(time.Hour)); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("retired login: %v", err)
	}
	if err := db.SetAccountPassword(ctx, "Third", "encoded-again", retired.Generation); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("retired reset: %v", err)
	}
	if _, err := db.AccountAuth(ctx, "nobody"); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("unknown account: %v", err)
	}
	// Retiring an unknown ID is a no-op rather than an error.
	if err := db.RetireAccountAuth(ctx, "nobody"); err != nil {
		t.Fatal(err)
	}
}

func TestResetDeletesOnlyTargetSessionsAndLinks(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []string{"a", "b"} {
		if err := db.RegisterAccountAuth(ctx, id); err != nil {
			t.Fatal(err)
		}
		if err := db.CreateAuthenticatedSession(ctx, "session-"+id, id, 1, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := db.CreateWebLoginLink(ctx, "link-"+id, ports.WebLoginLink{AccountID: id, SenderID: "1", Generation: 1, ExpiresAt: now.Add(5 * time.Minute)}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RevokeAccountAuth(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionAccount(ctx, "session-a", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoked session: %v", err)
	}
	if _, err := db.WebLoginLink(ctx, "link-a", now); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("revoked link: %v", err)
	}
	if account, err := db.SessionAccount(ctx, "session-b", now); err != nil || account != "b" {
		t.Fatalf("bystander session account=%q err=%v", account, err)
	}
	if link, err := db.WebLoginLink(ctx, "link-b", now); err != nil || link.AccountID != "b" || link.SenderID != "1" {
		t.Fatalf("bystander link=%+v err=%v", link, err)
	}
	a, err := db.AccountAuth(ctx, "a")
	if err != nil || a.Generation != 2 {
		t.Fatalf("a after revoke: %+v err=%v", a, err)
	}
	b, err := db.AccountAuth(ctx, "b")
	if err != nil || b.Generation != 1 {
		t.Fatalf("b after revoke: %+v err=%v", b, err)
	}
	// Minting against a stale generation is refused; a fresh mint replaces
	// the account's outstanding link.
	if err := db.CreateWebLoginLink(ctx, "link-a2", ports.WebLoginLink{AccountID: "a", SenderID: "1", Generation: 1, ExpiresAt: now.Add(time.Minute)}, now); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("stale mint: %v", err)
	}
	if err := db.CreateWebLoginLink(ctx, "link-b2", ports.WebLoginLink{AccountID: "b", SenderID: "1", Generation: 1, ExpiresAt: now.Add(time.Minute)}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.WebLoginLink(ctx, "link-b", now); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("older link survived a new mint: %v", err)
	}
	if _, err := db.WebLoginLink(ctx, "link-b2", now.Add(2*time.Minute)); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("expired link readable: %v", err)
	}
}

func TestLinkRedeemConcurrentSingleWinner(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.RegisterAccountAuth(ctx, "partner"); err != nil {
		t.Fatal(err)
	}
	link := ports.WebLoginLink{AccountID: "partner", SenderID: "123", Generation: 1, ExpiresAt: now.Add(5 * time.Minute)}
	if err := db.CreateWebLoginLink(ctx, "link", link, now); err != nil {
		t.Fatal(err)
	}
	const racers = 4
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, racers)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = db.RedeemWebLoginLink(ctx, "link", "session-"+strconv.Itoa(i), link, now, now.Add(time.Hour))
		}()
	}
	close(start)
	wg.Wait()
	winners := 0
	for i, err := range results {
		switch {
		case err == nil:
			winners++
			if account, err := db.SessionAccount(ctx, "session-"+strconv.Itoa(i), now); err != nil || account != "partner" {
				t.Fatalf("winner session account=%q err=%v", account, err)
			}
		case errors.Is(err, ports.ErrAuthDenied):
			if _, err := db.SessionAccount(ctx, "session-"+strconv.Itoa(i), now); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("loser got a session: %v", err)
			}
		default:
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
}

func TestLinkRedeemRequiresExactMetadata(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.RegisterAccountAuth(ctx, "partner"); err != nil {
		t.Fatal(err)
	}
	link := ports.WebLoginLink{AccountID: "partner", SenderID: "123", Generation: 1, ExpiresAt: now.Add(5 * time.Minute)}
	if err := db.CreateWebLoginLink(ctx, "link", link, now); err != nil {
		t.Fatal(err)
	}
	wrongSender := link
	wrongSender.SenderID = "456"
	if err := db.RedeemWebLoginLink(ctx, "link", "s1", wrongSender, now, now.Add(time.Hour)); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("reassigned sender: %v", err)
	}
	if err := db.RedeemWebLoginLink(ctx, "link", "s1", link, now.Add(10*time.Minute), now.Add(time.Hour)); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("expired: %v", err)
	}
	if err := db.RevokeAccountAuth(ctx, "partner"); err != nil {
		t.Fatal(err)
	}
	if err := db.RedeemWebLoginLink(ctx, "link", "s1", link, now, now.Add(time.Hour)); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("revoked generation: %v", err)
	}
	if _, err := db.SessionAccount(ctx, "s1", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("session issued on refusal: %v", err)
	}
}

func TestLinkInsertFailureRollsBackConsumption(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.RegisterAccountAuth(ctx, "partner"); err != nil {
		t.Fatal(err)
	}
	link := ports.WebLoginLink{AccountID: "partner", SenderID: "123", Generation: 1, ExpiresAt: now.Add(5 * time.Minute)}
	if err := db.CreateWebLoginLink(ctx, "link", link, now); err != nil {
		t.Fatal(err)
	}
	// Force the session insert to fail on its primary key.
	if err := db.CreateAuthenticatedSession(ctx, "taken", "partner", 1, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.RedeemWebLoginLink(ctx, "link", "taken", link, now, now.Add(time.Hour)); err == nil || errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("colliding session insert: %v", err)
	}
	if _, err := db.WebLoginLink(ctx, "link", now); err != nil {
		t.Fatalf("link consumed by a rolled-back redemption: %v", err)
	}
	if err := db.RedeemWebLoginLink(ctx, "link", "fresh", link, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("redeem after rollback: %v", err)
	}
	if _, err := db.WebLoginLink(ctx, "link", now); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("link survived redemption: %v", err)
	}
}

// writeVersion9Database builds the shape the previous release left behind:
// versioned at 9, with a live session, a Google identity, a pending login
// transaction, a private trace, and a sealed outbound grant.
func writeVersion9Database(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eggy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{schema, machineSchema, sessionSchema, identityLinkSchema,
		`CREATE TABLE identities (account_id TEXT PRIMARY KEY, issuer TEXT NOT NULL, subject TEXT NOT NULL, UNIQUE (issuer, subject))`,
		`CREATE TABLE IF NOT EXISTS login_transactions (state_hash TEXT PRIMARY KEY, browser_hash TEXT NOT NULL, nonce TEXT NOT NULL, verifier TEXT NOT NULL, expires_at TEXT NOT NULL)`,
		`INSERT INTO schema_meta VALUES ('machine_state_version', '9')`,
		`INSERT INTO sessions VALUES ('old-session', 'nigel', '2026-09-01T00:00:00Z', '2099-01-01T00:00:00Z')`,
		`INSERT INTO login_transactions VALUES ('st', 'br', 'n', 'v', '2099-01-01T00:00:00Z')`,
		`INSERT INTO identities VALUES ('nigel', 'https://accounts.google.com', 'sub-1')`,
		`INSERT INTO traces VALUES ('tr1', 'nigel', 'owner', '', 'telegram', 'telegram', 'owner', 'm', '', 'private prompt', 'reply', '', '{}', 10, 5, 1)`,
		`INSERT INTO auth_records VALUES ('google', 'workspace', 'sealed-grant')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return path
}

func TestOpenRefusesVersionNineUntilCutover(t *testing.T) {
	path := writeVersion9Database(t)
	if _, err := Open(path); err == nil || !errors.Is(err, ErrLocalLoginMigrationRequired) {
		t.Fatalf("version 9 opened without cutover: %v", err)
	}
	store, err := OpenForLocalAuthMigration(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RecordLocalLoginCutover(ctx, LocalLoginCutover{Phase: CutoverPrepared, ConfigDigest: "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil || !errors.Is(err, ErrLocalLoginMigrationRequired) {
		t.Fatalf("prepared cutover opened: %v", err)
	}
	store, err = OpenForLocalAuthMigration(path)
	if err != nil {
		t.Fatal(err)
	}
	marker, found, err := store.LocalLoginCutover(ctx)
	if err != nil || !found || marker.Phase != CutoverPrepared || marker.ConfigDigest != "abc" {
		t.Fatalf("marker=%+v found=%v err=%v", marker, found, err)
	}
	if err := store.MigrateLocalAuth(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLocalLoginCutover(ctx, LocalLoginCutover{Phase: CutoverDatabaseReady, ConfigDigest: "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil || !errors.Is(err, ErrLocalLoginMigrationRequired) {
		t.Fatalf("database_ready opened before the config write: %v", err)
	}
	store, err = OpenForLocalAuthMigration(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLocalLoginCutover(ctx, LocalLoginCutover{Phase: CutoverComplete, ConfigDigest: "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatalf("completed cutover refused: %v", err)
	}
	defer store.Close()
	var grant string
	if err := store.db.QueryRow(`SELECT record FROM auth_records WHERE section = 'google'`).Scan(&grant); err != nil || grant != "sealed-grant" {
		t.Fatalf("grant=%q err=%v", grant, err)
	}
}

func TestLocalAuthMigrationRunsOnce(t *testing.T) {
	path := writeVersion9Database(t)
	ctx := context.Background()
	now := time.Now().UTC()
	store, err := OpenForLocalAuthMigration(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`SELECT 1 FROM account_auth`); err == nil {
		t.Fatal("auth tables created before the migration ran")
	}
	if account, err := store.SessionAccount(ctx, "old-session", now); err != nil || account != "nigel" {
		t.Fatalf("pre-migration session account=%q err=%v", account, err)
	}
	for range 2 {
		if err := store.MigrateLocalAuth(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SessionAccount(ctx, "old-session", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("obsolete session survived: %v", err)
	}
	if err := store.RegisterAccountAuth(ctx, "nigel"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAuthenticatedSession(ctx, "new-session", "nigel", 1, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLocalLoginCutover(ctx, LocalLoginCutover{Phase: CutoverComplete}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if account, err := store.SessionAccount(ctx, "new-session", now); err != nil || account != "nigel" {
			t.Fatalf("fresh session invalidated on reopen: account=%q err=%v", account, err)
		}
		var version string
		if err := store.db.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, machineStateVersionKey).Scan(&version); err != nil || version != strconv.Itoa(MachineStateVersion) {
			t.Fatalf("version=%q err=%v", version, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupBeforeLocalAuth(t *testing.T) {
	path := writeVersion9Database(t)
	ctx := context.Background()
	store, err := OpenForLocalAuthMigration(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backup := filepath.Join(filepath.Dir(path), "eggy.db.pre-local-login")
	if err := store.BackupBeforeLocalAuth(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := store.BackupBeforeLocalAuth(ctx, backup); err == nil {
		t.Fatal("backup overwritten")
	}
	info, err := os.Stat(backup)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode=%v err=%v", info.Mode(), err)
	}
	copyDB, err := sql.Open("sqlite", backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var version, input, grant string
	if err := copyDB.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, machineStateVersionKey).Scan(&version); err != nil || version != "9" {
		t.Fatalf("backup version=%q err=%v", version, err)
	}
	if err := copyDB.QueryRow(`SELECT input FROM traces WHERE id = 'tr1'`).Scan(&input); err != nil || input != "private prompt" {
		t.Fatalf("backup trace=%q err=%v", input, err)
	}
	if err := copyDB.QueryRow(`SELECT record FROM auth_records WHERE section = 'google'`).Scan(&grant); err != nil || grant != "sealed-grant" {
		t.Fatalf("backup grant=%q err=%v", grant, err)
	}
	if err := VerifyDatabaseBackup(ctx, backup); err != nil {
		t.Fatalf("verify backup: %v", err)
	}
}

func TestTooNewDatabaseIsNotMigrated(t *testing.T) {
	path := writeVersion9Database(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE schema_meta SET value = ? WHERE key = ?`, strconv.Itoa(MachineStateVersion+1), machineStateVersionKey); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenForLocalAuthMigration(path); err == nil {
		t.Fatal("too-new database opened for migration")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT 1 FROM sessions WHERE hash = 'old-session'`).Scan(new(int)); err != nil {
		t.Fatalf("too-new database changed: %v", err)
	}
}
