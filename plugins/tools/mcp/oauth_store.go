package mcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/nigelteosw/eggy/plugins/auth/authfile"
)

var (
	ErrOAuthRecordNotFound = errors.New("MCP OAuth record not found")
	oauthServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type OAuthRecord struct {
	Version                 int       `json:"version"`
	ServerURL               string    `json:"server_url"`
	Resource                string    `json:"resource,omitempty"`
	AuthorizationEndpoint   string    `json:"authorization_endpoint,omitempty"`
	TokenEndpoint           string    `json:"token_endpoint,omitempty"`
	RegistrationEndpoint    string    `json:"registration_endpoint,omitempty"`
	ClientID                string    `json:"client_id,omitempty"`
	ClientSecret            string    `json:"client_secret,omitempty"`
	Scopes                  []string  `json:"scopes,omitempty"`
	AccessToken             string    `json:"access_token,omitempty"`
	RefreshToken            string    `json:"refresh_token,omitempty"`
	TokenType               string    `json:"token_type,omitempty"`
	Expiry                  time.Time `json:"expiry,omitempty"`
	State                   string    `json:"state,omitempty"`
	CodeVerifier            string    `json:"code_verifier,omitempty"`
	LastAuthorizationURL    string    `json:"last_authorization_url,omitempty"`
	StateExpires            time.Time `json:"state_expires,omitempty"`
	TokenEndpointAuthMethod string    `json:"token_endpoint_auth_method,omitempty"`
}

// OAuthStore keeps every server's OAuth record in the shared auth.json
// document (section "mcp", one key per server) rather than in a file tree of
// its own. Each record is sealed with AES-256-GCM under EGGY_ENCRYPTION_KEY
// and bound to its server name and URL, so a record cannot be replayed
// against a different server even by an owner editing auth.json by hand.
type OAuthStore struct {
	file *authfile.Store
	aead cipher.AEAD
}

const oauthSection = "mcp"

type encryptedOAuthRecord struct {
	Version    int    `json:"version"`
	Ciphertext string `json:"ciphertext"`
}

// OpenOAuthStore opens the store over an auth.json path.
func OpenOAuthStore(authPath, encodedKey string) (*OAuthStore, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode MCP encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("MCP encryption key must decode to 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &OAuthStore{file: authfile.Open(authPath), aead: aead}, nil
}

func (s *OAuthStore) Save(server, serverURL string, record OAuthRecord) error {
	if err := validateOAuthKey(server, serverURL); err != nil {
		return err
	}
	sealed, err := s.seal(server, serverURL, record)
	if err != nil {
		return err
	}
	return s.file.Write(oauthSection, server, sealed)
}

func (s *OAuthStore) Load(server, serverURL string) (OAuthRecord, error) {
	if err := validateOAuthKey(server, serverURL); err != nil {
		return OAuthRecord{}, err
	}
	stored, err := s.file.Read(oauthSection, server)
	if errors.Is(err, authfile.ErrNotFound) {
		return OAuthRecord{}, ErrOAuthRecordNotFound
	}
	if err != nil {
		return OAuthRecord{}, err
	}
	return s.open(stored, server, serverURL)
}

func (s *OAuthStore) Update(server, serverURL string, update func(*OAuthRecord) error) error {
	if err := validateOAuthKey(server, serverURL); err != nil {
		return err
	}
	return s.file.Update(oauthSection, server, func(stored json.RawMessage) (json.RawMessage, error) {
		record := OAuthRecord{Version: 1, ServerURL: serverURL}
		if stored != nil {
			opened, err := s.open(stored, server, serverURL)
			if err != nil {
				return nil, err
			}
			record = opened
		}
		if err := update(&record); err != nil {
			return nil, err
		}
		return s.seal(server, serverURL, record)
	})
}

func (s *OAuthStore) Delete(server, serverURL string) error {
	if err := validateOAuthKey(server, serverURL); err != nil {
		return err
	}
	return s.file.Delete(oauthSection, server)
}

func (s *OAuthStore) seal(server, serverURL string, record OAuthRecord) (json.RawMessage, error) {
	record.Version = 1
	record.ServerURL = serverURL
	plain, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := s.aead.Seal(nonce, nonce, plain, oauthAssociatedData(server, serverURL))
	return json.Marshal(encryptedOAuthRecord{Version: 1, Ciphertext: base64.RawURLEncoding.EncodeToString(sealed)})
}

func (s *OAuthStore) open(body json.RawMessage, server, serverURL string) (OAuthRecord, error) {
	var encrypted encryptedOAuthRecord
	if err := json.Unmarshal(body, &encrypted); err != nil || encrypted.Version != 1 {
		return OAuthRecord{}, errors.New("invalid MCP OAuth record")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(encrypted.Ciphertext)
	if err != nil || len(sealed) < s.aead.NonceSize() {
		return OAuthRecord{}, errors.New("invalid MCP OAuth ciphertext")
	}
	nonce := sealed[:s.aead.NonceSize()]
	plain, err := s.aead.Open(nil, nonce, sealed[s.aead.NonceSize():], oauthAssociatedData(server, serverURL))
	if err != nil {
		return OAuthRecord{}, errors.New("MCP OAuth record authentication failed")
	}
	var record OAuthRecord
	if err := json.Unmarshal(plain, &record); err != nil || record.Version != 1 || record.ServerURL != serverURL {
		return OAuthRecord{}, errors.New("invalid MCP OAuth record")
	}
	return record, nil
}

func validateOAuthKey(server, serverURL string) error {
	if !oauthServerNamePattern.MatchString(server) {
		return errors.New("invalid MCP OAuth server name")
	}
	if serverURL == "" {
		return errors.New("MCP OAuth server URL is required")
	}
	return nil
}

func oauthAssociatedData(server, serverURL string) []byte {
	return []byte("eggy-mcp-oauth-v1\x00" + server + "\x00" + serverURL)
}
