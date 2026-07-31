package authfile

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// Sealer is the one implementation of the sealed record: AES-256-GCM under
// EGGY_ENCRYPTION_KEY, nonce prefixed to the ciphertext, wrapped in a
// versioned envelope. It lives beside the auth.json container because every
// record that container holds is sealed the same way, and a second copy of
// this is a second thing to audit and a second place for a nonce or an
// associated-data mistake to hide.
//
// The record's own contents stay the caller's business. A Sealer takes bytes
// and associated data and returns bytes: what a grant looks like, and what
// binds it, is what distinguishes one provider from the next.
type Sealer struct {
	aead cipher.AEAD
	// label names the provider in errors an owner reads, so a failure points
	// at the grant that failed rather than at "a record".
	label string
}

// sealedEnvelope is what actually lands in auth.json. The version is the
// envelope's own, independent of any version the sealed record carries.
type sealedEnvelope struct {
	Version    int    `json:"version"`
	Ciphertext string `json:"ciphertext"`
}

const sealedVersion = 1

// NewSealer builds a Sealer from the base64 EGGY_ENCRYPTION_KEY. The key must
// decode to exactly 32 bytes: AES-256 is the only width used here, and
// silently accepting a shorter key would quietly weaken every stored grant.
func NewSealer(label, encodedKey string) (*Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode %s encryption key: %w", label, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s encryption key must decode to 32 bytes", label)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead, label: label}, nil
}

// Seal marshals record and returns the envelope to store. associatedData binds
// the ciphertext to the identity it was stored under, so a record moved to
// another key or another server by hand fails to open rather than being
// replayed.
func (s *Sealer) Seal(record any, associatedData []byte) (json.RawMessage, error) {
	plain, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := s.aead.Seal(nonce, nonce, plain, associatedData)
	return json.Marshal(sealedEnvelope{Version: sealedVersion, Ciphertext: base64.RawURLEncoding.EncodeToString(sealed)})
}

// Open reverses Seal into record. Every failure is reported as an opaque
// message rather than distinguishing a malformed envelope from a failed
// authentication: the repair is the same in both cases, and the difference is
// only useful to someone tampering.
func (s *Sealer) Open(body json.RawMessage, associatedData []byte, record any) error {
	var envelope sealedEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Version != sealedVersion {
		return fmt.Errorf("invalid %s record", s.label)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil || len(sealed) < s.aead.NonceSize() {
		return fmt.Errorf("invalid %s ciphertext", s.label)
	}
	nonce := sealed[:s.aead.NonceSize()]
	plain, err := s.aead.Open(nil, nonce, sealed[s.aead.NonceSize():], associatedData)
	if err != nil {
		return fmt.Errorf("%s record authentication failed", s.label)
	}
	if err := json.Unmarshal(plain, record); err != nil {
		return fmt.Errorf("invalid %s record", s.label)
	}
	return nil
}

// Invalid builds the caller-side rejection in Open's wording, keeping one
// phrasing for "this record is not usable" across every provider.
func (s *Sealer) Invalid() error { return fmt.Errorf("invalid %s record", s.label) }
