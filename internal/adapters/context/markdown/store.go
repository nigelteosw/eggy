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
	"github.com/nigelteosw/eggy/internal/ports"
)

const (
	initialSoul   = "# Eggy Soul\n\nBe practical, truthful, concise, and evidence-led.\n"
	initialUser   = "# Eggy User\n"
	initialMemory = "# Eggy Memory\n"
)

var sectionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,79}$`)

type Store struct {
	dir      string
	maxBytes int64
	mu       sync.Mutex
}

func Open(dir string, maxBytes int64) *Store {
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	return &Store{dir: dir, maxBytes: maxBytes}
}

func (s *Store) Load(ctx context.Context) (ports.AgentContext, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentContext{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	soul, err := s.loadDocument("SOUL.md", initialSoul)
	if err != nil {
		return ports.AgentContext{}, err
	}
	user, err := s.loadDocument("USER.md", initialUser)
	if err != nil {
		return ports.AgentContext{}, err
	}
	memory, err := s.loadDocument("MEMORY.md", initialMemory)
	if err != nil {
		return ports.AgentContext{}, err
	}
	return ports.AgentContext{Soul: soul, User: user, Memory: memory, MaxBytes: s.maxBytes}, nil
}

func (s *Store) Append(ctx context.Context, document ports.ContextDocument, section, content string) error {
	return s.edit(ctx, document, section, content, false)
}

func (s *Store) ReplaceSection(ctx context.Context, document ports.ContextDocument, section, content string) error {
	return s.edit(ctx, document, section, content, true)
}

// RemoveSection deletes one section (its heading and body) entirely. It
// errors if the section does not exist, so a caller cannot silently no-op a
// removal it believes succeeded.
func (s *Store) RemoveSection(ctx context.Context, document ports.ContextDocument, section string) error {
	if !sectionPattern.MatchString(section) {
		return errors.New("context section must be a plain heading")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name, initial, err := editableDocument(document)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, name)
	return filelock.With(path, func() error {
		current, err := s.loadDocumentUnlocked(path, initial)
		if err != nil {
			return err
		}
		heading := "## " + section
		bounds := sectionBounds(current, heading)
		if bounds == nil {
			return fmt.Errorf("section %q does not exist", section)
		}
		current = strings.TrimRight(current[:bounds[0]]+current[bounds[1]:], "\n") + "\n"
		return writeAtomic(path, []byte(current))
	})
}

func (s *Store) edit(ctx context.Context, document ports.ContextDocument, section, content string, replace bool) error {
	if err := validateEdit(section, content); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name, initial, err := editableDocument(document)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, name)
	return filelock.With(path, func() error {
		current, err := s.loadDocumentUnlocked(path, initial)
		if err != nil {
			return err
		}
		heading := "## " + section
		trimmed := strings.TrimSpace(content)
		bounds := sectionBounds(current, heading)
		if bounds == nil {
			current = strings.TrimRight(current, "\n") + "\n\n" + heading + "\n\n" + trimmed + "\n"
		} else if replace {
			current = current[:bounds[0]] + heading + "\n\n" + trimmed + "\n" + current[bounds[1]:]
		} else {
			current = strings.TrimRight(current[:bounds[1]], "\n") + "\n\n" + trimmed + "\n" + current[bounds[1]:]
		}
		if int64(len(current)) > s.maxBytes {
			return fmt.Errorf("%s exceeds context limit of %d bytes", name, s.maxBytes)
		}
		return writeAtomic(path, []byte(current))
	})
}

func editableDocument(document ports.ContextDocument) (string, string, error) {
	switch document {
	case ports.ContextUser:
		return "USER.md", initialUser, nil
	case ports.ContextMemory:
		return "MEMORY.md", initialMemory, nil
	default:
		return "", "", errors.New("context document is read-only")
	}
}

func (s *Store) loadDocument(name, initial string) (string, error) {
	path := filepath.Join(s.dir, name)
	var content string
	err := filelock.With(path, func() error {
		var err error
		content, err = s.loadDocumentUnlocked(path, initial)
		return err
	})
	return content, err
}

func (s *Store) loadDocumentUnlocked(path, initial string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeAtomic(path, []byte(initial)); err != nil {
			return "", err
		}
		return initial, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if int64(len(data)) > s.maxBytes {
		return "", fmt.Errorf("%s exceeds context limit of %d bytes", filepath.Base(path), s.maxBytes)
	}
	return string(data), nil
}

func validateEdit(section, content string) error {
	if !sectionPattern.MatchString(section) {
		return errors.New("context section must be a plain heading")
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("context content is empty")
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			return errors.New("context content cannot create headings")
		}
	}
	return nil
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
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-")
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
	directory, err := os.Open(dir)
	if err == nil {
		err = directory.Sync()
		_ = directory.Close()
	}
	return err
}
