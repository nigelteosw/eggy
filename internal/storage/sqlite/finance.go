package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// financeSchema is applied on every open with the other schemas. A new table
// is additive -- an older binary opens the same file and ignores it -- so it
// does not raise MachineStateVersion, which would lock a rollback out for no
// reason. A later change to this table's shape would.
//
// The table exists whether or not the finance plugin is enabled: turning the
// plugin off must never lose what was logged, and an empty table costs nothing.
//
// Money is an integer count of the minor unit, never a float. The CHECK is the
// last line behind the service's own validation.
const financeSchema = `
CREATE TABLE IF NOT EXISTS finance_entries (
  id           TEXT PRIMARY KEY,
  account_id   TEXT NOT NULL,
  occurred_on  TEXT NOT NULL,
  amount_minor INTEGER NOT NULL CHECK (amount_minor > 0),
  currency     TEXT NOT NULL,
  category     TEXT NOT NULL,
  merchant     TEXT NOT NULL DEFAULT '',
  note         TEXT NOT NULL DEFAULT '',
  source       TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS finance_entries_account_date
  ON finance_entries (account_id, occurred_on);
`

const financeColumns = `id, occurred_on, amount_minor, currency, category, merchant, note, source, created_at, updated_at`

func scanFinanceEntry(scan func(...any) error) (ports.FinanceEntry, error) {
	var (
		entry              ports.FinanceEntry
		created, updatedAt string
	)
	if err := scan(&entry.ID, &entry.OccurredOn, &entry.AmountMinor, &entry.Currency, &entry.Category, &entry.Merchant, &entry.Note, &entry.Source, &created, &updatedAt); err != nil {
		return ports.FinanceEntry{}, err
	}
	var err error
	if entry.CreatedAt, err = parseTime(created); err != nil {
		return ports.FinanceEntry{}, fmt.Errorf("finance entry %s created_at: %w", entry.ID, err)
	}
	if entry.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return ports.FinanceEntry{}, fmt.Errorf("finance entry %s updated_at: %w", entry.ID, err)
	}
	return entry, nil
}

// CreateFinanceEntry writes an entry for the acting account. The id is the
// caller's, so a duplicate is refused by the primary key rather than silently
// replacing someone's record.
func (s *Store) CreateFinanceEntry(ctx context.Context, entry ports.FinanceEntry) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO finance_entries (id, account_id, occurred_on, amount_minor, currency, category, merchant, note, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, account, entry.OccurredOn, entry.AmountMinor, entry.Currency, entry.Category, entry.Merchant, entry.Note, entry.Source,
		formatTime(entry.CreatedAt), formatTime(entry.UpdatedAt))
	if err != nil {
		return fmt.Errorf("create finance entry: %w", err)
	}
	return nil
}

// UpdateFinanceEntry runs the whole read-modify-write in one transaction. The
// id and creation time are not the mutator's to change: they are restored
// before the write, so an edit can never move a record or rewrite its history.
func (s *Store) UpdateFinanceEntry(ctx context.Context, id string, mutate func(*ports.FinanceEntry) error) (ports.FinanceEntry, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	defer func() { _ = tx.Rollback() }()
	entry, err := scanFinanceEntry(tx.QueryRowContext(ctx, `SELECT `+financeColumns+` FROM finance_entries WHERE id = ? AND account_id = ?`, id, account).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.FinanceEntry{}, ports.ErrFinanceEntryNotFound
	}
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	created := entry.CreatedAt
	if err := mutate(&entry); err != nil {
		return ports.FinanceEntry{}, err
	}
	entry.ID, entry.CreatedAt = id, created
	if _, err := tx.ExecContext(ctx, `UPDATE finance_entries
		SET occurred_on = ?, amount_minor = ?, currency = ?, category = ?, merchant = ?, note = ?, source = ?, updated_at = ?
		WHERE id = ? AND account_id = ?`,
		entry.OccurredOn, entry.AmountMinor, entry.Currency, entry.Category, entry.Merchant, entry.Note, entry.Source, formatTime(entry.UpdatedAt),
		id, account); err != nil {
		return ports.FinanceEntry{}, fmt.Errorf("update finance entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ports.FinanceEntry{}, err
	}
	return entry, nil
}

// DeleteFinanceEntry removes an entry and returns it. Read and delete share a
// transaction, so what is returned is exactly what was removed.
func (s *Store) DeleteFinanceEntry(ctx context.Context, id string) (ports.FinanceEntry, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	defer func() { _ = tx.Rollback() }()
	entry, err := scanFinanceEntry(tx.QueryRowContext(ctx, `SELECT `+financeColumns+` FROM finance_entries WHERE id = ? AND account_id = ?`, id, account).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.FinanceEntry{}, ports.ErrFinanceEntryNotFound
	}
	if err != nil {
		return ports.FinanceEntry{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM finance_entries WHERE id = ? AND account_id = ?`, id, account); err != nil {
		return ports.FinanceEntry{}, fmt.Errorf("delete finance entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ports.FinanceEntry{}, err
	}
	return entry, nil
}

// financeWhere builds the one predicate listing and totals share, always led by
// the account. Dates are YYYY-MM-DD text, so string comparison is date order.
func financeWhere(account, from, to, category string) (string, []any) {
	clauses := []string{"account_id = ?"}
	args := []any{account}
	if from != "" {
		clauses = append(clauses, "occurred_on >= ?")
		args = append(args, from)
	}
	if to != "" {
		clauses = append(clauses, "occurred_on <= ?")
		args = append(args, to)
	}
	if category != "" {
		clauses = append(clauses, "category = ?")
		args = append(args, category)
	}
	return strings.Join(clauses, " AND "), args
}

// ListFinanceEntries returns the newest entries first, with the number that
// matched before the limit so a caller can say how many were left out.
func (s *Store) ListFinanceEntries(ctx context.Context, filter ports.FinanceFilter) ([]ports.FinanceEntry, int, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return nil, 0, err
	}
	where, args := financeWhere(account, filter.From, filter.To, filter.Category)
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM finance_entries WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := `SELECT ` + financeColumns + ` FROM finance_entries WHERE ` + where + ` ORDER BY occurred_on DESC, created_at DESC, id DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	entries := make([]ports.FinanceEntry, 0)
	err = scanRows(ctx, s.db, query, func(scan func(...any) error) error {
		entry, err := scanFinanceEntry(scan)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	}, args...)
	if err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// FinanceTotals sums in SQL, per currency, so a month is never loaded into
// memory to be added up. Rows come back in a fixed order so the panel and the
// tests see one stable result.
func (s *Store) FinanceTotals(ctx context.Context, from, to string) (ports.FinanceTotals, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return ports.FinanceTotals{}, err
	}
	where, args := financeWhere(account, from, to, "")
	totals := ports.FinanceTotals{ByCategory: []ports.FinanceCategoryTotal{}, ByDay: []ports.FinanceDayTotal{}}
	err = scanRows(ctx, s.db, `SELECT currency, category, SUM(amount_minor), COUNT(*) FROM finance_entries WHERE `+where+`
		GROUP BY currency, category ORDER BY currency, SUM(amount_minor) DESC, category`, func(scan func(...any) error) error {
		var row ports.FinanceCategoryTotal
		if err := scan(&row.Currency, &row.Category, &row.AmountMinor, &row.Count); err != nil {
			return err
		}
		totals.ByCategory = append(totals.ByCategory, row)
		return nil
	}, args...)
	if err != nil {
		return ports.FinanceTotals{}, err
	}
	err = scanRows(ctx, s.db, `SELECT currency, occurred_on, SUM(amount_minor) FROM finance_entries WHERE `+where+`
		GROUP BY currency, occurred_on ORDER BY currency, occurred_on`, func(scan func(...any) error) error {
		var row ports.FinanceDayTotal
		if err := scan(&row.Currency, &row.Day, &row.AmountMinor); err != nil {
			return err
		}
		totals.ByDay = append(totals.ByDay, row)
		return nil
	}, args...)
	if err != nil {
		return ports.FinanceTotals{}, err
	}
	return totals, nil
}
