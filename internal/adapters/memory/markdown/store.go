package markdown

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/adapters/filelock"
)

const initialMemory = "# Eggy Memory\n"

var sectionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,79}$`)

type Store struct {
	path string
	mu   sync.Mutex
}

func Open(path string) *Store { return &Store{path: path} }

func (s *Store) Load(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var content string
	err := filelock.With(s.path, func() error { var err error; content, err = s.loadUnlocked(); return err })
	return content, err
}

func (s *Store) loadUnlocked() (string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeAtomic(s.path, []byte(initialMemory)); err != nil {
			return "", err
		}
		return initialMemory, nil
	}
	if err != nil {
		return "", fmt.Errorf("read memory: %w", err)
	}
	return string(data), nil
}

func validateEdit(section, content string) error {
	if !sectionPattern.MatchString(section) {
		return errors.New("memory section must be a plain heading")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("memory content is empty")
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			return errors.New("memory content cannot create headings")
		}
	}
	return nil
}

func (s *Store) Append(ctx context.Context, section, content string) error {
	if err := validateEdit(section, content); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(s.path, func() error {
		current, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		heading := "## " + section
		trimmed := strings.TrimSpace(content)
		if sectionBounds(current, heading) == nil {
			current = strings.TrimRight(current, "\n") + "\n\n" + heading + "\n\n" + trimmed + "\n"
		} else {
			bounds := sectionBounds(current, heading)
			insert := bounds[1]
			current = strings.TrimRight(current[:insert], "\n") + "\n\n" + trimmed + "\n" + current[insert:]
		}
		return writeAtomic(s.path, []byte(current))
	})
}

func (s *Store) ReplaceSection(ctx context.Context, section, content string) error {
	if err := validateEdit(section, content); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(s.path, func() error {
		current, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		heading := "## " + section
		bounds := sectionBounds(current, heading)
		replacement := heading + "\n\n" + strings.TrimSpace(content) + "\n"
		if bounds == nil {
			current = strings.TrimRight(current, "\n") + "\n\n" + replacement
		} else {
			current = current[:bounds[0]] + replacement + current[bounds[1]:]
		}
		return writeAtomic(s.path, []byte(current))
	})
}

func sectionBounds(document, heading string) []int {
	start := strings.Index(document, heading+"\n")
	if start < 0 {
		return nil
	}
	rest := document[start+len(heading)+1:]
	next := strings.Index(rest, "\n## ")
	end := len(document)
	if next >= 0 {
		end = start + len(heading) + 1 + next + 1
	}
	return []int{start, end}
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".MEMORY.md-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err == nil {
		err = d.Sync()
		_ = d.Close()
	}
	return err
}
