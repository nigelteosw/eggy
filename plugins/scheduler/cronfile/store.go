// Package cronfile stores scheduled jobs as one YAML file per job under
// <home>/cron/. Schedules used to be a map inside state.json, which made
// them invisible next to Eggy's own bookkeeping; as files they are listed,
// read, and edited like any other owner-facing artifact -- including through
// the web UI.
package cronfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/atomicfile"
	"github.com/nigelteosw/eggy/plugins/filelock"
	"gopkg.in/yaml.v3"
)

// ErrNotFound reports a job id with no file behind it.
var ErrNotFound = errors.New("schedule not found")

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Extension is the suffix every job file carries.
const Extension = ".yaml"

// job is the on-disk form. It mirrors ports.Schedule with YAML keys chosen
// to read well when an owner opens the file: `cron` rather than
// `expression`, and times as plain RFC3339 strings.
type job struct {
	ID          string `yaml:"id"`
	Kind        string `yaml:"kind"`
	Execution   string `yaml:"execution,omitempty"`
	Instruction string `yaml:"instruction"`
	Cron        string `yaml:"cron,omitempty"`
	NextRun     string `yaml:"next_run"`
	LastRun     string `yaml:"last_run,omitempty"`
	PendingRun  string `yaml:"pending_run,omitempty"`
	Enabled     bool   `yaml:"enabled"`
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func Open(dir string) *Store { return &Store{dir: dir} }

func (s *Store) Dir() string { return s.dir }

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+Extension) }

// List returns every job, ordered by id so callers and the UI see a stable
// listing. A file that does not parse is reported rather than skipped: a
// hand-edited schedule that silently stops firing is worse than a loud one.
func (s *Store) List() ([]ports.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cron directory: %w", err)
	}
	schedules := make([]ports.Schedule, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), Extension) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), Extension)
		if !idPattern.MatchString(id) {
			continue
		}
		schedule, err := s.loadUnlocked(id)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	sort.Slice(schedules, func(i, j int) bool { return schedules[i].ID < schedules[j].ID })
	return schedules, nil
}

func (s *Store) Get(id string) (ports.Schedule, error) {
	if !idPattern.MatchString(id) {
		return ports.Schedule{}, errors.New("invalid schedule id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked(id)
}

// Put writes one job, replacing any file already under that id.
func (s *Store) Put(schedule ports.Schedule) error {
	if !idPattern.MatchString(schedule.ID) {
		return errors.New("invalid schedule id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(s.path(schedule.ID), func() error { return s.saveUnlocked(schedule) })
}

// Create writes a job only when its id is free, so two schedules can never
// collapse into one file.
func (s *Store) Create(schedule ports.Schedule) error {
	if !idPattern.MatchString(schedule.ID) {
		return errors.New("invalid schedule id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(s.path(schedule.ID), func() error {
		if _, err := os.Stat(s.path(schedule.ID)); err == nil {
			return fmt.Errorf("schedule %q already exists", schedule.ID)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return s.saveUnlocked(schedule)
	})
}

// Update applies mutate to one job under its file lock. Returning
// ErrNotFound from the read leaves the file untouched.
func (s *Store) Update(id string, mutate func(*ports.Schedule) error) error {
	if !idPattern.MatchString(id) {
		return errors.New("invalid schedule id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(s.path(id), func() error {
		schedule, err := s.loadUnlocked(id)
		if err != nil {
			return err
		}
		if err := mutate(&schedule); err != nil {
			return err
		}
		schedule.ID = id
		return s.saveUnlocked(schedule)
	})
}

// Delete removes a job. A job that is already gone is not an error.
func (s *Store) Delete(id string) error {
	if !idPattern.MatchString(id) {
		return errors.New("invalid schedule id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(s.path(id), func() error {
		err := os.Remove(s.path(id))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

func (s *Store) loadUnlocked(id string) (ports.Schedule, error) {
	body, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return ports.Schedule{}, ErrNotFound
	}
	if err != nil {
		return ports.Schedule{}, fmt.Errorf("read schedule %s: %w", id, err)
	}
	var stored job
	if err := yaml.Unmarshal(body, &stored); err != nil {
		return ports.Schedule{}, fmt.Errorf("decode schedule %s: %w", id, err)
	}
	schedule, err := stored.toSchedule()
	if err != nil {
		return ports.Schedule{}, fmt.Errorf("schedule %s: %w", id, err)
	}
	// The filename is authoritative: an owner who copies a file to a new
	// name gets a new schedule, not a duplicate of the old one.
	schedule.ID = id
	return schedule, nil
}

func (s *Store) saveUnlocked(schedule ports.Schedule) error {
	body, err := yaml.Marshal(fromSchedule(schedule))
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path(schedule.ID), body, 0o600)
}
