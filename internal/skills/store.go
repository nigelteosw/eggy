// Package skills persists procedural skills as flat Markdown files, one per
// skill, each with a small YAML frontmatter block (name, description)
// followed by the skill's instructions. There is no database and no bundled
// scripts/assets: a skill is text the agent reads, never something Eggy
// executes, matching docs/adr/0005-procedural-skills.md.
package skills

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
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

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
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

// maxIndexBytes bounds the aggregate size of the summaries List returns.
// Every summary is resident in the system prompt on every turn (see
// agent.renderSkills), so the index is a context cost the owner never sees
// billed directly: cap it here rather than letting a skills directory grow
// the prompt without limit.
const maxIndexBytes = 16 << 10

// List returns the summaries of every readable skill, in name order. A file
// that is oversized, unreadable, or malformed is skipped with a warning
// instead of failing the whole listing, so one bad file cannot disable the
// skills the owner can still use. Once the summaries reach maxIndexBytes the
// remaining files are skipped, again with a warning naming them: an index at
// capacity is reported, never silently trimmed.
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
	indexBytes := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			slog.Warn("skipping unreadable skill file", "file", entry.Name(), "error", err)
			continue
		}
		if info.Size() > s.maxBytes {
			slog.Warn("skipping oversized skill file", "file", entry.Name(), "bytes", info.Size(), "limit", s.maxBytes)
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			slog.Warn("skipping unreadable skill file", "file", entry.Name(), "error", err)
			continue
		}
		front, _, err := parse(data)
		if err != nil {
			slog.Warn("skipping malformed skill file", "file", entry.Name(), "error", err)
			continue
		}
		cost := len(front.Name) + len(front.Description) + 2
		if indexBytes+cost > maxIndexBytes {
			slog.Warn("skill index is at capacity, skipping skill", "file", entry.Name(), "limit", maxIndexBytes)
			continue
		}
		indexBytes += cost
		summaries = append(summaries, ports.SkillSummary{Name: front.Name, Description: front.Description})
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
	data, err := s.readFile(name)
	if err != nil {
		return ports.Skill{}, err
	}
	front, body, err := parse(data)
	if err != nil {
		return ports.Skill{}, err
	}
	return ports.Skill{Name: front.Name, Description: front.Description, Body: body}, nil
}

func (s *Store) readFile(name string) ([]byte, error) {
	data, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("skill %q does not exist", name)
	}
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > s.maxBytes {
		return nil, fmt.Errorf("skill %q exceeds size limit of %d bytes", name, s.maxBytes)
	}
	return data, nil
}

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
	data, err := render(name, description, body)
	if err != nil {
		return err
	}
	if int64(len(data)) > s.maxBytes {
		return fmt.Errorf("skill %q exceeds size limit of %d bytes", name, s.maxBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
				return fmt.Errorf("skill %q does not exist", name)
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

func render(name, description, body string) ([]byte, error) {
	encoded, err := yaml.Marshal(frontmatter{Name: name, Description: description})
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
