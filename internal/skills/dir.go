package skills

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// A directory skill is skills/<name>/SKILL.md plus optional reference files
// beside it. Reference files are UTF-8 text the agent may read through
// skill_read; Eggy never executes them, so the allowlist is about what the
// agent can be handed, not what can run.
const (
	skillFileName     = "SKILL.md"
	maxReferenceBytes = 64 << 10
	maxSkillDirBytes  = 256 << 10
	maxSkillDirFiles  = 64
)

var referenceExtensions = []string{".md", ".txt", ".json", ".yaml", ".yml", ".csv"}

// reservedFrontKeys are the frontmatter keys Eggy writes itself. An extra key
// with one of these names would render as a duplicate YAML key.
var reservedFrontKeys = []string{"name", "description", "disabled", "source"}

func isReferenceExt(file string) bool {
	return slices.Contains(referenceExtensions, strings.ToLower(filepath.Ext(file)))
}

// cleanReference validates a reference path and returns its cleaned form. It
// refuses absolute paths, anything that escapes the skill directory, the
// reserved SKILL.md, and extensions outside the allowlist.
func cleanReference(file string) (string, error) {
	if file == "" || filepath.IsAbs(file) {
		return "", fmt.Errorf("reference path %q must be relative", file)
	}
	clean := filepath.Clean(file)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("reference path %q escapes the skill directory", file)
	}
	if clean == skillFileName {
		return "", fmt.Errorf("%s is reserved for the skill itself", skillFileName)
	}
	if !isReferenceExt(clean) {
		return "", fmt.Errorf("reference %q has an extension Eggy does not read (allowed: %s)", clean, strings.Join(referenceExtensions, " "))
	}
	return clean, nil
}

// checkReferenceText enforces the per-file size bound and UTF-8 text rule.
func checkReferenceText(clean string, content []byte) error {
	if len(content) > maxReferenceBytes {
		return fmt.Errorf("reference %q exceeds size limit of %d bytes", clean, maxReferenceBytes)
	}
	if !utf8.Valid(content) {
		return fmt.Errorf("reference %q is not UTF-8 text", clean)
	}
	return nil
}

// cleanBundleFiles validates every reference file in a bundle and returns them
// keyed by cleaned path. The directory total is checked by the caller, which
// also knows the SKILL.md size.
func cleanBundleFiles(files map[string]string) (map[string]string, error) {
	if len(files)+1 > maxSkillDirFiles {
		return nil, fmt.Errorf("skill directory holds at most %d files including %s", maxSkillDirFiles, skillFileName)
	}
	cleaned := make(map[string]string, len(files))
	for file, content := range files {
		clean, err := cleanReference(file)
		if err != nil {
			return nil, err
		}
		if _, dup := cleaned[clean]; dup {
			return nil, fmt.Errorf("reference %q appears more than once", clean)
		}
		if err := checkReferenceText(clean, []byte(content)); err != nil {
			return nil, err
		}
		cleaned[clean] = content
	}
	return cleaned, nil
}

// statReference resolves a cleaned reference path inside dir. Every component
// must be a real directory or, for the last one, a regular file: a symlink
// anywhere on the way is refused. Because clean has already been checked to
// stay relative and inside, the joined path cannot leave dir.
func statReference(dir, clean string) (string, error) {
	parts := strings.Split(clean, string(filepath.Separator))
	cur := dir
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("reference %q is a symlink", clean)
		}
		last := i == len(parts)-1
		if last && !info.Mode().IsRegular() {
			return "", fmt.Errorf("reference %q is not a regular file", clean)
		}
		if !last && !info.IsDir() {
			return "", fmt.Errorf("reference %q is not inside a directory", clean)
		}
	}
	return cur, nil
}

// referenceFiles lists the reference files of a directory skill, sorted,
// relative to the skill directory. SKILL.md, symlinks, and files with an
// extension outside the allowlist are left out.
func referenceFiles(dir string) ([]string, error) {
	// WalkDir does not follow a symlinked root, so walk the resolved directory
	// when the skill directory is itself a symlink.
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == skillFileName || !isReferenceExt(rel) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	return files, nil
}

// statIfExists follows symlinks and returns nil and no error when path does not
// exist, so callers can tell absence from failure without matching error text.
func statIfExists(path string) (fs.FileInfo, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return info, err
}

func randomSuffix() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// recoverLeftovers repairs what an interrupted replace left behind. It runs
// under the store mutex, which every replace also holds, so no replace is in
// progress: a staging directory is stale, and a skill that was moved aside
// without its new copy arriving is put back. Only names placeBundle itself
// produces are touched, so a dot-file the owner made is left alone. It reports
// whether anything changed.
func (s *Store) recoverLeftovers(entries []os.DirEntry) bool {
	changed := false
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(s.dir, name)
		if skill, ok := leftoverSkillName(name, ".stage-"); ok {
			if err := os.RemoveAll(path); err != nil {
				slog.Warn("skills: could not remove an interrupted staging directory", "skill", skill, "error", err)
				continue
			}
			changed = true
			continue
		}
		skill, ok := leftoverSkillName(name, ".old-")
		if !ok {
			continue
		}
		if s.skillPresent(skill) {
			// The new copy landed before the old one was removed.
			if err := os.RemoveAll(path); err != nil {
				slog.Warn("skills: could not remove a replaced skill", "skill", skill, "error", err)
				continue
			}
			changed = true
			continue
		}
		target := filepath.Join(s.dir, skill)
		if !entry.IsDir() {
			target += ".md"
		}
		if err := os.Rename(path, target); err != nil {
			slog.Warn("skills: could not restore a skill after an interrupted replace", "skill", skill, "error", err)
			continue
		}
		slog.Info("skills: restored a skill after an interrupted replace", "skill", skill)
		changed = true
	}
	return changed
}

// leftoverSkillName parses "<prefix><skill>-<16 hex>", the only shape
// placeBundle writes, and checks the skill name is valid.
func leftoverSkillName(entry, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(entry, prefix)
	if !ok || len(rest) < 18 || rest[len(rest)-17] != '-' {
		return "", false
	}
	if _, err := hex.DecodeString(rest[len(rest)-16:]); err != nil {
		return "", false
	}
	skill := rest[:len(rest)-17]
	if ValidateName(skill) != nil {
		return "", false
	}
	return skill, true
}

func (s *Store) skillPresent(name string) bool {
	if _, err := os.Lstat(filepath.Join(s.dir, name)); err == nil {
		return true
	}
	_, err := os.Lstat(filepath.Join(s.dir, name+".md"))
	return err == nil
}
