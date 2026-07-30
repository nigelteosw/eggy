// Package authfile owns the encrypted-provider-record container at auth.json.
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

func (s *Store) Write(section, key string, record json.RawMessage) error {
	return s.Update(section, key, func(json.RawMessage) (json.RawMessage, error) { return record, nil })
}

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
			if doc.Sections[section] == nil {
				doc.Sections[section] = map[string]json.RawMessage{}
			}
			doc.Sections[section][key] = updated
		}
		return s.saveUnlocked(doc)
	})
}

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
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, append(body, '\n'), 0o600)
}

func validate(section, key string) error {
	if !namePattern.MatchString(section) || !namePattern.MatchString(key) {
		return errors.New("invalid auth record name")
	}
	return nil
}
