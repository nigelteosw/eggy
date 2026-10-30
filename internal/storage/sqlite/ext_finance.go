package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// financeSchema is applied on every open with the other schemas. The table
// itself is additive, so an older binary can open the file and ignore it. The
// kind column changes what a total means, so it raises MachineStateVersion
// (see machine.go) and an older binary refuses the file instead.
//
// The table exists whether or not the finance extension is enabled: turning the
// extension off must never lose what was logged, and an empty table costs nothing.
//
// Money is an integer count of the minor unit, never a float. The CHECK is the
// last line behind the service's own validation.
const financeSchema = `
CREATE TABLE IF NOT EXISTS finance_entries (
  id           TEXT PRIMARY KEY,
  account_id   TEXT NOT NULL,
  kind         TEXT NOT NULL DEFAULT 'expense',
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
CREATE TABLE IF NOT EXISTS finance_categories (
  account_id TEXT NOT NULL,
  name       TEXT NOT NULL,
  color      TEXT NOT NULL,
  PRIMARY KEY (account_id, name)
);
`

// financeKindColumn is added to a table written before kinds existed. Every such
// row was logged as spending, which is what the default says, so an upgrade
// changes no total.
const financeKindColumn = "TEXT NOT NULL DEFAULT 'expense'"

const financeColumns = `id, kind, occurred_on, amount_minor, currency, category, merchant, note, source, created_at, updated_at`

func scanFinanceEntry(scan func(...any) error) (ports.FinanceEntry, error) {
	var (
		entry              ports.FinanceEntry
		created, updatedAt string
	)
	if err := scan(&entry.ID, &entry.Kind, &entry.OccurredOn, &entry.AmountMinor, &entry.Currency, &entry.Category, &entry.Merchant, &entry.Note, &entry.Source, &created, &updatedAt); err != nil {
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
	if entry.Kind == "" {
		entry.Kind = ports.FinanceKindExpense
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO finance_entries (id, account_id, kind, occurred_on, amount_minor, currency, category, merchant, note, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, account, entry.Kind, entry.OccurredOn, entry.AmountMinor, entry.Currency, entry.Category, entry.Merchant, entry.Note, entry.Source,
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
		SET kind = ?, occurred_on = ?, amount_minor = ?, currency = ?, category = ?, merchant = ?, note = ?, source = ?, updated_at = ?
		WHERE id = ? AND account_id = ?`,
		entry.Kind, entry.OccurredOn, entry.AmountMinor, entry.Currency, entry.Category, entry.Merchant, entry.Note, entry.Source, formatTime(entry.UpdatedAt),
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
func financeWhere(account, from, to, kind, category, text string) (string, []any) {
	clauses := []string{"account_id = ?"}
	args := []any{account}
	if kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, kind)
	}
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
	if text != "" {
		// instr rather than LIKE: the text is matched literally, so a % or _
		// the owner typed is just a character, not a wildcard.
		needle := strings.ToLower(text)
		clauses = append(clauses, "(instr(lower(merchant), ?) > 0 OR instr(lower(note), ?) > 0 OR instr(lower(category), ?) > 0)")
		args = append(args, needle, needle, needle)
	}
	return strings.Join(clauses, " AND "), args
}

// financeOrder is the ORDER BY for a listing. Every fragment is fixed here; the
// sort and order arrive already validated by the service and only choose among
// them. Ties end on newest first, so the order is the same on every call.
func financeOrder(sort, order string) string {
	dir := "DESC"
	if order == "asc" {
		dir = "ASC"
	}
	const newestFirst = ", occurred_on DESC, created_at DESC, id DESC"
	switch sort {
	case ports.FinanceSortAmount:
		// Currencies are never compared: JPY and SGD amounts sort in separate runs.
		return "currency ASC, amount_minor " + dir + newestFirst
	case ports.FinanceSortCategory:
		return "category " + dir + newestFirst
	case ports.FinanceSortMerchant:
		return "lower(merchant) " + dir + newestFirst
	default:
		return "occurred_on " + dir + ", created_at " + dir + ", id " + dir
	}
}

// ListFinanceEntries returns the matching entries in the filter's order, with
// the number that matched before the limit so a caller can say how many were
// left out.
func (s *Store) ListFinanceEntries(ctx context.Context, filter ports.FinanceFilter) ([]ports.FinanceEntry, int, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return nil, 0, err
	}
	where, args := financeWhere(account, filter.From, filter.To, filter.Kind, filter.Category, filter.Query)
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM finance_entries WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := `SELECT ` + financeColumns + ` FROM finance_entries WHERE ` + where + ` ORDER BY ` + financeOrder(filter.Sort, filter.Order)
	if filter.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, filter.Limit, max(filter.Offset, 0))
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
	where, args := financeWhere(account, from, to, "", "", "")
	totals := ports.FinanceTotals{ByCategory: []ports.FinanceCategoryTotal{}, ByDay: []ports.FinanceDayTotal{}}
	err = scanRows(ctx, s.db, `SELECT currency, kind, category, SUM(amount_minor), COUNT(*) FROM finance_entries WHERE `+where+`
		GROUP BY currency, kind, category ORDER BY currency, kind, SUM(amount_minor) DESC, category`, func(scan func(...any) error) error {
		var row ports.FinanceCategoryTotal
		if err := scan(&row.Currency, &row.Kind, &row.Category, &row.AmountMinor, &row.Count); err != nil {
			return err
		}
		totals.ByCategory = append(totals.ByCategory, row)
		return nil
	}, args...)
	if err != nil {
		return ports.FinanceTotals{}, err
	}
	err = scanRows(ctx, s.db, `SELECT currency, kind, occurred_on, SUM(amount_minor) FROM finance_entries WHERE `+where+`
		GROUP BY currency, kind, occurred_on ORDER BY currency, kind, occurred_on`, func(scan func(...any) error) error {
		var row ports.FinanceDayTotal
		if err := scan(&row.Currency, &row.Kind, &row.Day, &row.AmountMinor); err != nil {
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

// FinanceMonths sums each calendar month in from..to per currency and kind. The
// month is cut from the stored date text, so it is the owner's month, the same
// clock every other total uses.
func (s *Store) FinanceMonths(ctx context.Context, from, to string) ([]ports.FinanceMonthTotal, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	where, args := financeWhere(account, from, to, "", "", "")
	months := []ports.FinanceMonthTotal{}
	err = scanRows(ctx, s.db, `SELECT currency, kind, substr(occurred_on, 1, 7), SUM(amount_minor) FROM finance_entries WHERE `+where+`
		GROUP BY currency, kind, substr(occurred_on, 1, 7) ORDER BY substr(occurred_on, 1, 7), currency, kind`, func(scan func(...any) error) error {
		var row ports.FinanceMonthTotal
		if err := scan(&row.Currency, &row.Kind, &row.Month, &row.AmountMinor); err != nil {
			return err
		}
		months = append(months, row)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}
	return months, nil
}

// FinanceCategories lists each category the account has entries in, with how
// many, and the colour it was given. A category never used has no row here, so
// a deleted entry's category stops being offered once its last entry goes.
func (s *Store) FinanceCategories(ctx context.Context) ([]ports.FinanceCategory, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return nil, err
	}
	categories := []ports.FinanceCategory{}
	err = scanRows(ctx, s.db, `SELECT e.category, COUNT(*), COALESCE(c.color, '')
		FROM finance_entries e
		LEFT JOIN finance_categories c ON c.account_id = e.account_id AND c.name = e.category
		WHERE e.account_id = ?
		GROUP BY e.category ORDER BY e.category`, func(scan func(...any) error) error {
		var row ports.FinanceCategory
		if err := scan(&row.Name, &row.Count, &row.Color); err != nil {
			return err
		}
		categories = append(categories, row)
		return nil
	}, account)
	if err != nil {
		return nil, err
	}
	return categories, nil
}

// SetFinanceCategoryColor records the colour for one of the account's categories.
func (s *Store) SetFinanceCategoryColor(ctx context.Context, name, color string) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO finance_categories (account_id, name, color) VALUES (?, ?, ?)
		ON CONFLICT(account_id, name) DO UPDATE SET color = excluded.color`, account, name, color)
	return err
}

// RenameFinanceCategory moves the account's entries from one category to
// another in one transaction. The source's colour goes with them when the
// target has none, and is dropped either way: a merged category has one colour.
func (s *Store) RenameFinanceCategory(ctx context.Context, from, to string) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var sourceColor string
	err = tx.QueryRowContext(ctx, `SELECT color FROM finance_categories WHERE account_id = ? AND name = ?`, account, from).Scan(&sourceColor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE finance_entries SET category = ? WHERE account_id = ? AND category = ?`, to, account, from); err != nil {
		return fmt.Errorf("rename finance category: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM finance_categories WHERE account_id = ? AND name = ?`, account, from); err != nil {
		return err
	}
	if sourceColor != "" {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO finance_categories (account_id, name, color) VALUES (?, ?, ?)`, account, to, sourceColor); err != nil {
			return err
		}
	}
	return tx.Commit()
}
