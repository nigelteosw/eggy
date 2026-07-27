package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	sessionjson "github.com/nigelteosw/eggy/plugins/sessions/jsonfile"
)

// legacyCodingRun mirrors the pre-unification ports.CodingRun JSON shape.
// state.json no longer declares this field; the only place its data still
// exists is the raw bytes of an existing file written by an older Eggy.
type legacyCodingRun struct {
	ID           string    `json:"id"`
	Repository   string    `json:"repository"`
	Workspace    string    `json:"workspace"`
	Branch       string    `json:"branch"`
	BaseRevision string    `json:"base_revision"`
	Commit       string    `json:"commit"`
	Status       string    `json:"status"`
	Diff         string    `json:"diff"`
	Validation   string    `json:"validation"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
}

// importLegacyCodingRuns must run before the first StateStore.Load/Update
// call against statePath in the process's lifetime. jsonfile.Store's own
// schema migration silently drops any JSON field ports.State no longer
// declares (that's how it has always migrated old fields away), so
// coding_runs would otherwise be discarded forever the moment anything
// calls Load. This reads the raw file directly instead, extracts any
// still-present legacy coding_runs, and imports each into the session
// store -- the new canonical home for run metadata -- before that happens.
//
// It is idempotent and crash-safe to rerun: sessions that already exist
// (the normal case for any run whose session survived) are left exactly as
// they are, since the session store is the canonical source once a session
// exists at all; only a coding_runs entry with no matching session (e.g. an
// old deployment that ran with sessions disabled, or a session lost to a
// prior partial write) is imported, as a minimal ImplementationSession
// rather than replayed. A run whose workspace no longer exists on disk is
// imported as PhaseBlocked regardless of its recorded status, so nothing
// ever auto-resumes implementation work against a workspace that is gone.
func importLegacyCodingRuns(ctx context.Context, statePath string, changeStore *sessionjson.ChangeStore, now func() time.Time) (int, error) {
	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read state for legacy coding-run import: %w", err)
	}
	var probe struct {
		SchemaVersion int                        `json:"schema_version"`
		CodingRuns    map[string]legacyCodingRun `json:"coding_runs"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return 0, fmt.Errorf("decode state for legacy coding-run import: %w", err)
	}
	if len(probe.CodingRuns) == 0 {
		return 0, nil
	}
	imported := 0
	for id, run := range probe.CodingRuns {
		if _, err := changeStore.Load(ctx, id); err == nil || !errors.Is(err, sessionjson.ErrSessionNotFound) {
			// Either a change already exists (canonical, leave it alone)
			// or the lookup failed for a reason other than "not found";
			// either way this legacy run is not ours to import.
			continue
		}
		phase := legacyPhase(run.Status)
		if run.Workspace != "" {
			if _, err := os.Stat(run.Workspace); err != nil {
				phase = ports.PhaseBlocked
			}
		}
		// An old CodingRun is a Change: branched work with a lifecycle. It
		// carries no workspace, because that checkout belonged to a process
		// that exited long ago.
		change := ports.Change{
			ID:           id,
			Repository:   run.Repository,
			Branch:       run.Branch,
			BaseRevision: run.BaseRevision,
			Commit:       run.Commit,
			Diff:         run.Diff,
			Validation:   run.Validation,
			Phase:        phase,
			StartedAt:    run.StartedAt,
			FinishedAt:   run.FinishedAt,
		}
		if change.StartedAt.IsZero() {
			change.StartedAt = now()
		}
		if _, err := changeStore.Create(ctx, change); err != nil {
			return imported, fmt.Errorf("import legacy coding run %q: %w", id, err)
		}
		imported++
	}
	return imported, nil
}

// legacyPhase maps the old free-form CodingRun.Status strings onto the
// three-phase model. Anything that was not cleanly finished is imported as
// Blocked rather than silently treated as healthy: a run the migration
// interrupted is exactly the kind an owner should look at before resuming.
func legacyPhase(status string) ports.SessionPhase {
	switch status {
	case "completed":
		return ports.PhaseCompleted
	default:
		return ports.PhaseBlocked
	}
}
