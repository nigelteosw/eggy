// Package authfile owns <home>/auth.json: the one file holding every
// provider credential Eggy obtains at runtime rather than from the
// environment -- MCP server OAuth records and the Google Calendar refresh
// token today.
//
// The document is a map of sections to named records:
//
//	{"version":1,"mcp":{"railway":{...}},"calendar":{"google":{...}}}
//
// Records are opaque here. Whatever secret they carry is encrypted by the
// adapter that owns it before it ever reaches this store, so auth.json is
// never a plaintext secret file -- but it is still owner-only on disk and is
// never readable through the web API.
package authfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"

	"github.com/nigelteosw/eggy/plugins/atomicfile"
	"github.com/nigelteosw/eggy/plugins/filelock"
)

// ErrNotFound reports a section/key pair that auth.json does not carry.
var ErrNotFound = errors.New("auth record not found")

const currentVersion = 1

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type document struct {
	Version  int                                   `json:"version"`
	Sections map[string]map[string]json.RawMessage `json:"sections,omitempty"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func Open(path string) *Store { return &Store{path: path} }

func (s *Store) Path() string { return s.path }

// Read returns the raw record, or ErrNotFound when the section or key is
// absent.
func (s *Store) Read(section, key string) (json.RawMessage, error) {
	if err := validate(section, key); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var record json.RawMessage
	err := filelock.With(s.path, func() error {
		doc, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		value, ok := doc.Sections[section][key]
		if !ok {
			return ErrNotFound
		}
		record = value
		return nil
	})
	return record, err
}

// Write replaces one record, leaving every other section and key untouched.
func (s *Store) Write(section, key string, record json.RawMessage) error {
	return s.Update(section, key, func(json.RawMessage) (json.RawMessage, error) { return record, nil })
}

// Update applies mutate to the current record -- nil when absent -- and
// persists the result under the file lock, so two adapters writing different
// sections never lose each other's write. Returning a nil record deletes it.
func (s *Store) Update(section, key string, mutate func(json.RawMessage) (json.RawMessage, error)) error {
	if err := validate(section, key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return filelock.With(s.path, func() error {
		doc, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		updated, err := mutate(doc.Sections[section][key])
		if err != nil {
			return err
		}
		if updated == nil {
			delete(doc.Sections[section], key)
			if len(doc.Sections[section]) == 0 {
				delete(doc.Sections, section)
			}
		} else {
			if doc.Sections == nil {
				doc.Sections = map[string]map[string]json.RawMessage{}
			}
			if doc.Sections[section] == nil {
				doc.Sections[section] = map[string]json.RawMessage{}
			}
			doc.Sections[section][key] = updated
		}
		return s.saveUnlocked(doc)
	})
}

// Delete removes one record. A record that is already absent is not an error.
func (s *Store) Delete(section, key string) error {
	return s.Update(section, key, func(json.RawMessage) (json.RawMessage, error) { return nil, nil })
}

func (s *Store) loadUnlocked() (document, error) {
	doc := document{Version: currentVersion, Sections: map[string]map[string]json.RawMessage{}}
	body, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return document{}, fmt.Errorf("read auth.json: %w", err)
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return document{}, fmt.Errorf("decode auth.json: %w", err)
	}
	if doc.Version != currentVersion {
		return document{}, fmt.Errorf("auth.json has unsupported version %d", doc.Version)
	}
	if doc.Sections == nil {
		doc.Sections = map[string]map[string]json.RawMessage{}
	}
	return doc, nil
}

func (s *Store) saveUnlocked(doc document) error {
	doc.Version = currentVersion
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, append(body, '\n'), 0o600)
}

func validate(section, key string) error {
	if !namePattern.MatchString(section) {
		return errors.New("invalid auth section name")
	}
	if !namePattern.MatchString(key) {
		return errors.New("invalid auth record name")
	}
	return nil
}
