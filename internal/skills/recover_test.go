package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const (
	leftoverSuffix = "0123456789abcdef"
	pdfSkill       = "---\nname: pdf\ndescription: Fill PDF forms\n---\n\nbody\n"
)

func writeSkillDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(pdfSkill), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// A crash after the old skill was moved aside and before the new copy landed
// leaves it only under .old-. The next List puts it back.
func TestListRestoresASkillMovedAsideByAnInterruptedReplace(t *testing.T) {
	dir := t.TempDir()
	writeSkillDir(t, filepath.Join(dir, ".old-pdf-"+leftoverSuffix))
	got, err := Open(dir, 0).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "pdf" || got[0].Invalid != "" {
		t.Fatalf("list = %+v, want pdf restored", got)
	}
	if exists(filepath.Join(dir, ".old-pdf-"+leftoverSuffix)) || !exists(filepath.Join(dir, "pdf", "SKILL.md")) {
		t.Fatal("the moved-aside skill was not put back in place")
	}
}

func TestListRestoresAFlatSkillMovedAside(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".old-pdf-"+leftoverSuffix), []byte(pdfSkill), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Open(dir, 0).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "pdf" || !exists(filepath.Join(dir, "pdf.md")) {
		t.Fatalf("list = %+v, want pdf.md restored", got)
	}
}

// A crash after the new copy landed and before the old one was removed leaves a
// stale old copy. The new copy wins; the old one is removed.
func TestListDropsAStaleOldCopyWhenTheNewOneLanded(t *testing.T) {
	dir := t.TempDir()
	writeSkillDir(t, filepath.Join(dir, "pdf"))
	old := filepath.Join(dir, ".old-pdf-"+leftoverSuffix)
	writeSkillDir(t, old)
	if err := os.WriteFile(filepath.Join(old, "SKILL.md"), []byte("---\nname: pdf\ndescription: Old\n---\n\nold\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Open(dir, 0).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Description != "Fill PDF forms" {
		t.Fatalf("list = %+v, want the new copy", got)
	}
	if exists(old) {
		t.Fatal("stale old copy was left behind")
	}
}

func TestListRemovesAStaleStagingDirectory(t *testing.T) {
	dir := t.TempDir()
	stage := filepath.Join(dir, ".stage-pdf-"+leftoverSuffix)
	writeSkillDir(t, stage)
	got, err := Open(dir, 0).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || exists(stage) {
		t.Fatalf("list = %+v, stage left = %v; want an empty list and no stage", got, exists(stage))
	}
}

func TestListLeavesTheOwnersDotFilesAlone(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".old-notes", ".stage-draft", ".old-pdf-nothex0000000000", ".notes"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Open(dir, 0).List(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".old-notes", ".stage-draft", ".old-pdf-nothex0000000000", ".notes"} {
		if !exists(filepath.Join(dir, name)) {
			t.Fatalf("%s was removed; only placeBundle's own leftovers may be", name)
		}
	}
}

func TestListRestoresAMovedAsideSymlinkedDirectorySkill(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(t.TempDir(), "pdf-real")
	writeSkillDir(t, real)
	if err := os.Symlink(real, filepath.Join(dir, ".old-pdf-"+leftoverSuffix)); err != nil {
		t.Fatal(err)
	}
	got, err := Open(dir, 0).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "pdf" || got[0].Invalid != "" {
		t.Fatalf("list = %+v, want pdf restored", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "pdf")); err != nil || !info.IsDir() || exists(filepath.Join(dir, "pdf.md")) {
		t.Fatal("a symlinked directory skill was not restored as a directory")
	}
}
