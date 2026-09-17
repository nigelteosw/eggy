// Package connections stores the credentials a chat connection needs -- a
// Discord bot token today, a WhatsApp session tomorrow -- sealed in the same
// auth-record container OAuth grants use, keyed by connection ID. It exists so
// an owner can add a bot from the panel at runtime instead of editing the
// deployment's environment, and so the next connection adds a key, not a
// storage form.
package connections

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/nigelteosw/eggy/plugins/auth/grants"
)

// section is the auth_records section every connection's credentials share.
const section = "connections"

// Credentials is one connection's secret values by field name, for example
// {"bot_token": "..."}. Which fields a connection needs is the connection
// adapter's business; this package only keeps them sealed.
type Credentials map[string]string

var connectionIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Store reads and writes sealed credentials. Records is the container;
// Sealer is the owner's key. A nil Sealer (no EGGY_ENCRYPTION_KEY) makes
// every read empty and every write an error, which the panel reports.
type Store struct {
	records grants.Records
	sealer  *grants.Sealer
}

func New(records grants.Records, sealer *grants.Sealer) *Store {
	return &Store{records: records, sealer: sealer}
}

// ErrUnavailable reports a store with no key to seal under.
var ErrUnavailable = errors.New("connection credentials need EGGY_ENCRYPTION_KEY")

func associatedData(connection string) []byte { return []byte(section + ":" + connection) }

func checkConnection(connection string) error {
	if !connectionIDPattern.MatchString(connection) {
		return fmt.Errorf("%q is not a connection id", connection)
	}
	return nil
}

// Read returns the connection's credentials, or an empty set when none are
// stored. A record that fails to open is an error, not an empty set: silently
// running without a credential that exists would look like "not configured".
func (s *Store) Read(connection string) (Credentials, error) {
	if err := checkConnection(connection); err != nil {
		return nil, err
	}
	if s == nil || s.sealer == nil {
		return Credentials{}, nil
	}
	body, err := s.records.Read(section, connection)
	if errors.Is(err, grants.ErrNotFound) {
		return Credentials{}, nil
	}
	if err != nil {
		return nil, err
	}
	var credentials Credentials
	if err := s.sealer.Open(body, associatedData(connection), &credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

// Write merges values over the stored credentials: a blank value removes
// that field, so a form that leaves a field empty keeps what is there
// unless it asks to clear it. An empty resulting set deletes the record.
func (s *Store) Write(connection string, values Credentials) error {
	if err := checkConnection(connection); err != nil {
		return err
	}
	if s == nil || s.sealer == nil {
		return ErrUnavailable
	}
	current, err := s.Read(connection)
	if err != nil {
		return err
	}
	for field, value := range values {
		if value == "" {
			delete(current, field)
		} else {
			current[field] = value
		}
	}
	if len(current) == 0 {
		return s.Delete(connection)
	}
	sealed, err := s.sealer.Seal(current, associatedData(connection))
	if err != nil {
		return err
	}
	return s.records.Write(section, connection, sealed)
}

func (s *Store) Delete(connection string) error {
	if err := checkConnection(connection); err != nil {
		return err
	}
	if s == nil || s.sealer == nil {
		return nil
	}
	return s.records.Delete(section, connection)
}

// Values lists every stored secret across connections, for log redaction.
func (s *Store) Values(connectionIDs ...string) []string {
	var values []string
	for _, id := range connectionIDs {
		credentials, err := s.Read(id)
		if err != nil {
			continue
		}
		fields := make([]string, 0, len(credentials))
		for field := range credentials {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			values = append(values, credentials[field])
		}
	}
	return values
}
