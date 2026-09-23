package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCreatesOwnerOnlyDirectories(t *testing.T) {
	layout := At(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	// Directories()[0] is the root, whose mode belongs to whoever
	// provisioned the volume; Eggy only forces the ones it owns.
	for _, dir := range layout.Directories()[1:] {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
		// The home holds .env, auth.json, and repository clones, so no
		// group or other bits may ever appear on it.
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("%s mode=%v, want 0700", dir, perm)
		}
	}
}

// TestEnsureTightensAPreexistingDirectory proves a subdirectory left
// world-readable by an earlier run or a careless copy is repaired, not
// accepted as-is.
func TestEnsureTightensAPreexistingDirectory(t *testing.T) {
	layout := At(t.TempDir())
	if err := os.MkdirAll(layout.Skills(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(layout.Skills())
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
}

// TestMigrateMovesFlatContextDocuments proves a home written by an older
// Eggy -- MEMORY.md and USER.md loose at the top level -- is folded into
// memories/ without the caller doing anything.
func TestMigrateMovesFlatContextDocuments(t *testing.T) {
	layout := At(t.TempDir())
	for name, body := range map[string]string{"MEMORY.md": "# memory", "USER.md": "# user", "SOUL.md": "# soul"} {
		if err := os.WriteFile(filepath.Join(layout.Root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := layout.Migrate(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{layout.Memory(): "# memory", layout.User(): "# user", layout.Soul(): "# soul"} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != want {
			t.Fatalf("%s body=%q err=%v", path, body, err)
		}
	}
	// SOUL.md belongs at the top level and must not have been moved.
	if _, err := os.Stat(filepath.Join(layout.Root, "MEMORY.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy MEMORY.md survived: %v", err)
	}
}

// TestMigrateKeepsTheCurrentFileWhenBothExist proves the file already in
// memories/ wins: it is the one Eggy has been writing, and the loose copy is
// a leftover.
func TestMigrateKeepsTheCurrentFileWhenBothExist(t *testing.T) {
	layout := At(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.Root, "MEMORY.md"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Memories(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.Memory(), []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := layout.Migrate(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(layout.Memory())
	if err != nil || string(body) != "current" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	layout := At(t.TempDir())
	if err := os.MkdirAll(layout.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.Root, "USER.md"), []byte("# user"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := layout.Migrate(); err != nil {
			t.Fatal(err)
		}
	}
	if body, err := os.ReadFile(layout.User()); err != nil || string(body) != "# user" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestResolvePrefersFlagThenEnvThenConfigDirectory(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	if got := Resolve("/flag/home", env(map[string]string{"EGGY_HOME": "/env/home"})); got.Root != "/flag/home" {
		t.Fatalf("flag ignored: %q", got.Root)
	}
	if got := Resolve("", env(map[string]string{"EGGY_HOME": "/env/home", "EGGY_CONFIG": "/other/config.yaml"})); got.Root != "/env/home" {
		t.Fatalf("EGGY_HOME ignored: %q", got.Root)
	}
	// An existing deployment sets only EGGY_CONFIG, and its home is the
	// directory that config lives in.
	if got := Resolve("", env(map[string]string{"EGGY_CONFIG": "/srv/eggy/config.yaml"})); got.Root != "/srv/eggy" {
		t.Fatalf("EGGY_CONFIG ignored: %q", got.Root)
	}
	if got := Resolve("", env(nil)); got.Root != At("~/.eggy").Root {
		t.Fatalf("default=%q", got.Root)
	}
}

func TestAtExpandsHomeRelativePaths(t *testing.T) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no user home directory")
	}
	if got := At("~/.eggy"); got.Root != filepath.Join(userHome, ".eggy") {
		t.Fatalf("root=%q", got.Root)
	}
}

func TestWatchLivesUnderMemories(t *testing.T) {
	layout := At("/data")
	if got, want := layout.Watch(), "/data/memories/WATCH.md"; got != want {
		t.Fatalf("Watch()=%q want %q", got, want)
	}
}

func TestAccountMemoriesValidatesTheID(t *testing.T) {
	layout := At(t.TempDir())
	dir, err := layout.AccountMemories("nigel")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(layout.Root, "accounts", "nigel", "memories") {
		t.Fatalf("dir=%s", dir)
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/b", "a\\b", " nigel", strings.Repeat("a", 65)} {
		if _, err := layout.AccountMemories(bad); err == nil {
			t.Errorf("AccountMemories(%q) accepted an unsafe id", bad)
		}
	}
}

// recordingPhases is the migration's progress record, kept in memory here
// where bootstrap keeps it in SQLite.
type recordingPhases struct{ phase string }

func (r *recordingPhases) DocumentMigrationPhase() (string, error) { return r.phase, nil }
func (r *recordingPhases) RecordDocumentMigrationPhase(phase string) error {
	r.phase = phase
	return nil
}

func writeLegacyMemories(t *testing.T, layout Layout) {
	t.Helper()
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Memories(), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"USER.md": "# Eggy User\n\n- likes tea\n", "MEMORY.md": "# Eggy Memory\n\n- fact\n", "WATCH.md": "# Eggy Watch\n"} {
		if err := os.WriteFile(filepath.Join(layout.Memories(), name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrateAccountDocumentsCopiesVerifiesPublishesAndArchives(t *testing.T) {
	layout := At(t.TempDir())
	writeLegacyMemories(t, layout)
	phases := &recordingPhases{}
	if err := layout.MigrateAccountDocuments("nigel", phases); err != nil {
		t.Fatal(err)
	}
	if phases.phase != DocumentMigrationComplete {
		t.Fatalf("phase=%q", phases.phase)
	}
	dir, _ := layout.AccountMemories("nigel")
	body, err := os.ReadFile(filepath.Join(dir, "USER.md"))
	if err != nil || string(body) != "# Eggy User\n\n- likes tea\n" {
		t.Fatalf("copied USER.md=%q err=%v", body, err)
	}
	// The original is archived beside the home, never deleted: it is the
	// rollback.
	if _, err := os.Stat(layout.Memories()); !os.IsNotExist(err) {
		t.Fatalf("legacy memories still in place: %v", err)
	}
	if _, err := os.Stat(layout.Memories() + ".migrated/MEMORY.md"); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	// Idempotent: a second run on a complete migration changes nothing.
	if err := layout.MigrateAccountDocuments("nigel", phases); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateAccountDocumentsResumesAfterACopyThatWasNotArchived(t *testing.T) {
	layout := At(t.TempDir())
	writeLegacyMemories(t, layout)
	phases := &recordingPhases{}
	if err := layout.MigrateAccountDocuments("nigel", phases); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash between publish and archive: restore the original and
	// roll the record back one phase.
	if err := os.Rename(layout.Memories()+".migrated", layout.Memories()); err != nil {
		t.Fatal(err)
	}
	phases.phase = DocumentMigrationCopied
	if err := layout.MigrateAccountDocuments("nigel", phases); err != nil {
		t.Fatalf("resume after copy: %v", err)
	}
	if phases.phase != DocumentMigrationComplete {
		t.Fatalf("phase=%q", phases.phase)
	}
	if _, err := os.Stat(layout.Memories()); !os.IsNotExist(err) {
		t.Fatal("original not archived on resume")
	}
}

func TestMigrateAccountDocumentsRefusesANonidenticalDestination(t *testing.T) {
	layout := At(t.TempDir())
	writeLegacyMemories(t, layout)
	dir, _ := layout.AccountMemories("nigel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "USER.md"), []byte("# Eggy User\n\n- something else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	phases := &recordingPhases{}
	if err := layout.MigrateAccountDocuments("nigel", phases); err == nil {
		t.Fatal("a destination holding different content must be refused")
	}
	if _, err := os.Stat(filepath.Join(layout.Memories(), "USER.md")); err != nil {
		t.Fatalf("original touched by a refused migration: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "USER.md")); string(body) != "# Eggy User\n\n- something else\n" {
		t.Fatalf("destination overwritten: %q", body)
	}
	// A byte-identical destination is a retry, and proceeds.
	if err := os.WriteFile(filepath.Join(dir, "USER.md"), []byte("# Eggy User\n\n- likes tea\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := layout.MigrateAccountDocuments("nigel", phases); err != nil {
		t.Fatalf("identical destination retry: %v", err)
	}
}

func TestMigrateAccountDocumentsDoesNothingWithoutLegacyDocuments(t *testing.T) {
	layout := At(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	phases := &recordingPhases{}
	if err := layout.MigrateAccountDocuments("nigel", phases); err != nil {
		t.Fatal(err)
	}
	if phases.phase != "" {
		t.Fatalf("a fresh home recorded a migration: %q", phases.phase)
	}
}
