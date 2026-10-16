// Package skills persists procedural skills as flat Markdown files, one per
// skill, each with a small YAML frontmatter block (name, description, an
// optional disabled flag and source, and any other keys, which are preserved)
// followed by the skill's instructions. There is no
// database and no bundled scripts/assets: a skill is text the agent reads,
// never something Eggy executes, matching docs/adr/0005-procedural-skills.md.
package skills

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
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

func (s *Store) path(name string) string {
	return filepath.Join(s.dir, name+".md")
}

// List returns every skill file in the directory in name order, including
// disabled and invalid ones, so the owner can see and repair what is there.
// A file that is oversized, unreadable, or malformed is listed with Invalid
// set to the reason rather than failing the whole listing. Model-facing
// filtering and the index budget are the service's job, not the store's.
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
	summaries := make([]ports.SkillSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".md")
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
	front, body, err := s.load(name)
	if err != nil {
		return ports.Skill{}, err
	}
	return ports.Skill{Name: front.Name, Description: front.Description, Body: body, Disabled: front.Disabled, Source: front.Source}, nil
}

// load reads and validates one skill file. The frontmatter name must match
// the file it lives in: a file whose identity disagrees with its name is
// refused rather than served under either one.
func (s *Store) load(name string) (frontmatter, string, error) {
	data, err := readLimited(s.path(name), s.maxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return frontmatter{}, "", fmt.Errorf("%w: %q", ports.ErrSkillNotFound, name)
	}
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

// Write creates or replaces a skill. A file that already exists keeps its
// disabled state, so editing content never re-enables a skill the owner
// turned off.
func (s *Store) Write(ctx context.Context, name, description, body string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return errors.New("skill description is empty")
	}
	if len(description) > maxDescriptionBytes {
		return fmt.Errorf("skill description exceeds %d bytes", maxDescriptionBytes)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("skill content is empty")
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
	var front frontmatter
	existing, _, err := s.load(name)
	switch {
	case err == nil:
		front = existing
	case errors.Is(err, ports.ErrSkillNotFound):
	default:
		return fmt.Errorf("skill %q cannot be rewritten until its file is repaired or removed: %w", name, err)
	}
	front.Name, front.Description = name, description
	data, err := render(front, body)
	if err != nil {
		return err
	}
	if int64(len(data)) > s.maxBytes {
		return fmt.Errorf("skill %q exceeds size limit of %d bytes", name, s.maxBytes)
	}
	path := s.path(name)
	return filelock.With(path, func() error {
		return atomicfile.Write(path, data, 0o600)
	})
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
	front, body, err := s.load(name)
	if err != nil {
		return err
	}
	front.Disabled = disabled
	data, err := render(front, body)
	if err != nil {
		return err
	}
	path := s.path(name)
	return filelock.With(path, func() error {
		return atomicfile.Write(path, data, 0o600)
	})
}

func (s *Store) Delete(ctx context.Context, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(name)
	return filelock.With(path, func() error {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%w: %q", ports.ErrSkillNotFound, name)
			}
			return err
		}
		return os.Remove(path)
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
