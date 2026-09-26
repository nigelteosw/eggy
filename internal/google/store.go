package google

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/grants"
)

var ErrNotAuthorized = errors.New("Google Workspace is not authorized")

// TokenRecord is the whole of Eggy's Google state: one grant, one refresh
// token, every product. That is the property the MCP path could not have --
// there, each product is a separate server with its own record and its own
// consent screen.
//
// State and CodeVerifier hold a login in progress. They are part of the same
// record rather than a second file because a pending login and a live grant
// are the same account's business, and one sealed document cannot disagree
// with itself about which login the verifier belongs to.
type TokenRecord struct {
	Version      int       `json:"version"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Expiry       time.Time `json:"expiry,omitzero"`
	Scopes       []string  `json:"scopes,omitempty"`
	State        string    `json:"state,omitempty"`
	CodeVerifier string    `json:"code_verifier,omitempty"`
	StateExpires time.Time `json:"state_expires,omitzero"`
	// Email and Subject are the verified identity behind the grant, read
	// from Google's identity endpoint before the grant was stored. A record
	// without them predates verification and is verified before it serves.
	Email   string `json:"email,omitempty"`
	Subject string `json:"subject,omitempty"`
	// Generation counts connections: every replacement and every disconnect
	// increments it, and it survives a disconnect so a reconnect never lands
	// on a number an outstanding approval was granted under.
	Generation uint64 `json:"generation,omitempty"`
}

// Authorized reports a grant that can still be renewed. An access token alone
// is not enough: without a refresh token the connection dies at the first
// expiry, which is the failure the MCP adapter learned to guard against by
// forcing consent.
func (r TokenRecord) Authorized() bool { return r.RefreshToken != "" || r.AccessToken != "" }

// TokenStore keeps the record in the shared auth-record store under section
// "google", sealed with AES-256-GCM under EGGY_ENCRYPTION_KEY. Hermes writes
// the equivalent as plaintext JSON in the home directory; there is no reason
// to give up the store Eggy already has.
type TokenStore struct {
	records grants.Records
	sealer  *grants.Sealer
}

const (
	tokenSection = "google"
	tokenKey     = "workspace"
)

func OpenTokenStore(records grants.Records, encodedKey string) (*TokenStore, error) {
	sealer, err := grants.NewSealer("Google", encodedKey)
	if err != nil {
		return nil, err
	}
	return &TokenStore{records: records, sealer: sealer}, nil
}

// Load returns a zero record rather than an error when nothing is stored: an
// unauthorized daemon is an ordinary state on first boot, not a fault.
func (s *TokenStore) Load() (TokenRecord, error) {
	stored, err := s.records.Read(tokenSection, tokenKey)
	if errors.Is(err, grants.ErrNotFound) {
		return TokenRecord{Version: 1}, nil
	}
	if err != nil {
		return TokenRecord{}, err
	}
	return s.open(stored)
}

func (s *TokenStore) Save(record TokenRecord) error {
	sealed, err := s.seal(record)
	if err != nil {
		return err
	}
	return s.records.Write(tokenSection, tokenKey, sealed)
}

func (s *TokenStore) Update(update func(*TokenRecord) error) error {
	return s.records.Update(tokenSection, tokenKey, func(stored json.RawMessage) (json.RawMessage, error) {
		record := TokenRecord{Version: 1}
		if stored != nil {
			opened, err := s.open(stored)
			if err != nil {
				return nil, err
			}
			record = opened
		}
		if err := update(&record); err != nil {
			return nil, err
		}
		return s.seal(record)
	})
}

func (s *TokenStore) Delete() error { return s.records.Delete(tokenSection, tokenKey) }

func (s *TokenStore) seal(record TokenRecord) (json.RawMessage, error) {
	record.Version = 1
	return s.sealer.Seal(record, associatedData())
}

func (s *TokenStore) open(body json.RawMessage) (TokenRecord, error) {
	var record TokenRecord
	if err := s.sealer.Open(body, associatedData(), &record); err != nil {
		return TokenRecord{}, err
	}
	if record.Version != 1 {
		return TokenRecord{}, s.sealer.Invalid()
	}
	return record, nil
}

func associatedData() []byte { return []byte("eggy-google-oauth-v1") }
