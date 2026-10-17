package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// marketplaceSchema holds install records for the marketplace extension. It is
// additive and applied on every open with the other schemas, so an older binary
// can open the file and ignore it, and no MachineStateVersion bump is needed.
//
// marketplace_installs has one row per installed item. UNIQUE(source_name, item)
// stops the same item from being installed twice, so a second install cannot
// silently replace the first's record. marketplace_files lists what that install
// wrote, with each file's hash as installed, which is how local edits are found.
const marketplaceSchema = `
CREATE TABLE IF NOT EXISTS marketplace_installs (
  id           TEXT PRIMARY KEY,
  source_name  TEXT NOT NULL,
  repo         TEXT NOT NULL,
  item         TEXT NOT NULL,
  commit_sha   TEXT NOT NULL,
  plan_digest  TEXT NOT NULL,
  installed_by TEXT NOT NULL,
  installed_at TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  disabled     INTEGER NOT NULL DEFAULT 0,
  UNIQUE (source_name, item)
);
CREATE TABLE IF NOT EXISTS marketplace_files (
  install_id   TEXT NOT NULL,
  kind         TEXT NOT NULL,
  target       TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  PRIMARY KEY (install_id, kind, target)
);
`

func (s *Store) CreateInstall(ctx context.Context, install ports.MarketplaceInstall) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO marketplace_installs
		(id, source_name, repo, item, commit_sha, plan_digest, installed_by, installed_at, updated_at, disabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		install.ID, install.SourceName, install.Repo, install.Item, install.CommitSHA, install.PlanDigest,
		install.InstalledBy, marketplaceTime(install.InstalledAt), marketplaceTime(install.UpdatedAt), boolInt(install.Disabled))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: marketplace_installs.source_name") {
			return ports.ErrMarketplaceInstallExists
		}
		return err
	}
	if err := insertMarketplaceFiles(ctx, tx, install); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceInstall rewrites an existing record and its file list in one
// transaction, for an update the owner has approved.
func (s *Store) ReplaceInstall(ctx context.Context, install ports.MarketplaceInstall) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE marketplace_installs
		SET source_name = ?, repo = ?, item = ?, commit_sha = ?, plan_digest = ?, updated_at = ?, disabled = ?
		WHERE id = ?`,
		install.SourceName, install.Repo, install.Item, install.CommitSHA, install.PlanDigest,
		marketplaceTime(install.UpdatedAt), boolInt(install.Disabled), install.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ports.ErrMarketplaceInstallNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM marketplace_files WHERE install_id = ?`, install.ID); err != nil {
		return err
	}
	if err := insertMarketplaceFiles(ctx, tx, install); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListInstalls(ctx context.Context) ([]ports.MarketplaceInstall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, source_name, repo, item, commit_sha, plan_digest, installed_by, installed_at, updated_at, disabled
		FROM marketplace_installs ORDER BY installed_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var installs []ports.MarketplaceInstall
	for rows.Next() {
		install, err := scanMarketplaceInstall(rows.Scan)
		if err != nil {
			return nil, err
		}
		installs = append(installs, install)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range installs {
		files, err := s.marketplaceFiles(ctx, installs[i].ID)
		if err != nil {
			return nil, err
		}
		installs[i].Files = files
	}
	return installs, nil
}

func (s *Store) GetInstall(ctx context.Context, id string) (ports.MarketplaceInstall, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, source_name, repo, item, commit_sha, plan_digest, installed_by, installed_at, updated_at, disabled
		FROM marketplace_installs WHERE id = ?`, id)
	install, err := scanMarketplaceInstall(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.MarketplaceInstall{}, ports.ErrMarketplaceInstallNotFound
	}
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	install.Files, err = s.marketplaceFiles(ctx, id)
	return install, err
}

func (s *Store) SetInstallDisabled(ctx context.Context, id string, disabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE marketplace_installs SET disabled = ? WHERE id = ?`, boolInt(disabled), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ports.ErrMarketplaceInstallNotFound
	}
	return nil
}

// DeleteInstall removes a record and its file list together. It does not touch
// the skills or the MCP config the install wrote; the service does that first.
func (s *Store) DeleteInstall(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM marketplace_files WHERE install_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM marketplace_installs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ports.ErrMarketplaceInstallNotFound
	}
	return tx.Commit()
}

func (s *Store) marketplaceFiles(ctx context.Context, installID string) ([]ports.MarketplaceFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, target, content_hash FROM marketplace_files
		WHERE install_id = ? ORDER BY kind, target`, installID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []ports.MarketplaceFile
	for rows.Next() {
		var f ports.MarketplaceFile
		if err := rows.Scan(&f.Kind, &f.Target, &f.ContentHash); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

type marketplaceExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertMarketplaceFiles(ctx context.Context, tx marketplaceExecer, install ports.MarketplaceInstall) error {
	for _, f := range install.Files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO marketplace_files (install_id, kind, target, content_hash) VALUES (?, ?, ?, ?)`,
			install.ID, f.Kind, f.Target, f.ContentHash); err != nil {
			return err
		}
	}
	return nil
}

func scanMarketplaceInstall(scan func(...any) error) (ports.MarketplaceInstall, error) {
	var (
		install                ports.MarketplaceInstall
		installedAt, updatedAt string
		disabled               int
	)
	if err := scan(&install.ID, &install.SourceName, &install.Repo, &install.Item, &install.CommitSHA,
		&install.PlanDigest, &install.InstalledBy, &installedAt, &updatedAt, &disabled); err != nil {
		return ports.MarketplaceInstall{}, err
	}
	var err error
	if install.InstalledAt, err = time.Parse(time.RFC3339Nano, installedAt); err != nil {
		return ports.MarketplaceInstall{}, err
	}
	if install.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return ports.MarketplaceInstall{}, err
	}
	install.Disabled = disabled != 0
	return install, nil
}

func marketplaceTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
