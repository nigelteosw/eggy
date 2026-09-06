// Package grants holds the two things every stored OAuth grant shares: the
// sealed envelope its bytes live in, and the container interface it is read
// from and written to. The container itself is SQLite -- the auth_records
// table in <home>/eggy.db -- and lives with the rest of the machine state;
// this package stays provider-neutral so Google and MCP seal their records
// the same way without either one owning the storage.
package grants

import (
	"encoding/json"
	"errors"
)

// ErrNotFound reports a section and key with no record behind it. An
// unauthorized provider is an ordinary first-boot state, so every caller
// branches on this rather than treating it as a fault.
var ErrNotFound = errors.New("auth record not found")

// Records is the container a provider stores its sealed grants in. It is
// declared here, beside the Sealer, because the pair is what a provider
// needs: bytes that only open under the owner's key, in a store that keys
// them by provider and record.
type Records interface {
	Read(section, key string) (json.RawMessage, error)
	Write(section, key string, record json.RawMessage) error
	Update(section, key string, mutate func(json.RawMessage) (json.RawMessage, error)) error
	Delete(section, key string) error
}
