package jsonfile

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/atomicfile"
	"github.com/nigelteosw/eggy/plugins/filelock"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)

// ErrSessionNotFound is returned by Load when no record with the given id
// exists, so callers can distinguish "doesn't exist yet" from a real read
// failure without string-matching an error.
var ErrSessionNotFound = errors.New("transcript not found")

// ErrSessionExists is returned by Create when a record with the given id
// already exists.
var ErrSessionExists = errors.New("transcript already exists")

type Store struct {
	root string
	mu   sync.Mutex
}

func Open(root string) *Store { return &Store{root: root} }

func (s *Store) Create(ctx context.Context, transcript ports.Transcript) (ports.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return ports.Transcript{}, err
	}
	if !idPattern.MatchString(transcript.ID) {
		return ports.Transcript{}, errors.New("invalid transcript id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var created ports.Transcript
	err := filelock.With(s.sessionPath(transcript.ID), func() error {
		if _, err := os.Stat(s.sessionPath(transcript.ID)); err == nil {
			return ErrSessionExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.writeLocked(transcript); err != nil {
			return err
		}
		created = transcript
		return nil
	})
	return created, err
}

func (s *Store) Load(ctx context.Context, id string) (ports.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return ports.Transcript{}, err
	}
	if !idPattern.MatchString(id) {
		return ports.Transcript{}, errors.New("invalid transcript id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var transcript ports.Transcript
	err := filelock.With(s.sessionPath(id), func() error {
		var err error
		transcript, err = s.loadLocked(id)
		return err
	})
	return transcript, err
}

func (s *Store) List(ctx context.Context) ([]ports.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return []ports.Transcript{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read transcript directory: %w", err)
	}
	sessions := make([]ports.Transcript, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !idPattern.MatchString(entry.Name()) {
			continue
		}
		transcript, err := s.Load(ctx, entry.Name())
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, transcript)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt) })
	return sessions, nil
}

func (s *Store) AppendEvent(ctx context.Context, id string, event ports.TranscriptEvent) (ports.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return ports.Transcript{}, err
	}
	if !idPattern.MatchString(id) {
		return ports.Transcript{}, errors.New("invalid transcript id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var transcript ports.Transcript
	err := filelock.With(s.sessionPath(id), func() error {
		var err error
		transcript, err = s.loadLocked(id)
		if err != nil {
			return err
		}
		event.Sequence = uint64(len(transcript.Events) + 1)
		data, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode transcript event: %w", err)
		}
		file, err := os.OpenFile(s.eventsPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open transcript events: %w", err)
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			_ = file.Close()
			return fmt.Errorf("append transcript event: %w", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return fmt.Errorf("sync transcript events: %w", err)
		}
		if err := file.Close(); err != nil {
			return err
		}
		transcript.Events = append(transcript.Events, event)
		return nil
	})
	return transcript, err
}

func (s *Store) Update(ctx context.Context, id string, mutate func(*ports.Transcript) error) (ports.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return ports.Transcript{}, err
	}
	if !idPattern.MatchString(id) {
		return ports.Transcript{}, errors.New("invalid transcript id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var transcript ports.Transcript
	err := filelock.With(s.sessionPath(id), func() error {
		var err error
		transcript, err = s.loadLocked(id)
		if err != nil {
			return err
		}
		if err := mutate(&transcript); err != nil {
			return err
		}
		return s.writeLocked(transcript)
	})
	return transcript, err
}

func (s *Store) loadLocked(id string) (ports.Transcript, error) {
	data, err := os.ReadFile(s.sessionPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return ports.Transcript{}, ErrSessionNotFound
	}
	if err != nil {
		return ports.Transcript{}, fmt.Errorf("read transcript: %w", err)
	}
	var transcript ports.Transcript
	if err := json.Unmarshal(data, &transcript); err != nil {
		return ports.Transcript{}, fmt.Errorf("decode transcript: %w", err)
	}
	events, err := readEvents(s.eventsPath(id))
	if err != nil {
		return ports.Transcript{}, err
	}
	transcript.Events = events
	return transcript, nil
}

func (s *Store) writeLocked(transcript ports.Transcript) error {
	if err := os.MkdirAll(s.sessionDir(transcript.ID), 0o700); err != nil {
		return fmt.Errorf("create transcript directory: %w", err)
	}
	metadata := transcript
	metadata.Events = nil
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode transcript: %w", err)
	}
	if err := atomicfile.Write(s.sessionPath(transcript.ID), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	return nil
}

func (s *Store) sessionDir(id string) string { return filepath.Join(s.root, id) }
func (s *Store) sessionPath(id string) string {
	// The document is still session.json on disk: a transcript loads from
	// exactly the file the previous shape wrote, and the fields that went
	// away are simply ignored. Renaming it would orphan every existing
	// record for no gain.
	return filepath.Join(s.sessionDir(id), "session.json")
}
func (s *Store) eventsPath(id string) string { return filepath.Join(s.sessionDir(id), "events.jsonl") }

func readEvents(path string) ([]ports.TranscriptEvent, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []ports.TranscriptEvent{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open transcript events: %w", err)
	}
	defer file.Close()
	var events []ports.TranscriptEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event ports.TranscriptEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("decode transcript event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read transcript events: %w", err)
	}
	return events, nil
}

// ChangeStore persists Change records as one document each. It shares this
// package's atomic-write and file-lock primitives with the transcript store
// but nothing else: a change has no event log, and a transcript has no
// lifecycle, so collapsing them into one document is exactly the conflation
// this split removed.
type ChangeStore struct {
	root string
	mu   sync.Mutex
}

func OpenChanges(root string) *ChangeStore { return &ChangeStore{root: root} }

func (s *ChangeStore) path(id string) string { return filepath.Join(s.root, id+".json") }

func (s *ChangeStore) Create(ctx context.Context, change ports.Change) (ports.Change, error) {
	if err := ctx.Err(); err != nil {
		return ports.Change{}, err
	}
	if !idPattern.MatchString(change.ID) {
		return ports.Change{}, errors.New("invalid change id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return ports.Change{}, fmt.Errorf("create changes directory: %w", err)
	}
	var created ports.Change
	err := filelock.With(s.path(change.ID), func() error {
		if _, err := os.Stat(s.path(change.ID)); err == nil {
			return ErrSessionExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.writeLocked(change); err != nil {
			return err
		}
		created = change
		return nil
	})
	return created, err
}

func (s *ChangeStore) Load(ctx context.Context, id string) (ports.Change, error) {
	if err := ctx.Err(); err != nil {
		return ports.Change{}, err
	}
	if !idPattern.MatchString(id) {
		return ports.Change{}, errors.New("invalid change id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var change ports.Change
	err := filelock.With(s.path(id), func() error {
		var err error
		change, err = s.loadLocked(id)
		return err
	})
	return change, err
}

func (s *ChangeStore) List(ctx context.Context) ([]ports.Change, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read changes directory: %w", err)
	}
	changes := make([]ports.Change, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		change, err := s.loadLocked(name[:len(name)-len(".json")])
		if errors.Is(err, ErrSessionNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].UpdatedAt.After(changes[j].UpdatedAt) })
	return changes, nil
}

func (s *ChangeStore) Update(ctx context.Context, id string, mutate func(*ports.Change) error) (ports.Change, error) {
	if err := ctx.Err(); err != nil {
		return ports.Change{}, err
	}
	if !idPattern.MatchString(id) {
		return ports.Change{}, errors.New("invalid change id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var updated ports.Change
	err := filelock.With(s.path(id), func() error {
		change, err := s.loadLocked(id)
		if err != nil {
			return err
		}
		if err := mutate(&change); err != nil {
			return err
		}
		if err := s.writeLocked(change); err != nil {
			return err
		}
		updated = change
		return nil
	})
	return updated, err
}

func (s *ChangeStore) loadLocked(id string) (ports.Change, error) {
	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return ports.Change{}, ErrSessionNotFound
	}
	if err != nil {
		return ports.Change{}, fmt.Errorf("read change: %w", err)
	}
	var change ports.Change
	if err := json.Unmarshal(data, &change); err != nil {
		return ports.Change{}, fmt.Errorf("decode change: %w", err)
	}
	return change, nil
}

func (s *ChangeStore) writeLocked(change ports.Change) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create changes directory: %w", err)
	}
	data, err := json.MarshalIndent(change, "", "  ")
	if err != nil {
		return fmt.Errorf("encode change: %w", err)
	}
	return atomicfile.Write(s.path(change.ID), append(data, '\n'), 0o600)
}
