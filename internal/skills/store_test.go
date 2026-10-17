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

func TestStoreListReportsMalformedFilesAsInvalidButKeepsUsableSkills(t *testing.T) {
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
	invalid := map[string]bool{}
	for _, summary := range summaries {
		if summary.Invalid != "" {
			invalid[summary.Name] = true
		} else if summary.Name != "usable-skill" {
			t.Fatalf("unexpected valid summary %#v", summary)
		}
	}
	if len(summaries) != 3 || !invalid["no-frontmatter"] || !invalid["bad-yaml"] {
		t.Fatalf("summaries=%#v", summaries)
	}
	if _, err := store.Read(ctx, "no-frontmatter"); err == nil {
		t.Fatal("expected error reading a malformed skill file")
	}
}

func TestStoreListReportsOversizedFileAsInvalid(t *testing.T) {
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
	if len(summaries) != 2 {
		t.Fatalf("summaries=%#v", summaries)
	}
	for _, summary := range summaries {
		if summary.Name == "big-skill" && summary.Invalid == "" {
			t.Fatalf("oversized file listed as valid: %#v", summary)
		}
		if summary.Name == "small-skill" && summary.Invalid != "" {
			t.Fatalf("small file listed as invalid: %#v", summary)
		}
	}
	if _, err := store.Read(ctx, "big-skill"); err == nil {
		t.Fatal("expected error reading an oversized skill")
	}
}

func TestStoreRejectsFrontmatterNameThatDoesNotMatchFile(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 32<<10)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "alpha.md"), []byte("---\nname: beta\ndescription: Use when alpha\n---\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ctx, "alpha"); err == nil {
		t.Fatal("expected error when frontmatter name differs from file name")
	}
	summaries, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Invalid == "" {
		t.Fatalf("summaries=%#v", summaries)
	}
}

func TestStoreDisabledFlagSurvivesRewriteAndToggle(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 32<<10)
	ctx := context.Background()
	if err := store.Write(ctx, "toggle-me", "Use when toggling", "steps"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDisabled(ctx, "toggle-me", true); err != nil {
		t.Fatal(err)
	}
	skill, err := store.Read(ctx, "toggle-me")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Disabled || skill.Body != "steps" {
		t.Fatalf("skill=%#v", skill)
	}
	summaries, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || !summaries[0].Disabled {
		t.Fatalf("summaries=%#v", summaries)
	}

	// Rewriting the content keeps the owner's disabled choice.
	if err := store.Write(ctx, "toggle-me", "Use when toggling, revised", "new steps"); err != nil {
		t.Fatal(err)
	}
	skill, err = store.Read(ctx, "toggle-me")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Disabled || skill.Body != "new steps" {
		t.Fatalf("after rewrite skill=%#v", skill)
	}

	if err := store.SetDisabled(ctx, "toggle-me", false); err != nil {
		t.Fatal(err)
	}
	skill, err = store.Read(ctx, "toggle-me")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Disabled || skill.Body != "new steps" {
		t.Fatalf("after enable skill=%#v", skill)
	}
	if err := store.SetDisabled(ctx, "missing-skill", true); err == nil {
		t.Fatal("expected error disabling a skill that does not exist")
	}
}

func TestReadLimitedNeverHoldsMoreThanLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grows.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLimited(path, 50); err == nil {
		t.Fatal("expected a file over the limit to be refused")
	}
	data, err := readLimited(path, 100)
	if err != nil || len(data) != 100 {
		t.Fatalf("len=%d err=%v", len(data), err)
	}
}

// A skill's disabled flag lives in the file a write replaces. When that file
// cannot be read the flag is unknown, so the write is refused and the file is
// left as it was, rather than re-enabling a skill the owner turned off.
func TestStoreWriteRefusesToOverwriteAnUnreadableSkill(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 32<<10)
	ctx := context.Background()
	if err := store.Write(ctx, "turned-off", "Use when turned off", "body"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDisabled(ctx, "turned-off", true); err != nil {
		t.Fatal(err)
	}
	broken := []byte("---\nname: [\n---\n\nbody\n")
	if err := os.WriteFile(filepath.Join(dir, "turned-off.md"), broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, "turned-off", "Use when turned off, revised", "new steps"); err == nil {
		t.Fatal("expected the unreadable skill to be refused")
	}
	after, err := os.ReadFile(filepath.Join(dir, "turned-off.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(broken) {
		t.Fatalf("refused write still changed the file: %q", after)
	}
}

func writeRaw(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readRaw(t *testing.T, dir, file string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const sourcedSkill = "---\nname: pdf\ndescription: Fill PDFs\nsource: anthropic-skills/pdf\nlicense: MIT\nallowed-tools: [Bash]\n---\n\nbody\n"

func TestWritePreservesSourceAndUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	writeRaw(t, dir, "pdf.md", sourcedSkill)
	s := Open(dir, 0)
	if err := s.Write(ctx, "pdf", "Fill PDF forms", "new body"); err != nil {
		t.Fatal(err)
	}
	got := readRaw(t, dir, "pdf.md")
	for _, want := range []string{"source: anthropic-skills/pdf", "license: MIT", "allowed-tools:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "source:") != 1 {
		t.Fatalf("known key duplicated into extras:\n%s", got)
	}
	sk, err := s.Read(ctx, "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if sk.Source != "anthropic-skills/pdf" || sk.Body != "new body" {
		t.Fatalf("%+v", sk)
	}
}

func TestSetDisabledPreservesSource(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	writeRaw(t, dir, "pdf.md", sourcedSkill)
	s := Open(dir, 0)
	if err := s.SetDisabled(ctx, "pdf", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(ctx, "pdf", false); err != nil {
		t.Fatal(err)
	}
	got := readRaw(t, dir, "pdf.md")
	for _, want := range []string{"source: anthropic-skills/pdf", "license: MIT", "allowed-tools:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("round trip lost %q:\n%s", want, got)
		}
	}
	sk, err := s.Read(ctx, "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if sk.Source != "anthropic-skills/pdf" || sk.Disabled {
		t.Fatalf("%+v", sk)
	}
}

func TestListReportsSource(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	writeRaw(t, dir, "pdf.md", sourcedSkill)
	s := Open(dir, 0)
	if err := s.Write(ctx, "hand", "Hand placed", "body"); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "hand" || list[0].Source != "" || list[1].Name != "pdf" || list[1].Source != "anthropic-skills/pdf" {
		t.Fatalf("list=%#v", list)
	}
}
