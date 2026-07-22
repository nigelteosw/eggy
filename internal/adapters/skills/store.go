// Package skills persists procedural skills as flat Markdown files, one per
// skill, each with a small YAML frontmatter block (name, description)
// followed by the skill's instructions. There is no database and no bundled
// scripts/assets: a skill is text the agent reads, never something Eggy
// executes, matching docs/adr/0005-procedural-skills.md.
package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/adapters/atomicfile"
	"github.com/nigelteosw/eggy/internal/adapters/filelock"
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
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		front, _, err := parse(data)
		if err != nil {
			return nil, fmt.Errorf("skill file %q: %w", entry.Name(), err)
		}
		summaries = append(summaries, ports.SkillSummary{Name: front.Name, Description: front.Description})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
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
