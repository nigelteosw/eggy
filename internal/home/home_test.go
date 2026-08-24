package home

import (
	"os"
	"path/filepath"
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
	if got := Resolve("", env(nil)); got.Root != DefaultRoot {
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
