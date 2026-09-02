package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configWithoutTracing is a config written before the tracing section existed:
// valid, current in every other respect, and silent about a setting this build
// reads.
func configWithoutTracing() string {
	return strings.Replace(validConfig(), "tracing:\n  keep_turns: 500\n", "", 1)
}

func TestBackfillAddsANewSectionAnOlderConfigNeverNamed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(configWithoutTracing()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets())); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "tracing:") {
		t.Fatalf("the new section was not written into the owner's config:\n%s", body)
	}
	// Written at the defaults, and introduced, so the owner can see what
	// appeared and what it is for rather than finding a bare block.
	for _, want := range []string{"enabled: true", "keep_turns: 500", "max_body_bytes: 1048576", "# The turn trace"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("backfilled section is missing %q:\n%s", want, body)
		}
	}
}

// The invariant that makes backfilling safe to do unasked: it writes the
// defaults that were already in force, so the file starts describing a setting
// that was already true rather than changing one.
func TestBackfillPreservesMeaning(t *testing.T) {
	before, _, err := loadText(t, configWithoutTracing(), testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(configWithoutTracing()), 0o600); err != nil {
		t.Fatal(err)
	}
	after, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	if before.Tracing.Active() != after.Tracing.Active() {
		t.Fatalf("backfill changed whether tracing runs: %v -> %v", before.Tracing.Active(), after.Tracing.Active())
	}
	if before.Tracing.KeepTurns != after.Tracing.KeepTurns ||
		before.Tracing.Retention != after.Tracing.Retention ||
		before.Tracing.MaxBodyBytes != after.Tracing.MaxBodyBytes {
		t.Fatalf("backfill changed the effective settings:\n%+v\n%+v", before.Tracing, after.Tracing)
	}
}

// A section the owner already wrote is theirs, whatever is in it. Backfilling
// over a deliberate choice would be the opposite of the point.
func TestBackfillLeavesAnOwnerWrittenSectionAlone(t *testing.T) {
	for name, tracing := range map[string]string{
		"switched off": "tracing:\n  enabled: false\n",
		"customized":   "tracing:\n  keep_turns: 5\n",
		// An empty block is a deliberate "I know about this and want the
		// defaults", not an omission to be filled in.
		"empty block": "tracing:\n",
	} {
		t.Run(name, func(t *testing.T) {
			body := configWithoutTracing() + tracing
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets())); err != nil {
				t.Fatal(err)
			}
			assertFileBytes(t, path, []byte(body))
		})
	}
}

// The owner's file is theirs: a backfill appends one section and changes
// nothing else, comments included.
func TestBackfillPreservesTheRestOfTheFile(t *testing.T) {
	body := strings.Replace(configWithoutTracing(), "data_dir: /data", "# the volume Railway mounts\ndata_dir: /data", 1)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets())); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "# the volume Railway mounts") {
		t.Fatalf("an owner comment was lost:\n%s", written)
	}
	if !strings.Contains(string(written), "deepseek-v4-pro") {
		t.Fatalf("existing settings were lost:\n%s", written)
	}
}

// A generated config and an upgraded one should describe the same settings, so
// a first boot does not leave the owner with a file that is already behind.
func TestFirstBootConfigAlsoCarriesTheDefaultedSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := LoadOrCreateConfig(path, mapEnv(firstBootEnv())); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "tracing:") {
		t.Fatalf("a freshly generated config does not name tracing:\n%s", body)
	}
}

// Every entry has to render, or the section it describes silently never
// reaches an owner's config.
func TestEveryDefaultedSectionRenders(t *testing.T) {
	for _, section := range defaultedSections {
		key, value, err := sectionNodes(section)
		if err != nil {
			t.Fatalf("%s: %v", section.key, err)
		}
		if key.HeadComment == "" {
			t.Fatalf("%s has no introductory comment", section.key)
		}
		if len(value.Content) == 0 {
			t.Fatalf("%s rendered an empty section", section.key)
		}
	}
}
