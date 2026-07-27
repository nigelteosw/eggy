package skills

import (
	"context"
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

func TestStoreRejectsMalformedFileOnLoad(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 32<<10)
	if err := os.WriteFile(filepath.Join(dir, "no-frontmatter.md"), []byte("just body text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(context.Background()); err == nil {
		t.Fatal("expected error listing a directory containing a malformed skill file")
	}
	if _, err := store.Read(context.Background(), "no-frontmatter"); err == nil {
		t.Fatal("expected error reading a malformed skill file")
	}
}
