package cronfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func sampleSchedule() ports.Schedule {
	return ports.Schedule{
		ID: "abc123", Kind: ports.ScheduleRecurring, Execution: ports.ScheduleExecutionAgent,
		Instruction: "check the oven", Expression: "*/5 * * * *",
		NextRun: time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC), Enabled: true,
	}
}

func TestRoundTripPreservesEveryField(t *testing.T) {
	store := Open(t.TempDir())
	want := sampleSchedule()
	want.LastRun = time.Date(2026, 7, 19, 9, 55, 0, 0, time.UTC)
	if err := store.Put(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != want.Kind || got.Execution != want.Execution || got.Instruction != want.Instruction ||
		got.Expression != want.Expression || !got.NextRun.Equal(want.NextRun) || !got.LastRun.Equal(want.LastRun) ||
		!got.Enabled {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
}

// TestFileIsReadableYAML proves the whole point of the cron directory: an
// owner opening the file sees plain keys they can edit, not an opaque blob.
func TestFileIsReadableYAML(t *testing.T) {
	dir := t.TempDir()
	if err := Open(dir).Put(sampleSchedule()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "abc123.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"instruction: check the oven", "cron: '*/5 * * * *'", "enabled: true"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("file does not contain %q:\n%s", want, body)
		}
	}
}

// TestRecurringScheduleKeepsItsLocation proves a "09:00 daily" job written in
// the owner's timezone survives a round trip in that timezone: normalizing to
// UTC on disk would silently move the job.
func TestRecurringScheduleKeepsItsLocation(t *testing.T) {
	singapore, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Skip("no tzdata")
	}
	store := Open(t.TempDir())
	schedule := sampleSchedule()
	schedule.NextRun = time.Date(2026, 7, 20, 9, 0, 0, 0, singapore)
	if err := store.Put(schedule); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hour := got.NextRun.Hour(); hour != 9 {
		t.Fatalf("next run hour=%d in zone %v, want 9", hour, got.NextRun.Location())
	}
}

func TestGetReportsMissingSchedules(t *testing.T) {
	if _, err := Open(t.TempDir()).Get("nothing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

func TestCreateRefusesAnExistingID(t *testing.T) {
	store := Open(t.TempDir())
	if err := store.Create(sampleSchedule()); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(sampleSchedule()); err == nil {
		t.Fatal("expected a duplicate id to be refused")
	}
}

func TestListIsOrderedAndIgnoresForeignFiles(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir)
	for _, id := range []string{"ccc", "aaa", "bbb"} {
		schedule := sampleSchedule()
		schedule.ID = id
		if err := store.Put(schedule); err != nil {
			t.Fatal(err)
		}
	}
	// A stray file in the directory must not break the listing.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	schedules, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 3 || schedules[0].ID != "aaa" || schedules[2].ID != "ccc" {
		t.Fatalf("schedules=%#v", schedules)
	}
}

// TestFilenameIsAuthoritativeForTheID proves an owner who copies a job file
// to a new name gets a second, independent schedule rather than a duplicate
// that fights the first one for the same id.
func TestFilenameIsAuthoritativeForTheID(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir)
	if err := store.Put(sampleSchedule()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "abc123.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copy.yaml"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	copied, err := store.Get("copy")
	if err != nil {
		t.Fatal(err)
	}
	if copied.ID != "copy" {
		t.Fatalf("id=%q, want the filename", copied.ID)
	}
}

// TestHandEditedFileWithABadTimeIsReportedNotSkipped proves a schedule an
// owner broke while editing fails loudly, instead of quietly never firing.
func TestHandEditedFileWithABadTimeIsReportedNotSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("id: broken\ninstruction: x\nnext_run: tomorrow\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir).List(); err == nil {
		t.Fatal("expected an unparseable schedule to be reported")
	}
}

func TestInvalidIDsAreRejected(t *testing.T) {
	store := Open(t.TempDir())
	for _, id := range []string{"", "../escape", "with/slash"} {
		schedule := sampleSchedule()
		schedule.ID = id
		if err := store.Put(schedule); err == nil {
			t.Fatalf("id %q was accepted", id)
		}
		if _, err := store.Get(id); err == nil {
			t.Fatalf("id %q was read", id)
		}
	}
}

func TestUpdateAppliesUnderTheFileLock(t *testing.T) {
	store := Open(t.TempDir())
	if err := store.Put(sampleSchedule()); err != nil {
		t.Fatal(err)
	}
	if err := store.Update("abc123", func(schedule *ports.Schedule) error {
		schedule.Enabled = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("abc123")
	if err != nil || got.Enabled {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	store := Open(t.TempDir())
	if err := store.Put(sampleSchedule()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.Delete("abc123"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Get("abc123"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}
