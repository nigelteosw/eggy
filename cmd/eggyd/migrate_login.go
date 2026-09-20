package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/ports"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

// The offline cutover from Google Sign-In to local accounts. It runs with
// the daemon stopped, backs up config.yaml and eggy.db, upgrades the
// database, rewrites the config, and exits without starting Eggy. Progress
// is recorded in the database's own schema_meta so an interrupted run
// resumes where it stopped: it never takes a second backup over the first,
// never clears sessions twice, and never re-migrates a config it already
// wrote.
//
// This file orchestrates; the migrations themselves live in internal/config
// and plugins/store/sqlite, and nothing here writes YAML or SQL directly.

const localLoginBackupSuffix = ".pre-local-login"

// migrationHooks lets tests interrupt the cutover after each stage, standing
// in for a crash or a lost connection at exactly that point.
type migrationHooks struct {
	afterBackup   func() error
	afterDatabase func() error
	afterConfig   func() error
}

func (h migrationHooks) run(hook func() error) error {
	if hook == nil {
		return nil
	}
	return hook()
}

// migrateLocalLogin performs or resumes the cutover for one home.
func migrateLocalLogin(ctx context.Context, layout home.Layout, configPath, passwordAccount string, getenv func(string) string, stdout io.Writer, hooks migrationHooks) error {
	passwordAccount = strings.TrimSpace(passwordAccount)
	if passwordAccount == "" {
		return errors.New("--password-account is required: the account EGGY_UI_USER_EMAIL and EGGY_UI_PASSWORD sign in")
	}
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	// Preflight: the environment credentials, the binding, and the whole
	// candidate config are checked before anything is touched.
	plan, err := config.PrepareLocalAccounts(configPath, passwordAccount, getenv)
	if err != nil {
		return err
	}
	configBackup := configPath + localLoginBackupSuffix
	databasePath := layout.Database()
	databaseBackup := databasePath + localLoginBackupSuffix

	if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
		// A home with no database yet has nothing to back up and no
		// sessions to clear; the config is the whole migration.
		if err := config.MigrateLocalAccounts(configPath, passwordAccount, getenv); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "local login migration complete (no database yet)\nconfig: %s\n", configPath)
		return nil
	} else if err != nil {
		return err
	}

	// Opening also proves the daemon is stopped: a live eggyd holds the
	// single connection and the open times out.
	store, err := sqlitestore.OpenForLocalAuthMigration(databasePath)
	if err != nil {
		return fmt.Errorf("open database (is eggyd stopped?): %w", err)
	}
	defer store.Close()

	marker, found, err := store.LocalLoginCutover(ctx)
	if err != nil {
		return err
	}
	if !found {
		// Backups with no marker are somebody else's: an earlier attempt
		// with a different binary, or a copy the operator made. Refuse
		// rather than guess which state they describe.
		for _, path := range []string{configBackup, databaseBackup} {
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists but no migration is in progress; move it aside before running the cutover", path)
			}
		}
		marker = sqlitestore.LocalLoginCutover{Phase: sqlitestore.CutoverPrepared, ConfigDigest: plan.ConfigDigest, ConfigBackup: configBackup, DatabaseBackup: databaseBackup}
		if err := store.RecordLocalLoginCutover(ctx, marker); err != nil {
			return err
		}
	}
	if marker.ConfigBackup != "" {
		configBackup = marker.ConfigBackup
	}
	if marker.DatabaseBackup != "" {
		databaseBackup = marker.DatabaseBackup
	}

	switch marker.Phase {
	case sqlitestore.CutoverPrepared:
		// Interrupted before or during the backups. The config on disk must
		// still be the one the marker describes; a file edited since would
		// be migrated from a version nobody backed up.
		if plan.ConfigDigest != marker.ConfigDigest {
			return fmt.Errorf("config.yaml changed since the migration was prepared; restore it or move %s aside and start over", configBackup)
		}
		if err := ensureConfigBackup(configPath, configBackup, marker.ConfigDigest); err != nil {
			return err
		}
		if err := ensureDatabaseBackup(ctx, store, databaseBackup); err != nil {
			return err
		}
		if err := hooks.run(hooks.afterBackup); err != nil {
			return err
		}
		if err := store.MigrateLocalAuth(ctx); err != nil {
			return fmt.Errorf("upgrade database: %w", err)
		}
		if err := seedAccountAuth(ctx, store, plan.Accounts); err != nil {
			return err
		}
		marker.Phase = sqlitestore.CutoverDatabaseReady
		if err := store.RecordLocalLoginCutover(ctx, marker); err != nil {
			return err
		}
		fallthrough
	case sqlitestore.CutoverDatabaseReady:
		if err := hooks.run(hooks.afterDatabase); err != nil {
			return err
		}
		// A rerun after a crash here finds the database upgraded; the
		// backups were verified when they were taken and are reused, rows
		// are seeded only where missing, and MigrateLocalAuth is a no-op.
		if err := seedAccountAuth(ctx, store, plan.Accounts); err != nil {
			return err
		}
		if err := config.MigrateLocalAccounts(configPath, passwordAccount, getenv); err != nil {
			return fmt.Errorf("rewrite config: %w", err)
		}
		if err := hooks.run(hooks.afterConfig); err != nil {
			return err
		}
		marker.Phase = sqlitestore.CutoverComplete
		if err := store.RecordLocalLoginCutover(ctx, marker); err != nil {
			return err
		}
	case sqlitestore.CutoverComplete:
		// The config write is atomic and precedes the marker, so a complete
		// marker with an unmigrated config means the operator restored the
		// old file by hand. Finish it rather than fail.
		if err := config.MigrateLocalAccounts(configPath, passwordAccount, getenv); err != nil {
			return fmt.Errorf("rewrite config: %w", err)
		}
	default:
		return fmt.Errorf("unknown migration phase %q", marker.Phase)
	}
	fmt.Fprintf(stdout, "local login migration complete\nconfig: %s\nconfig backup: %s\ndatabase backup: %s\n", configPath, configBackup, databaseBackup)
	return nil
}

// ensureConfigBackup writes the backup exactly once and verifies whatever it
// finds against the digest the marker recorded, so a backup from an
// interrupted run is trusted only if it is byte-for-byte the file the run
// started from.
func ensureConfigBackup(configPath, backup, digest string) error {
	if existing, err := config.ConfigDigest(backup); err == nil {
		if existing != digest {
			return fmt.Errorf("%s does not match the config the migration started from; move it aside", backup)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(backup), ".config-backup-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// Link rather than rename: link fails when the target exists, which is
	// the no-overwrite rule enforced by the filesystem instead of a check.
	if err := os.Link(temporaryPath, backup); err != nil {
		return fmt.Errorf("write config backup: %w", err)
	}
	written, err := config.ConfigDigest(backup)
	if err != nil {
		return err
	}
	if written != digest {
		return fmt.Errorf("config backup %s did not verify", backup)
	}
	return nil
}

func ensureDatabaseBackup(ctx context.Context, store *sqlitestore.Store, backup string) error {
	if _, err := os.Stat(backup); err == nil {
		return sqlitestore.VerifyDatabaseBackup(ctx, backup)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return store.BackupBeforeLocalAuth(ctx, backup)
}

// seedAccountAuth registers a credential row for every configured account,
// leaving rows that already exist -- pending, live or retired -- alone.
func seedAccountAuth(ctx context.Context, store *sqlitestore.Store, accounts []config.AccountConfig) error {
	for _, account := range accounts {
		err := store.RegisterAccountAuth(ctx, account.ID)
		if err != nil && !errors.Is(err, ports.ErrAccountIDUsed) {
			return fmt.Errorf("register account %q: %w", account.ID, err)
		}
	}
	return nil
}
