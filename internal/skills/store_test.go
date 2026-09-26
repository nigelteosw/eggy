package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreWriteReadListDelete(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 32<<10)
	ctx := context.Background()

	summaries, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Fatalf("expected no skills yet, got %#v", summaries)
	}

	if err := store.Write(ctx, "fix-flaky-tests", "Use when a test intermittently fails", "1. Rerun with -count=10\n2. Look for shared state"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "fix-flaky-tests.md"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info, err)
	}

	skill, err := store.Read(ctx, "fix-flaky-tests")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "fix-flaky-tests" || skill.Description != "Use when a test intermittently fails" || !strings.Contains(skill.Body, "Rerun with -count=10") {
		t.Fatalf("skill=%#v", skill)
	}

	summaries, err = store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Name != "fix-flaky-tests" || summaries[0].Description != skill.Description {
		t.Fatalf("summaries=%#v", summaries)
	}

	// Write again with the same name replaces the whole file.
	if err := store.Write(ctx, "fix-flaky-tests", "Updated description", "New body"); err != nil {
		t.Fatal(err)
	}
	skill, err = store.Read(ctx, "fix-flaky-tests")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Description != "Updated description" || skill.Body != "New body" {
		t.Fatalf("skill after rewrite=%#v", skill)
	}

	if err := store.Delete(ctx, "fix-flaky-tests"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ctx, "fix-flaky-tests"); err == nil {
		t.Fatal("expected error reading a deleted skill")
	}
	if err := store.Delete(ctx, "fix-flaky-tests"); err == nil {
		t.Fatal("expected error deleting a skill that does not exist")
	}
}

func TestStoreRejectsInvalidNamesAndOversizedContent(t *testing.T) {
	store := Open(t.TempDir(), 32<<10)
	ctx := context.Background()

	for _, name := range []string{"", "Bad_Name", "-leading-hyphen", "UPPER", strings.Repeat("a", 65)} {
		if err := store.Write(ctx, name, "description", "body"); err == nil {
			t.Fatalf("expected error writing invalid name %q", name)
		}
	}

	if err := store.Write(ctx, "valid-name", "", "body"); err == nil {
		t.Fatal("expected error writing empty description")
	}
	if err := store.Write(ctx, "valid-name", "description", ""); err == nil {
		t.Fatal("expected error writing empty body")
	}
	if err := store.Write(ctx, "valid-name", strings.Repeat("d", maxDescriptionBytes+1), "body"); err == nil {
		t.Fatal("expected error writing oversized description")
	}

	small := Open(t.TempDir(), 16)
	if err := small.Write(ctx, "valid-name", "description", strings.Repeat("x", 32)); err == nil {
		t.Fatal("expected error writing content over the size limit")
	}
}

func TestStoreListSkipsMalformedFileButKeepsUsableSkills(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 32<<10)
	ctx := context.Background()
	if err := store.Write(ctx, "usable-skill", "Use when the other file is broken", "body"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "no-frontmatter.md"), []byte("just body text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad-yaml.md"), []byte("---\nname: [\n---\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	summaries, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Name != "usable-skill" {
		t.Fatalf("summaries=%#v", summaries)
	}
	if _, err := store.Read(ctx, "no-frontmatter"); err == nil {
		t.Fatal("expected error reading a malformed skill file")
	}
}

func TestStoreListSkipsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 512)
	ctx := context.Background()
	if err := store.Write(ctx, "small-skill", "Use when the file fits", "body"); err != nil {
		t.Fatal(err)
	}
	oversized := "---\nname: big-skill\ndescription: Use when the file is huge\n---\n\n" + strings.Repeat("x", 1024)
	if err := os.WriteFile(filepath.Join(dir, "big-skill.md"), []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}

	summaries, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Name != "small-skill" {
		t.Fatalf("summaries=%#v", summaries)
	}
}

func TestStoreListBoundsAggregateIndexSize(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 32<<10)
	ctx := context.Background()
	description := strings.Repeat("d", maxDescriptionBytes)
	for i := range 100 {
		name := fmt.Sprintf("skill-%03d", i)
		if err := store.Write(ctx, name, description, "body"); err != nil {
			t.Fatal(err)
		}
	}

	summaries, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	indexBytes := 0
	for _, summary := range summaries {
		indexBytes += len(summary.Name) + len(summary.Description) + 2
	}
	if indexBytes > maxIndexBytes {
		t.Fatalf("index of %d summaries is %d bytes, over the %d byte limit", len(summaries), indexBytes, maxIndexBytes)
	}
	if len(summaries) == 0 || len(summaries) == 100 {
		t.Fatalf("expected a bounded but non-empty index, got %d summaries", len(summaries))
	}
	// The cut is deterministic: directory order, so the earliest names survive.
	if summaries[0].Name != "skill-000" {
		t.Fatalf("summaries[0]=%#v", summaries[0])
	}
}
