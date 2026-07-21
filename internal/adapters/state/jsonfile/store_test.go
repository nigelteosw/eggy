package jsonfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestMigratesSchemaOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	body := `{
  "schema_version": 1,
  "version": 7,
  "conversation_summary": "summary",
  "selected_repository": "eggy",
  "recent_messages": [{"role":"user","content":"hello"}],
  "tasks": {"task-1":{"id":"task-1","kind":"chat","status":"pending"}},
  "approvals": {},
  "schedules": {},
  "coding_runs": {},
  "processed_events": {"event-1":"2026-07-19T00:00:00Z"},
  "proactive_messages": ["2026-07-19T00:00:00Z"],
  "calendar": {"encrypted_refresh_token":"cipher"}
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := Open(path).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != CurrentSchemaVersion || state.Version != 7 || len(state.RecentMessages) != 1 || state.Calendar.EncryptedRefreshToken != "cipher" || len(state.ProcessedEvents) != 1 || len(state.ProactiveMessages) != 1 {
		t.Fatalf("migrated state = %#v", state)
	}
	if state.Agent.SelectedModel != "" || len(state.Agent.Usage) != 0 {
		t.Fatalf("unexpected agent state = %#v", state.Agent)
	}
	persisted, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(persisted), `"schema_version": 3`) {
		t.Fatalf("persisted migration=%s err=%v", persisted, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
}

// TestMigratesRepresentativeProductionState loads a schema-2 state file shaped
// like a real deployed instance (repositories, approvals, schedules, Calendar
// auth, model selection and usage, and coding history) plus the dead
// schema-2 fields (tasks, selected_repository, coding) that this migration
// drops, and checks the drop leaves every unrelated field intact.
func TestMigratesRepresentativeProductionState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	body := `{
  "schema_version": 2,
  "version": 42,
  "conversation_summary": "stale summary",
  "selected_repository": "eggy",
  "recent_messages": [{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}],
  "tasks": {"task-1":{"id":"task-1","kind":"chat","status":"running"}},
  "approvals": {"a-1":{"id":"a-1","status":"pending"}},
  "schedules": {"s-1":{"id":"s-1","kind":"recurring","instruction":"check in","expression":"0 9 * * *","enabled":true}},
  "coding_runs": {"run-1":{"id":"run-1","repository":"eggy","workspace":"/data/runs/run-1","branch":"eggy/run-1","status":"completed"}},
  "repositories": {"eggy":{"Name":"eggy","CloneURL":"https://github.com/nigelteosw/eggy.git","BaseBranch":"main"}},
  "processed_events": {"event-1":"2026-07-19T00:00:00Z"},
  "proactive_messages": ["2026-07-19T00:00:00Z"],
  "calendar": {"encrypted_refresh_token":"cipher","enrollment_digest":"digest"},
  "agent": {"selected_model":"gpt-5","reasoning_effort":"medium","usage":{"gpt-5":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}},
  "coding": {"selected_agent":"stale-agent"}
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := Open(path).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != CurrentSchemaVersion || state.Version != 42 {
		t.Fatalf("schema/version = %#v", state)
	}
	if len(state.RecentMessages) != 2 || len(state.ProcessedEvents) != 1 || len(state.ProactiveMessages) != 1 {
		t.Fatalf("unrelated collections corrupted = %#v", state)
	}
	if len(state.Approvals) != 1 || state.Approvals["a-1"].Status != approvals.Pending {
		t.Fatalf("approvals corrupted = %#v", state.Approvals)
	}
	if len(state.Schedules) != 1 || state.Schedules["s-1"].Instruction != "check in" {
		t.Fatalf("schedules corrupted = %#v", state.Schedules)
	}
	if len(state.CodingRuns) != 1 || state.CodingRuns["run-1"].Status != "completed" {
		t.Fatalf("coding runs corrupted = %#v", state.CodingRuns)
	}
	if len(state.Repositories) != 1 || state.Repositories["eggy"].CloneURL != "https://github.com/nigelteosw/eggy.git" {
		t.Fatalf("repositories corrupted = %#v", state.Repositories)
	}
	if state.Calendar.EncryptedRefreshToken != "cipher" || state.Calendar.EnrollmentDigest != "digest" {
		t.Fatalf("calendar auth corrupted = %#v", state.Calendar)
	}
	if state.Agent.SelectedModel != "gpt-5" || state.Agent.ReasoningEffort != "medium" || state.Agent.Usage["gpt-5"].TotalTokens != 150 {
		t.Fatalf("agent state corrupted = %#v", state.Agent)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, dropped := range []string{`"tasks"`, `"selected_repository"`, `"conversation_summary"`, `"coding":`} {
		if strings.Contains(string(persisted), dropped) {
			t.Fatalf("dropped field %q still persisted: %s", dropped, persisted)
		}
	}
}

func TestRejectsFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path).Load(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported state schema 4") {
		t.Fatalf("error=%v", err)
	}
}

func TestStoreCreatesAndAtomicallyUpdatesVersionedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	store := Open(path)
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != CurrentSchemaVersion || state.Version != 0 {
		t.Fatalf("unexpected initial state %#v", state)
	}
	updated, err := store.Update(context.Background(), 0, func(s *ports.State) error {
		s.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
		s.Agent.SelectedModel = "gpt-5"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 1 || len(updated.Repositories) != 1 || updated.Repositories["eggy"].Name != "eggy" || updated.Agent.SelectedModel != "gpt-5" {
		t.Fatalf("unexpected updated state %#v", updated)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil || len(onDisk) == 0 {
		t.Fatalf("state not persisted: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json-*"))
	if len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestStoreRejectsStaleVersionWithoutMutation(t *testing.T) {
	store := Open(filepath.Join(t.TempDir(), "state.json"))
	if _, err := store.Update(context.Background(), 0, func(s *ports.State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := store.Update(context.Background(), 0, func(s *ports.State) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrVersionConflict) || called {
		t.Fatalf("expected pre-mutation conflict, got err=%v called=%v", err, called)
	}
}

func TestStoreSerializesConcurrentUpdates(t *testing.T) {
	store := Open(filepath.Join(t.TempDir(), "state.json"))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Update(context.Background(), 0, func(s *ports.State) error { return nil })
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestIndependentStoreInstancesUseProcessLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	stores := []*Store{Open(path), Open(path)}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, store := range stores {
		go func(store *Store) {
			<-start
			_, err := store.Update(context.Background(), 0, func(*ports.State) error { return nil })
			results <- err
		}(store)
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrVersionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}
