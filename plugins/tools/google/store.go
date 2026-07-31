package google

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nigelteosw/eggy/plugins/auth/authfile"
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
	Expiry       time.Time `json:"expiry,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	State        string    `json:"state,omitempty"`
	CodeVerifier string    `json:"code_verifier,omitempty"`
	StateExpires time.Time `json:"state_expires,omitempty"`
}

// Authorized reports a grant that can still be renewed. An access token alone
// is not enough: without a refresh token the connection dies at the first
// expiry, which is the failure the MCP adapter learned to guard against by
// forcing consent.
func (r TokenRecord) Authorized() bool { return r.RefreshToken != "" || r.AccessToken != "" }

// TokenStore keeps the record in the shared auth.json document under section
// "google", sealed with AES-256-GCM under EGGY_ENCRYPTION_KEY. Hermes writes
// the equivalent as plaintext JSON in the home directory; there is no reason
// to give up the store Eggy already has.
type TokenStore struct {
	file *authfile.Store
	aead cipher.AEAD
}

const (
	tokenSection = "google"
	tokenKey     = "workspace"
)

type encryptedRecord struct {
	Version    int    `json:"version"`
	Ciphertext string `json:"ciphertext"`
}

func OpenTokenStore(authPath, encodedKey string) (*TokenStore, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode Google encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("Google encryption key must decode to 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &TokenStore{file: authfile.Open(authPath), aead: aead}, nil
}

// Load returns a zero record rather than an error when nothing is stored: an
// unauthorized daemon is an ordinary state on first boot, not a fault.
func (s *TokenStore) Load() (TokenRecord, error) {
	stored, err := s.file.Read(tokenSection, tokenKey)
	if errors.Is(err, authfile.ErrNotFound) {
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
	return s.file.Write(tokenSection, tokenKey, sealed)
}

func (s *TokenStore) Update(update func(*TokenRecord) error) error {
	return s.file.Update(tokenSection, tokenKey, func(stored json.RawMessage) (json.RawMessage, error) {
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

func (s *TokenStore) Delete() error { return s.file.Delete(tokenSection, tokenKey) }

func (s *TokenStore) seal(record TokenRecord) (json.RawMessage, error) {
	record.Version = 1
	plain, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := s.aead.Seal(nonce, nonce, plain, associatedData())
	return json.Marshal(encryptedRecord{Version: 1, Ciphertext: base64.RawURLEncoding.EncodeToString(sealed)})
}

func (s *TokenStore) open(body json.RawMessage) (TokenRecord, error) {
	var encrypted encryptedRecord
	if err := json.Unmarshal(body, &encrypted); err != nil || encrypted.Version != 1 {
		return TokenRecord{}, errors.New("invalid Google token record")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(encrypted.Ciphertext)
	if err != nil || len(sealed) < s.aead.NonceSize() {
		return TokenRecord{}, errors.New("invalid Google token ciphertext")
	}
	nonce := sealed[:s.aead.NonceSize()]
	plain, err := s.aead.Open(nil, nonce, sealed[s.aead.NonceSize():], associatedData())
	if err != nil {
		return TokenRecord{}, errors.New("Google token record authentication failed")
	}
	var record TokenRecord
	if err := json.Unmarshal(plain, &record); err != nil || record.Version != 1 {
		return TokenRecord{}, errors.New("invalid Google token record")
	}
	return record, nil
}

func associatedData() []byte { return []byte("eggy-google-oauth-v1") }
