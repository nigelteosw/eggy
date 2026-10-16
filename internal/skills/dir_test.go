package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func writeFlatSkill(t *testing.T, dir, name, description, body string) {
	t.Helper()
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeDirSkill(t *testing.T, dir, name, description, body string) string {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return skillDir
}

func TestListFindsBothLayouts(t *testing.T) {
	dir := t.TempDir()
	writeFlatSkill(t, dir, "a", "Flat skill", "steps")
	writeDirSkill(t, dir, "b", "Directory skill", "steps")

	summaries, err := Open(dir, 32<<10).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 || summaries[0].Name != "a" || summaries[1].Name != "b" {
		t.Fatalf("summaries=%#v", summaries)
	}
	for _, s := range summaries {
		if s.Invalid != "" {
			t.Fatalf("skill %q unexpectedly invalid: %s", s.Name, s.Invalid)
		}
	}
}

func TestBothLayoutsIsInvalid(t *testing.T) {
	dir := t.TempDir()
	writeFlatSkill(t, dir, "a", "Flat skill", "steps")
	writeDirSkill(t, dir, "a", "Directory skill", "steps")

	summaries, err := Open(dir, 32<<10).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Name != "a" || !strings.Contains(summaries[0].Invalid, "both") {
		t.Fatalf("summaries=%#v", summaries)
	}
}

func TestReadFileRefusesTraversal(t *testing.T) {
	dir := t.TempDir()
	skillDir := writeDirSkill(t, dir, "b", "Directory skill", "steps")
	if err := os.WriteFile(filepath.Join(skillDir, "reference.md"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := Open(dir, 32<<10)
	for _, file := range []string{"../x.md", "/etc/passwd", "a/../../x.md"} {
		if _, err := store.ReadFile(context.Background(), "b", file); err == nil {
			t.Fatalf("expected ReadFile to refuse %q", file)
		}
	}
}

func TestReadFileRefusesExtensionAndSymlink(t *testing.T) {
	dir := t.TempDir()
	skillDir := writeDirSkill(t, dir, "b", "Directory skill", "steps")
	if err := os.WriteFile(filepath.Join(skillDir, "run.py"), []byte("print(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(skillDir, "ref.md")); err != nil {
		t.Fatal(err)
	}
	store := Open(dir, 32<<10)
	if _, err := store.ReadFile(context.Background(), "b", "run.py"); err == nil {
		t.Fatal("expected a non-allowlisted extension to be refused")
	}
	if _, err := store.ReadFile(context.Background(), "b", "ref.md"); err == nil {
		t.Fatal("expected a symlinked reference to be refused")
	}
}

func TestReadFileBounded(t *testing.T) {
	dir := t.TempDir()
	skillDir := writeDirSkill(t, dir, "b", "Directory skill", "steps")
	store := Open(dir, 32<<10)
	ok := strings.Repeat("a", 64<<10)
	if err := os.WriteFile(filepath.Join(skillDir, "ok.md"), []byte(ok), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ReadFile(context.Background(), "b", "ok.md"); err != nil || len(got) != 64<<10 {
		t.Fatalf("64 KiB reference: len=%d err=%v", len(got), err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "big.md"), []byte(ok+"a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadFile(context.Background(), "b", "big.md"); err == nil {
		t.Fatal("expected 64 KiB+1 reference to be refused")
	}
}

func TestWriteBundleCreateOnly(t *testing.T) {
	store := Open(t.TempDir(), 32<<10)
	ctx := context.Background()
	bundle := ports.SkillBundle{Name: "c", Description: "Create once", Body: "steps"}
	if err := store.WriteBundle(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	err := store.WriteBundle(ctx, bundle)
	if !errors.Is(err, ports.ErrSkillExists) {
		t.Fatalf("expected ErrSkillExists, got %v", err)
	}
}

func TestWriteBundleReplaceKeepsDisabled(t *testing.T) {
	store := Open(t.TempDir(), 32<<10)
	ctx := context.Background()
	if err := store.WriteBundle(ctx, ports.SkillBundle{Name: "c", Description: "First", Body: "steps", Files: map[string]string{"old.md": "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDisabled(ctx, "c", true); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteBundle(ctx, ports.SkillBundle{Name: "c", Description: "Second", Body: "new steps", Files: map[string]string{"new.md": "y"}, Replace: true}); err != nil {
		t.Fatal(err)
	}
	skill, err := store.Read(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Disabled || skill.Description != "Second" || !reflect.DeepEqual(skill.Files, []string{"new.md"}) {
		t.Fatalf("skill after replace=%#v", skill)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "c", "old.md")); !os.IsNotExist(err) {
		t.Fatalf("old reference file survived replace: %v", err)
	}
}

func TestWriteBundleDirBounds(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 32<<10)
	ctx := context.Background()

	tooMany := map[string]string{}
	for i := 0; i < 64; i++ {
		tooMany[fmt.Sprintf("ref%02d.md", i)] = "x"
	}
	if err := store.WriteBundle(ctx, ports.SkillBundle{Name: "many", Description: "Many files", Body: "steps", Files: tooMany}); err == nil {
		t.Fatal("expected 65 files in one directory to be refused")
	}

	big := map[string]string{}
	chunk := strings.Repeat("a", 64<<10)
	for _, name := range []string{"one.md", "two.md", "three.md", "four.md"} {
		big[name] = chunk
	}
	if err := store.WriteBundle(ctx, ports.SkillBundle{Name: "heavy", Description: "Heavy", Body: "steps", Files: big}); err == nil {
		t.Fatal("expected a directory over 256 KiB to be refused")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("refused bundles left %d entries on disk", len(entries))
	}
}

func TestWriteBundleLeavesNoStagingOnFailure(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 32<<10)
	err := store.WriteBundle(context.Background(), ports.SkillBundle{Name: "bad", Description: "Bad file", Body: "steps", Files: map[string]string{"../x.md": "x"}})
	if err == nil {
		t.Fatal("expected invalid file path to be refused")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stage-") {
			t.Fatalf("staging directory %q left behind", entry.Name())
		}
	}
}

func TestDeleteRemovesDirectory(t *testing.T) {
	dir := t.TempDir()
	writeDirSkill(t, dir, "b", "Directory skill", "steps")
	if err := Open(dir, 32<<10).Delete(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
		t.Fatalf("skill directory survived delete: %v", err)
	}
}

func TestReadListsFiles(t *testing.T) {
	dir := t.TempDir()
	skillDir := writeDirSkill(t, dir, "b", "Directory skill", "steps")
	if err := os.MkdirAll(filepath.Join(skillDir, "examples"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"reference.md", "examples/fill.md"} {
		if err := os.WriteFile(filepath.Join(skillDir, rel), []byte("text"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	skill, err := Open(dir, 32<<10).Read(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(skill.Files, []string{"examples/fill.md", "reference.md"}) {
		t.Fatalf("files=%#v", skill.Files)
	}
}
