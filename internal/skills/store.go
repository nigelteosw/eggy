// Package skills persists procedural skills. A skill is either a flat Markdown
// file (skills/<name>.md) or a directory (skills/<name>/SKILL.md) that may hold
// UTF-8 text reference files beside it. Each SKILL.md has a small YAML
// frontmatter block (name, description, an optional disabled flag and source,
// and any other keys, which are preserved) followed by the skill's
// instructions. There is no database and no bundled scripts or executable
// assets: a skill and its references are text the agent reads, never something
// Eggy executes, matching docs/adr/0005-procedural-skills.md.
package skills

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/fsutil/atomicfile"
	"github.com/nigelteosw/eggy/internal/fsutil/filelock"
	"github.com/nigelteosw/eggy/internal/ports"
	"gopkg.in/yaml.v3"
)

// namePattern matches the agentskills.io slug convention pi and Hermes both
// use: 1-64 lowercase letters, digits, or hyphens.
var namePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

const maxDescriptionBytes = 1024

var frontmatterPattern = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n\n?(.*)\z`)

// ValidateName reports whether name is a valid skill slug, safe to use as a
// filename with no further sanitization.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return errors.New("skill name must be 1-64 lowercase letters, digits, or hyphens")
	}
	return nil
}

// frontmatter holds the keys Eggy reads and writes. Source records where a
// marketplace skill came from. Extra keeps every other key a skill author or
// installer put in the block, so a rewrite never drops them.
type frontmatter struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Disabled    bool           `yaml:"disabled,omitempty"`
	Source      string         `yaml:"source,omitempty"`
	Extra       map[string]any `yaml:",inline"`
}

type Store struct {
	dir      string
	maxBytes int64
	mu       sync.Mutex
}

func Open(dir string, maxBytes int64) *Store {
	if maxBytes <= 0 {
		maxBytes = 32 << 10
	}
	return &Store{dir: dir, maxBytes: maxBytes}
}

// path is the flat-layout file for name. A directory skill's file is
// dir(name)/SKILL.md instead; locate decides which one a name uses.
func (s *Store) path(name string) string {
	return filepath.Join(s.dir, name+".md")
}

// lockKey is the path every write to a skill serializes on across processes.
// It names the flat file's stem and the directory alike, so the two layouts
// share one lock.
func (s *Store) lockKey(name string) string {
	return filepath.Join(s.dir, name)
}

// List returns every skill in the directory in name order, including disabled
// and invalid ones, so the owner can see and repair what is there. A skill
// that is oversized, unreadable, or malformed is listed with Invalid set to the
// reason rather than failing the whole listing. Model-facing filtering and the
// index budget are the service's job, not the store's. Entries whose names
// start with a dot are staging or replaced leftovers and are ignored.
func (s *Store) List(ctx context.Context) ([]ports.SkillSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stems := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		switch {
		case entry.IsDir():
			stems[name] = true
		case strings.HasSuffix(name, ".md"):
			stems[strings.TrimSuffix(name, ".md")] = true
		}
	}
	summaries := make([]ports.SkillSummary, 0, len(stems))
	for stem := range stems {
		front, _, err := s.load(stem)
		if err != nil {
			summaries = append(summaries, ports.SkillSummary{Name: stem, Invalid: err.Error()})
			continue
		}
		summaries = append(summaries, ports.SkillSummary{Name: front.Name, Description: front.Description, Disabled: front.Disabled, Source: front.Source})
	}
	slices.SortFunc(summaries, func(a, b ports.SkillSummary) int { return cmp.Compare(a.Name, b.Name) })
	return summaries, nil
}

func (s *Store) Read(ctx context.Context, name string) (ports.Skill, error) {
	if err := ValidateName(name); err != nil {
		return ports.Skill{}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.Skill{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, dir, err := s.locate(name)
	if err != nil {
		return ports.Skill{}, err
	}
	front, body, err := s.loadFile(file, name)
	if err != nil {
		return ports.Skill{}, err
	}
	var files []string
	if dir != "" {
		if files, err = referenceFiles(dir); err != nil {
			return ports.Skill{}, err
		}
	}
	return ports.Skill{Name: front.Name, Description: front.Description, Body: body, Disabled: front.Disabled, Source: front.Source, Files: files}, nil
}

// ReadFile returns one reference file of a directory skill. The path is
// checked before any file is touched: it must be relative, stay inside the
// skill directory, use an allowlisted extension, cross no symlink, and hold
// UTF-8 text no larger than maxReferenceBytes.
func (s *Store) ReadFile(ctx context.Context, name, file string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	clean, err := cleanReference(file)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, dir, err := s.locate(name)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", fmt.Errorf("skill %q is a flat skill and has no reference files", name)
	}
	path, err := statReference(dir, clean)
	if err != nil {
		return "", err
	}
	data, err := readLimited(path, maxReferenceBytes)
	if err != nil {
		return "", fmt.Errorf("reference %q: %w", clean, err)
	}
	if err := checkReferenceText(clean, data); err != nil {
		return "", err
	}
	return string(data), nil
}

// locate finds the file that holds name's SKILL text. dir is the skill's
// directory for a directory skill and empty for a flat one. A name present in
// both layouts, a directory with no SKILL.md, and a symlinked skill are errors
// rather than guesses. Nothing is created.
func (s *Store) locate(name string) (file, dir string, err error) {
	flat := s.path(name)
	dir = filepath.Join(s.dir, name)
	skill := filepath.Join(dir, skillFileName)
	dirInfo, err := lstatIfExists(dir)
	if err != nil {
		return "", "", err
	}
	if dirInfo != nil && dirInfo.Mode()&fs.ModeSymlink != 0 {
		return "", "", fmt.Errorf("skill %q is a symlink", name)
	}
	flatInfo, err := lstatIfExists(flat)
	if err != nil {
		return "", "", err
	}
	var skillInfo fs.FileInfo
	if dirInfo != nil && dirInfo.IsDir() {
		if skillInfo, err = lstatIfExists(skill); err != nil {
			return "", "", err
		}
	}
	switch {
	case flatInfo != nil && skillInfo != nil:
		return "", "", fmt.Errorf("both skills/%s.md and skills/%s/ exist", name, name)
	case skillInfo != nil:
		if !skillInfo.Mode().IsRegular() {
			return "", "", fmt.Errorf("skill %q: %s is not a regular file", name, skillFileName)
		}
		return skill, dir, nil
	case flatInfo != nil:
		if !flatInfo.Mode().IsRegular() {
			return "", "", fmt.Errorf("skill %q: file is not a regular file", name)
		}
		return flat, "", nil
	case dirInfo != nil && dirInfo.IsDir():
		return "", "", fmt.Errorf("skill directory %q has no %s", name, skillFileName)
	}
	return "", "", fmt.Errorf("%w: %q", ports.ErrSkillNotFound, name)
}

// load reads and validates the skill called name, whichever layout it uses.
func (s *Store) load(name string) (frontmatter, string, error) {
	file, _, err := s.locate(name)
	if err != nil {
		return frontmatter{}, "", err
	}
	return s.loadFile(file, name)
}

// loadFile reads and validates one skill file. The frontmatter name must match
// the name the skill is located under: a file whose identity disagrees with
// its name is refused rather than served under either one.
func (s *Store) loadFile(file, name string) (frontmatter, string, error) {
	data, err := readLimited(file, s.maxBytes)
	if err != nil {
		return frontmatter{}, "", fmt.Errorf("skill %q: %w", name, err)
	}
	front, body, err := parse(data)
	if err != nil {
		return frontmatter{}, "", err
	}
	if front.Name != name {
		return frontmatter{}, "", fmt.Errorf("frontmatter name %q does not match file name %q", front.Name, name)
	}
	return front, body, nil
}

// readLimited reads at most limit bytes and refuses anything longer. It reads
// through a LimitReader so a file that grows after it was stat'd still cannot
// allocate more than limit+1 bytes.
func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("exceeds size limit of %d bytes", limit)
	}
	return data, nil
}

// checkFields validates the SKILL.md fields shared by Write and WriteBundle.
func checkFields(name, description, body string) (string, string, error) {
	if err := ValidateName(name); err != nil {
		return "", "", err
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return "", "", errors.New("skill description is empty")
	}
	if len(description) > maxDescriptionBytes {
		return "", "", fmt.Errorf("skill description exceeds %d bytes", maxDescriptionBytes)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", errors.New("skill content is empty")
	}
	return description, body, nil
}

func unrewritable(name string, err error) error {
	return fmt.Errorf("skill %q cannot be rewritten until its file is repaired or removed: %w", name, err)
}

// Write creates or replaces a skill's SKILL.md. A file that already exists
// keeps its disabled state, so editing content never re-enables a skill the
// owner turned off. A directory skill keeps its reference files: only SKILL.md
// is rewritten.
func (s *Store) Write(ctx context.Context, name, description, body string) error {
	description, body, err := checkFields(name, description, body)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A new skill starts enabled with no source. An existing one keeps its
	// disabled flag, source, and unknown keys, which live in a file this write
	// is about to replace, so a file that cannot be read is refused rather
	// than overwritten: its contents are unknown.
	file, _, err := s.locate(name)
	var front frontmatter
	switch {
	case err == nil:
		existing, _, err := s.loadFile(file, name)
		if err != nil {
			return unrewritable(name, err)
		}
		front = existing
	case errors.Is(err, ports.ErrSkillNotFound):
		file = s.path(name)
	default:
		return unrewritable(name, err)
	}
	front.Name, front.Description = name, description
	data, err := render(front, body)
	if err != nil {
		return err
	}
	if int64(len(data)) > s.maxBytes {
		return fmt.Errorf("skill %q exceeds size limit of %d bytes", name, s.maxBytes)
	}
	return s.commit(name, file, data)
}

// SetDisabled changes only the owner's enabled choice. The content is left as
// it is, and a file that cannot be read is not rewritten.
func (s *Store) SetDisabled(ctx context.Context, name string, disabled bool) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, _, err := s.locate(name)
	if err != nil {
		return err
	}
	front, body, err := s.loadFile(file, name)
	if err != nil {
		return err
	}
	front.Disabled = disabled
	data, err := render(front, body)
	if err != nil {
		return err
	}
	return s.commit(name, file, data)
}

// WriteBundle writes a whole skill, its SKILL.md and reference files, as one
// directory. Every check runs before anything touches the disk. The directory
// is staged under a dot name and then renamed into place, so a failure leaves
// the previous skill, or nothing, behind. A replace keeps the old skill's
// disabled state.
func (s *Store) WriteBundle(ctx context.Context, b ports.SkillBundle) error {
	description, body, err := checkFields(b.Name, b.Description, b.Body)
	if err != nil {
		return err
	}
	for key := range b.Extra {
		if slices.Contains(reservedFrontKeys, key) {
			return fmt.Errorf("extra frontmatter key %q is managed by Eggy", key)
		}
	}
	files, err := cleanBundleFiles(b.Files)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, dir, err := s.locate(b.Name)
	present := err == nil || !errors.Is(err, ports.ErrSkillNotFound)
	if present && !b.Replace {
		return fmt.Errorf("%w: %q", ports.ErrSkillExists, b.Name)
	}
	front := frontmatter{Name: b.Name, Description: description, Source: b.Source, Extra: b.Extra}
	original := ""
	if present {
		existing, _, err := s.loadFile(file, b.Name)
		if err != nil {
			return fmt.Errorf("skill %q cannot be replaced until its files are repaired or removed: %w", b.Name, err)
		}
		front.Disabled = existing.Disabled
		original = dir
		if original == "" {
			original = file
		}
	}
	data, err := render(front, body)
	if err != nil {
		return err
	}
	if int64(len(data)) > s.maxBytes {
		return fmt.Errorf("skill %q exceeds size limit of %d bytes", b.Name, s.maxBytes)
	}
	total := len(data)
	for _, content := range files {
		total += len(content)
	}
	if total > maxSkillDirBytes {
		return fmt.Errorf("skill %q exceeds the %d byte directory limit", b.Name, maxSkillDirBytes)
	}
	return filelock.With(s.lockKey(b.Name), func() error {
		return s.placeBundle(b.Name, original, data, files)
	})
}

// placeBundle stages the directory and renames it into place. When original is
// set, the skill being replaced is moved aside first and restored if the
// rename fails. The caller holds the store mutex and the skill's file lock.
func (s *Store) placeBundle(name, original string, data []byte, files map[string]string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	stage := filepath.Join(s.dir, ".stage-"+name+"-"+suffix)
	if err := os.Mkdir(stage, 0o700); err != nil {
		return err
	}
	if err := writeTree(stage, data, files); err != nil {
		os.RemoveAll(stage)
		return err
	}
	old := ""
	if original != "" {
		old = filepath.Join(s.dir, ".old-"+name+"-"+suffix)
		if err := os.Rename(original, old); err != nil {
			os.RemoveAll(stage)
			return err
		}
	}
	if err := os.Rename(stage, filepath.Join(s.dir, name)); err != nil {
		if old != "" {
			os.Rename(old, original)
		}
		os.RemoveAll(stage)
		return err
	}
	if old != "" {
		os.RemoveAll(old)
	}
	return nil
}

func writeTree(root string, skillData []byte, files map[string]string) error {
	if err := os.WriteFile(filepath.Join(root, skillFileName), skillData, 0o600); err != nil {
		return err
	}
	for file, content := range files {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// commit atomically replaces one skill file under the skill's file lock.
func (s *Store) commit(name, file string, data []byte) error {
	return filelock.With(s.lockKey(name), func() error {
		return atomicfile.Write(file, data, 0o600)
	})
}

// Delete removes a skill in either layout, including its reference files.
func (s *Store) Delete(ctx context.Context, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(s.lockKey(name), func() error {
		removed := false
		for _, target := range []string{s.path(name), filepath.Join(s.dir, name)} {
			info, err := os.Lstat(target)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if info.IsDir() {
				err = os.RemoveAll(target)
			} else {
				err = os.Remove(target)
			}
			if err != nil {
				return err
			}
			removed = true
		}
		if !removed {
			return fmt.Errorf("%w: %q", ports.ErrSkillNotFound, name)
		}
		return nil
	})
}

func parse(data []byte) (frontmatter, string, error) {
	matches := frontmatterPattern.FindSubmatch(data)
	if matches == nil {
		return frontmatter{}, "", errors.New("missing YAML frontmatter")
	}
	var front frontmatter
	if err := yaml.Unmarshal(matches[1], &front); err != nil {
		return frontmatter{}, "", fmt.Errorf("parse frontmatter: %w", err)
	}
	if err := ValidateName(front.Name); err != nil {
		return frontmatter{}, "", err
	}
	if strings.TrimSpace(front.Description) == "" {
		return frontmatter{}, "", errors.New("skill description is empty")
	}
	body := strings.TrimRight(string(matches[2]), "\n")
	return front, body, nil
}

func render(front frontmatter, body string) ([]byte, error) {
	encoded, err := yaml.Marshal(front)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(encoded)
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return []byte(b.String()), nil
}
