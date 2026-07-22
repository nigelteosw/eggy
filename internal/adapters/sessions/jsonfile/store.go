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

	"github.com/nigelteosw/eggy/internal/adapters/atomicfile"
	"github.com/nigelteosw/eggy/internal/adapters/filelock"
	"github.com/nigelteosw/eggy/internal/ports"
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)

// ErrSessionNotFound is returned by Load when no session with the given id
// exists, so callers (e.g. a legacy-run import) can distinguish "doesn't
// exist yet" from a real read failure without string-matching an error.
var ErrSessionNotFound = errors.New("implementation session not found")

// ErrSessionExists is returned by Create when a session with the given id
// already exists.
var ErrSessionExists = errors.New("implementation session already exists")

type Store struct {
	root string
	mu   sync.Mutex
}

func Open(root string) *Store { return &Store{root: root} }

func (s *Store) Create(ctx context.Context, session ports.ImplementationSession) (ports.ImplementationSession, error) {
	if err := ctx.Err(); err != nil {
		return ports.ImplementationSession{}, err
	}
	if !sessionIDPattern.MatchString(session.ID) {
		return ports.ImplementationSession{}, errors.New("invalid implementation session id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var created ports.ImplementationSession
	err := filelock.With(s.sessionPath(session.ID), func() error {
		if _, err := os.Stat(s.sessionPath(session.ID)); err == nil {
			return ErrSessionExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.writeLocked(session); err != nil {
			return err
		}
		created = session
		return nil
	})
	return created, err
}

func (s *Store) Load(ctx context.Context, id string) (ports.ImplementationSession, error) {
	if err := ctx.Err(); err != nil {
		return ports.ImplementationSession{}, err
	}
	if !sessionIDPattern.MatchString(id) {
		return ports.ImplementationSession{}, errors.New("invalid implementation session id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var session ports.ImplementationSession
	err := filelock.With(s.sessionPath(id), func() error {
		var err error
		session, err = s.loadLocked(id)
		return err
	})
	return session, err
}

func (s *Store) List(ctx context.Context) ([]ports.ImplementationSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return []ports.ImplementationSession{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session directory: %w", err)
	}
	sessions := make([]ports.ImplementationSession, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !sessionIDPattern.MatchString(entry.Name()) {
			continue
		}
		session, err := s.Load(ctx, entry.Name())
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt) })
	return sessions, nil
}

func (s *Store) AppendEvent(ctx context.Context, id string, event ports.ImplementationSessionEvent) (ports.ImplementationSession, error) {
	if err := ctx.Err(); err != nil {
		return ports.ImplementationSession{}, err
	}
	if !sessionIDPattern.MatchString(id) {
		return ports.ImplementationSession{}, errors.New("invalid implementation session id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var session ports.ImplementationSession
	err := filelock.With(s.sessionPath(id), func() error {
		var err error
		session, err = s.loadLocked(id)
		if err != nil {
			return err
		}
		event.Sequence = uint64(len(session.Events) + 1)
		data, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode session event: %w", err)
		}
		file, err := os.OpenFile(s.eventsPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open session events: %w", err)
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			_ = file.Close()
			return fmt.Errorf("append session event: %w", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return fmt.Errorf("sync session events: %w", err)
		}
		if err := file.Close(); err != nil {
			return err
		}
		session.Events = append(session.Events, event)
		return nil
	})
	return session, err
}

func (s *Store) Update(ctx context.Context, id string, mutate func(*ports.ImplementationSession) error) (ports.ImplementationSession, error) {
	if err := ctx.Err(); err != nil {
		return ports.ImplementationSession{}, err
	}
	if !sessionIDPattern.MatchString(id) {
		return ports.ImplementationSession{}, errors.New("invalid implementation session id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var session ports.ImplementationSession
	err := filelock.With(s.sessionPath(id), func() error {
		var err error
		session, err = s.loadLocked(id)
		if err != nil {
			return err
		}
		if err := mutate(&session); err != nil {
			return err
		}
		return s.writeLocked(session)
	})
	return session, err
}

func (s *Store) loadLocked(id string) (ports.ImplementationSession, error) {
	data, err := os.ReadFile(s.sessionPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return ports.ImplementationSession{}, ErrSessionNotFound
	}
	if err != nil {
		return ports.ImplementationSession{}, fmt.Errorf("read session: %w", err)
	}
	var session ports.ImplementationSession
	if err := json.Unmarshal(data, &session); err != nil {
		return ports.ImplementationSession{}, fmt.Errorf("decode session: %w", err)
	}
	contextData, err := os.ReadFile(s.contextPath(id))
	if err != nil {
		return ports.ImplementationSession{}, fmt.Errorf("read session context: %w", err)
	}
	if err := json.Unmarshal(contextData, &session.Context); err != nil {
		return ports.ImplementationSession{}, fmt.Errorf("decode session context: %w", err)
	}
	events, err := readEvents(s.eventsPath(id))
	if err != nil {
		return ports.ImplementationSession{}, err
	}
	session.Events = events
	return session, nil
}

func (s *Store) writeLocked(session ports.ImplementationSession) error {
	if err := os.MkdirAll(s.sessionDir(session.ID), 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	metadata := session
	metadata.Context = ports.SessionContext{}
	metadata.Events = nil
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	if err := atomicfile.Write(s.sessionPath(session.ID), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	contextData, err := json.MarshalIndent(session.Context, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session context: %w", err)
	}
	if err := atomicfile.Write(s.contextPath(session.ID), append(contextData, '\n'), 0o600); err != nil {
		return fmt.Errorf("write session context: %w", err)
	}
	return nil
}

func (s *Store) sessionDir(id string) string  { return filepath.Join(s.root, id) }
func (s *Store) sessionPath(id string) string { return filepath.Join(s.sessionDir(id), "session.json") }
func (s *Store) contextPath(id string) string { return filepath.Join(s.sessionDir(id), "context.json") }
func (s *Store) eventsPath(id string) string  { return filepath.Join(s.sessionDir(id), "events.jsonl") }

func readEvents(path string) ([]ports.ImplementationSessionEvent, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []ports.ImplementationSessionEvent{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open session events: %w", err)
	}
	defer file.Close()
	var events []ports.ImplementationSessionEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event ports.ImplementationSessionEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("decode session event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read session events: %w", err)
	}
	return events, nil
}
