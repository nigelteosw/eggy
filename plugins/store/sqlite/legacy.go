package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LegacyHome names the three artifacts a pre-consolidation home kept beside
// eggy.db. They are passed in rather than resolved here so this package stays
// unaware of the home layout, and so a test can point them at a fixture.
type LegacyHome struct {
	State string // <home>/state.json
	Cron  string // <home>/cron
	Auth  string // <home>/auth.json
}

// Import names are the keys in the imports table. They are the identity of a
// migration forever after: renaming one would re-run it against a home that
// already moved.
const (
	importState     = "state.json"
	importSchedules = "cron"
	importAuth      = "auth.json"
)

// archiveSuffix is appended to a source once its records are in the database.
// The source is kept rather than deleted: it is the rollback path, and a
// migration whose only copy of the owner's grants is the one it just wrote is
// a migration nobody should run.
const archiveSuffix = ".migrated"

// ImportReport says what each migration moved, for the one log line a boot
// after an upgrade should produce. An absent key means that import had
// already run.
type ImportReport struct{ Moved map[string]int }

// Moved reports whether anything was imported at all, so a steady-state boot
// stays silent.
func (r ImportReport) Any() bool { return len(r.Moved) > 0 }

// ImportLegacy moves state.json, cron/, and auth.json into the database. It
// is safe to call on every boot and on a home that never had them.
//
// Each import is one transaction plus a marker row, so an interruption lands
// on one side or the other: no marker means nothing was written and the next
// boot retries; a marker means every record committed and the next boot skips
// it. Archiving the source happens after the marker rather than inside the
// transaction, so a crash in the gap is repaired by the next boot's archive
// step instead of importing the same records twice.
func (s *Store) ImportLegacy(ctx context.Context, legacy LegacyHome, now func() time.Time) (ImportReport, error) {
	if now == nil {
		now = time.Now
	}
	report := ImportReport{Moved: map[string]int{}}
	imports := []struct {
		name   string
		source string
		apply  func(context.Context, *sql.Tx, string) (int, error)
	}{
		{importState, legacy.State, importStateFile},
		{importSchedules, legacy.Cron, importCronDirectory},
		{importAuth, legacy.Auth, importAuthFile},
	}
	for _, item := range imports {
		if item.source == "" {
			continue
		}
		moved, ran, err := s.importOnce(ctx, item.name, item.source, now, item.apply)
		if err != nil {
			return ImportReport{}, fmt.Errorf("import %s: %w", item.name, err)
		}
		// A fresh home runs every import against nothing and records the
		// markers anyway, so it never looks again. That is not news, so it
		// does not reach the report.
		if ran && moved > 0 {
			report.Moved[item.name] = moved
		}
	}
	return report, nil
}

func (s *Store) importOnce(ctx context.Context, name, source string, now func() time.Time, apply func(context.Context, *sql.Tx, string) (int, error)) (int, bool, error) {
	var applied int
	err := s.db.QueryRowContext(ctx, `SELECT records FROM imports WHERE name = ?`, name).Scan(&applied)
	switch {
	case err == nil:
		// Already imported. The archive below still runs: the previous boot
		// may have died between the commit and the rename.
		return 0, false, archive(source)
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	moved, err := apply(ctx, tx, source)
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO imports (name, imported_at, records) VALUES (?, ?, ?)`,
		name, now().UnixNano(), moved); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return moved, true, archive(source)
}

// archive renames a migrated source out of the way. A target that already
// exists is never overwritten -- it is an earlier archive, and the older copy
// is the one worth keeping -- so the newer source is parked beside it.
func archive(source string) error {
	if _, err := os.Lstat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	target := source + archiveSuffix
	if _, err := os.Lstat(target); err == nil {
		target = fmt.Sprintf("%s.%d", target, time.Now().UnixNano())
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("archive %s: %w", source, err)
	}
	return nil
}

// importStateFile moves state.json in whole. The file's own schema version is
// not checked: every version it ever carried decodes into the same struct,
// and the fields that were dropped along the way are simply absent from it.
func importStateFile(ctx context.Context, tx *sql.Tx, path string) (int, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	state := initialState()
	if err := json.Unmarshal(body, &state); err != nil {
		return 0, fmt.Errorf("decode state: %w", err)
	}
	state.SchemaVersion = MachineStateVersion
	// Written unowned: state.json predates accounts, and MigrateAccounts
	// assigns it in the same boot once the owner is known.
	if err := saveState(ctx, tx, "", state); err != nil {
		return 0, err
	}
	// One record for the state row itself, plus every collection entry, so
	// the reported count matches what an owner would see restored.
	return 1 + len(state.Approvals) + len(state.ProcessedEvents) + len(state.ProactiveMessages), nil
}

// legacyJob is the YAML shape cron files were written in. It is duplicated
// here rather than imported because the package that owned it is gone: this
// is the last reader that will ever need it.
type legacyJob struct {
	ID          string `yaml:"id"`
	Kind        string `yaml:"kind"`
	Execution   string `yaml:"execution"`
	Instruction string `yaml:"instruction"`
	Cron        string `yaml:"cron"`
	NextRun     string `yaml:"next_run"`
	LastRun     string `yaml:"last_run"`
	PendingRun  string `yaml:"pending_run"`
	Enabled     bool   `yaml:"enabled"`
}

func importCronDirectory(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	moved := 0
	for _, name := range names {
		id := strings.TrimSuffix(name, ".yaml")
		if !namePattern.MatchString(id) {
			return 0, fmt.Errorf("schedule file %q has an unusable id", name)
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return 0, err
		}
		var job legacyJob
		if err := yaml.Unmarshal(body, &job); err != nil {
			return 0, fmt.Errorf("decode schedule %s: %w", id, err)
		}
		// The file name is the id an owner actually addressed the job by, so
		// a file whose body disagrees with its name keeps the name.
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedules (`+scheduleColumns+`) VALUES (?, '', ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, job.Kind, job.Execution, job.Instruction, job.Cron,
			job.NextRun, job.LastRun, job.PendingRun, boolToInt(job.Enabled)); err != nil {
			return 0, fmt.Errorf("write schedule %s: %w", id, err)
		}
		moved++
	}
	return moved, nil
}

// legacyAuthDocument is auth.json's container shape. Records inside it stay
// sealed: this copies ciphertext, so the owner's key is not needed to
// migrate and a wrong key cannot corrupt a grant on the way through.
type legacyAuthDocument struct {
	Version  int                                   `json:"version"`
	Sections map[string]map[string]json.RawMessage `json:"sections"`
}

func importAuthFile(ctx context.Context, tx *sql.Tx, path string) (int, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var document legacyAuthDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return 0, fmt.Errorf("decode auth.json: %w", err)
	}
	if document.Version != 1 {
		return 0, fmt.Errorf("auth.json has unsupported version %d", document.Version)
	}
	sections := make([]string, 0, len(document.Sections))
	for section := range document.Sections {
		sections = append(sections, section)
	}
	sort.Strings(sections)
	moved := 0
	for _, section := range sections {
		keys := make([]string, 0, len(document.Sections[section]))
		for key := range document.Sections[section] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := validateAuthName(section, key); err != nil {
				return 0, fmt.Errorf("auth record %s/%s: %w", section, key, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO auth_records (section, key, record) VALUES (?, ?, ?)`,
				section, key, string(document.Sections[section][key])); err != nil {
				return 0, fmt.Errorf("write auth record %s/%s: %w", section, key, err)
			}
			moved++
		}
	}
	return moved, nil
}
