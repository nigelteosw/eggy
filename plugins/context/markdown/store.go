package markdown

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/atomicfile"
	"github.com/nigelteosw/eggy/plugins/filelock"
)

const (
	initialSoul   = "# Eggy Soul\n\nI'm Eggy: a small eggy buddy, happiest when quietly useful. Warm and a little playful, never sappy about it. Underneath the smile, still practical, truthful, concise, and evidence-led — say what's actually true, not what sounds nice.\n"
	initialUser   = "# Eggy User\n"
	initialMemory = "# Eggy Memory\n"
	initialWatch  = "# Eggy Watch\n"
)

// Default write budgets. They are deliberately small: a bounded document that
// errors on overflow forces the agent to consolidate, where a large one just
// accretes and is re-injected into every turn's prompt.
const (
	DefaultUserMaxBytes   = 2 << 10
	DefaultMemoryMaxBytes = 4 << 10
	// Deliberately the largest of the three: the watch list carries both the
	// items and Eggy's notes about what it already said, and the annotation is
	// the whole anti-repetition mechanism. Still bounded, for the reason the
	// other two are.
	DefaultWatchMaxBytes = 6 << 10
)

// Paths locates each context document explicitly, because they no longer
// share one directory: SOUL.md sits at the top of the home while MEMORY.md
// and USER.md live under
// memories/ (see internal/home).
type Paths struct {
	Soul   string
	User   string
	Memory string
	Watch  string
}

type Store struct {
	paths          Paths
	userMaxBytes   int64
	memoryMaxBytes int64
	watchMaxBytes  int64
	mu             sync.Mutex
}

func Open(paths Paths, userMaxBytes, memoryMaxBytes, watchMaxBytes int64) *Store {
	if userMaxBytes <= 0 {
		userMaxBytes = DefaultUserMaxBytes
	}
	if memoryMaxBytes <= 0 {
		memoryMaxBytes = DefaultMemoryMaxBytes
	}
	if watchMaxBytes <= 0 {
		watchMaxBytes = DefaultWatchMaxBytes
	}
	return &Store{paths: paths, userMaxBytes: userMaxBytes, memoryMaxBytes: memoryMaxBytes, watchMaxBytes: watchMaxBytes}
}

// InDir returns a store using Eggy's former flat layout, where every context
// document sat directly in one directory. Tests and any caller that only
// needs a scratch home keep using it.
func InDir(dir string, userMaxBytes, memoryMaxBytes int64) *Store {
	return Open(Paths{
		Soul:   filepath.Join(dir, "SOUL.md"),
		User:   filepath.Join(dir, "USER.md"),
		Memory: filepath.Join(dir, "MEMORY.md"),
		Watch:  filepath.Join(dir, "WATCH.md"),
	}, userMaxBytes, memoryMaxBytes, 0)
}

func (s *Store) Load(ctx context.Context) (ports.AgentContext, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentContext{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	soul, err := s.loadDocument(s.paths.Soul, initialSoul)
	if err != nil {
		return ports.AgentContext{}, err
	}
	user, err := s.loadDocument(s.paths.User, initialUser)
	if err != nil {
		return ports.AgentContext{}, err
	}
	memory, err := s.loadDocument(s.paths.Memory, initialMemory)
	if err != nil {
		return ports.AgentContext{}, err
	}
	watch, err := s.loadDocument(s.paths.Watch, initialWatch)
	if err != nil {
		return ports.AgentContext{}, err
	}
	return ports.AgentContext{
		Soul: soul, User: user, Memory: memory, Watch: watch,
		UserMaxBytes: s.userMaxBytes, MemoryMaxBytes: s.memoryMaxBytes, WatchMaxBytes: s.watchMaxBytes,
	}, nil
}

// AddEntry appends text to document as one entry.
func (s *Store) AddEntry(ctx context.Context, document ports.ContextDocument, text string) error {
	entry, err := normalizeEntry(text)
	if err != nil {
		return err
	}
	return s.rewrite(ctx, document, func(lines []string) ([]string, error) {
		return append(lines, entry), nil
	})
}

// ReplaceEntry rewrites the single entry containing oldText.
func (s *Store) ReplaceEntry(ctx context.Context, document ports.ContextDocument, oldText, text string) error {
	entry, err := normalizeEntry(text)
	if err != nil {
		return err
	}
	return s.rewrite(ctx, document, func(lines []string) ([]string, error) {
		index, err := matchEntry(lines, oldText)
		if err != nil {
			return nil, err
		}
		lines[index] = entry
		return lines, nil
	})
}

// RemoveEntry deletes the single entry containing oldText.
func (s *Store) RemoveEntry(ctx context.Context, document ports.ContextDocument, oldText string) error {
	return s.rewrite(ctx, document, func(lines []string) ([]string, error) {
		index, err := matchEntry(lines, oldText)
		if err != nil {
			return nil, err
		}
		return append(lines[:index], lines[index+1:]...), nil
	})
}

// ReplaceDocument overwrites document with content. Unlike the entry methods
// it does not preserve the existing header, because the caller supplied a
// whole document.
//
// The budget is enforced the same way rewrite enforces it, including the
// shrinking-edit escape hatch: a write that leaves the document no larger
// than it found it always proceeds, so a document already over budget can
// still be brought back under.
func (s *Store) ReplaceDocument(ctx context.Context, document ports.ContextDocument, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, initial, maxBytes, err := s.writableDocument(document)
	if err != nil {
		return err
	}
	updated := strings.TrimRight(content, "\n") + "\n"
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(path, func() error {
		current, err := s.loadDocumentUnlocked(path, initial)
		if err != nil {
			return err
		}
		if int64(len(updated)) > maxBytes && len(updated) >= len(current) {
			return fmt.Errorf("%s is full (%d/%d bytes): consolidate or remove entries before adding more", filepath.Base(path), len(updated), maxBytes)
		}
		return atomicfile.Write(path, []byte(updated), 0o600)
	})
}

// rewrite applies edit to document's entry lines under lock, then writes the
// result back if it still fits the document's budget. The budget is enforced
// on write only: a document that predates the budget still loads, and the
// first edit that would grow it further is what fails. Shrinking edits are
// always allowed, so an over-budget document can be brought back under.
func (s *Store) rewrite(ctx context.Context, document ports.ContextDocument, edit func([]string) ([]string, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, initial, maxBytes, err := s.writableDocument(document)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(path, func() error {
		current, err := s.loadDocumentUnlocked(path, initial)
		if err != nil {
			return err
		}
		header, lines := splitEntries(current)
		lines, err = edit(lines)
		if err != nil {
			return err
		}
		updated := joinEntries(header, lines)
		// An edit that shrinks the document always proceeds, even while it
		// stays over budget. Enforcing the ceiling on removals too would wedge
		// any document already above it -- including every one written before
		// the budget shrank -- by rejecting the only edits that could recover.
		if int64(len(updated)) > maxBytes && len(updated) >= len(current) {
			return fmt.Errorf("%s is full (%d/%d bytes): consolidate or remove entries before adding more", filepath.Base(path), len(updated), maxBytes)
		}
		return atomicfile.Write(path, []byte(updated), 0o600)
	})
}

func (s *Store) writableDocument(document ports.ContextDocument) (path, initial string, maxBytes int64, err error) {
	switch document {
	case ports.ContextUser:
		return s.paths.User, initialUser, s.userMaxBytes, nil
	case ports.ContextMemory:
		return s.paths.Memory, initialMemory, s.memoryMaxBytes, nil
	case ports.ContextWatch:
		return s.paths.Watch, initialWatch, s.watchMaxBytes, nil
	default:
		return "", "", 0, fmt.Errorf("context document %q is read-only", document)
	}
}

// splitEntries divides a document into its leading markdown header (the "#
// Title" line and anything before the first entry) and its entry lines.
//
// An entry is any non-blank line that is not a markdown heading. Only the
// leading run of headings and blank lines is kept as the header; a document
// written by the older section-based store is therefore flattened on its
// first edit, dropping every "## Section" line below the first entry and
// keeping the body lines as ordinary, matchable entries. That is deliberate:
// entries are addressed by substring, so section structure carries no
// meaning here and preserving it would only be decoration to maintain.
func splitEntries(document string) (string, []string) {
	var header strings.Builder
	var lines []string
	for _, line := range strings.Split(document, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(lines) == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			header.WriteString(line)
			header.WriteString("\n")
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, trimmed)
	}
	return strings.TrimRight(header.String(), "\n"), lines
}

func joinEntries(header string, lines []string) string {
	if len(lines) == 0 {
		return header + "\n"
	}
	return header + "\n\n" + strings.Join(lines, "\n") + "\n"
}

// matchEntry finds the one entry containing oldText. Ambiguity is an error
// rather than a first-match guess, so the agent is told to be more specific
// instead of silently editing the wrong entry.
func matchEntry(lines []string, oldText string) (int, error) {
	needle := strings.TrimSpace(oldText)
	if needle == "" {
		return 0, errors.New("old_text is required")
	}
	found := -1
	count := 0
	for index, line := range lines {
		if strings.Contains(line, needle) {
			if count == 0 {
				found = index
			}
			count++
		}
	}
	switch {
	case count == 0:
		return 0, fmt.Errorf("no entry contains %q", needle)
	case count > 1:
		return 0, fmt.Errorf("%d entries contain %q: use a longer old_text that matches only one", count, needle)
	}
	return found, nil
}

// normalizeEntry flattens text to the single line an entry occupies, so no
// write can inject a heading or a blank line and reshape the document.
func normalizeEntry(text string) (string, error) {
	entry := strings.Join(strings.Fields(text), " ")
	entry = strings.TrimLeft(entry, "#")
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", errors.New("text is required")
	}
	if !strings.HasPrefix(entry, "- ") {
		entry = "- " + entry
	}
	return entry, nil
}

func (s *Store) loadDocument(path, initial string) (string, error) {
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
		if err := atomicfile.Write(path, []byte(initial), 0o600); err != nil {
			return "", err
		}
		return initial, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return string(data), nil
}
