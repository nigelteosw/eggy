package markdown

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// SOUL.md rides in every turn's prompt for every account, so it is bounded
	// like the rest, though it changes rarely enough to afford the room.
	DefaultSoulMaxBytes = 4 << 10
)

// Paths locates the context documents. SOUL.md is one shared file at the top
// of the home; USER.md, MEMORY.md and WATCH.md are private, one set per
// account, and Memories resolves the directory holding them from the acting
// account's ID (see internal/home.Layout.AccountMemories). It is a function
// rather than a map so a removed account resolves to nothing and an ID the
// layout refuses cannot become a path.
type Paths struct {
	Soul     string
	Memories func(accountID string) (string, error)
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
		Soul:     filepath.Join(dir, "SOUL.md"),
		Memories: func(string) (string, error) { return dir, nil },
	}, userMaxBytes, memoryMaxBytes, 0)
}

// privateDir resolves the acting account's memories directory. It fails
// closed on a missing principal and refuses a directory that is a symlink,
// so neither a forged account nor a planted link can move a write outside
// the account's own directory.
func (s *Store) privateDir(ctx context.Context) (string, error) {
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", err
	}
	dir, err := s.paths.Memories(principal.AccountID)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink; refusing to use it for private documents", dir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Store) Load(ctx context.Context) (ports.AgentContext, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentContext{}, err
	}
	dir, err := s.privateDir(ctx)
	if err != nil {
		return ports.AgentContext{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return ports.AgentContext{
		Soul:         s.loadDocument(s.paths.Soul, initialSoul),
		User:         s.loadDocument(filepath.Join(dir, "USER.md"), initialUser),
		Memory:       s.loadDocument(filepath.Join(dir, "MEMORY.md"), initialMemory),
		Watch:        s.loadDocument(filepath.Join(dir, "WATCH.md"), initialWatch),
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
// whole document. Blank content is how a document is reset: the next load
// reads the built-in default in its place.
//
// The budget is enforced the same way rewrite enforces it, including the
// shrinking-edit escape hatch: a write that leaves the document no larger
// than it found it always proceeds, so a document already over budget can
// still be brought back under.
func (s *Store) ReplaceDocument(ctx context.Context, document ports.ContextDocument, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, initial, maxBytes, err := s.writableDocument(ctx, document)
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
	// SOUL.md is prose, not a list: entry edits would flatten its headings.
	if document == ports.ContextSoul {
		return errors.New("SOUL.md is edited as a whole document, not by entry")
	}
	path, initial, maxBytes, err := s.writableDocument(ctx, document)
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

func (s *Store) writableDocument(ctx context.Context, document ports.ContextDocument) (path, initial string, maxBytes int64, err error) {
	var name string
	switch document {
	case ports.ContextSoul:
		// Shared by every account, so it needs no principal's directory --
		// but a write still needs a principal, like every other write.
		if _, err := ports.PrincipalFromContext(ctx); err != nil {
			return "", "", 0, err
		}
		return s.paths.Soul, initialSoul, DefaultSoulMaxBytes, nil
	case ports.ContextUser:
		name, initial, maxBytes = "USER.md", initialUser, s.userMaxBytes
	case ports.ContextMemory:
		name, initial, maxBytes = "MEMORY.md", initialMemory, s.memoryMaxBytes
	case ports.ContextWatch:
		name, initial, maxBytes = "WATCH.md", initialWatch, s.watchMaxBytes
	default:
		return "", "", 0, fmt.Errorf("unknown context document %q", document)
	}
	dir, err := s.privateDir(ctx)
	if err != nil {
		return "", "", 0, err
	}
	return filepath.Join(dir, name), initial, maxBytes, nil
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

// loadDocument is the lenient read a turn depends on. Whatever state the
// owner's file is in -- missing, emptied, unreadable -- the turn gets a
// usable document: the built-in default stands in, and an unreadable file is
// logged rather than failing every turn until someone notices.
func (s *Store) loadDocument(path, initial string) string {
	var content string
	err := filelock.With(path, func() error {
		var err error
		content, err = s.loadDocumentUnlocked(path, initial)
		return err
	})
	if err != nil {
		slog.Warn("context document unreadable; using the built-in default", "document", filepath.Base(path), "error", err)
		return initial
	}
	return content
}

// loadDocumentUnlocked reads path, returning initial for a file that does not
// exist or holds nothing but whitespace. The default is never written back:
// the file exists only once someone writes it, so an improved default reaches
// every owner who never changed theirs, and deleting the file is a reset.
func (s *Store) loadDocumentUnlocked(path, initial string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return initial, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return initial, nil
	}
	return string(data), nil
}
