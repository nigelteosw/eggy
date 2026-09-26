package google

import (
	"encoding/json"
	"sync"

	"github.com/nigelteosw/eggy/internal/auth/grants"
)

// memoryRecords is the auth-record container these tests run against. What it
// holds is byte-for-byte what the real container stores, so asserting no
// plaintext credential appears in a record is the same assertion whether the
// container is SQLite or anything else -- which is the point of the store
// owning nothing but ciphertext.
type memoryRecords struct {
	mu      sync.Mutex
	records map[string]json.RawMessage
}

func newMemoryRecords() *memoryRecords { return &memoryRecords{records: map[string]json.RawMessage{}} }

func (m *memoryRecords) Read(section, key string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.records[section+"/"+key]
	if !ok {
		return nil, grants.ErrNotFound
	}
	return record, nil
}

func (m *memoryRecords) Write(section, key string, record json.RawMessage) error {
	return m.Update(section, key, func(json.RawMessage) (json.RawMessage, error) { return record, nil })
}

func (m *memoryRecords) Update(section, key string, mutate func(json.RawMessage) (json.RawMessage, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	updated, err := mutate(m.records[section+"/"+key])
	if err != nil {
		return err
	}
	if updated == nil {
		delete(m.records, section+"/"+key)
		return nil
	}
	m.records[section+"/"+key] = updated
	return nil
}

func (m *memoryRecords) Delete(section, key string) error {
	return m.Update(section, key, func(json.RawMessage) (json.RawMessage, error) { return nil, nil })
}

// raw returns the stored bytes for a record, so a test can look for a
// credential in what was actually persisted.
func (m *memoryRecords) raw(section, key string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.records[section+"/"+key]...)
}
