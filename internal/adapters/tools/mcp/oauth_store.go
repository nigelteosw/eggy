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
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/filelock"
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

type OAuthStore struct {
	root string
	aead cipher.AEAD
}

type encryptedOAuthRecord struct {
	Version    int    `json:"version"`
	Ciphertext string `json:"ciphertext"`
}

func OpenOAuthStore(dataDir, encodedKey string) (*OAuthStore, error) {
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
	return &OAuthStore{root: filepath.Join(dataDir, "mcp"), aead: aead}, nil
}

func (s *OAuthStore) path(server string) string {
	return filepath.Join(s.root, server, "oauth.json")
}

func (s *OAuthStore) Save(server, serverURL string, record OAuthRecord) error {
	if err := validateOAuthKey(server, serverURL); err != nil {
		return err
	}
	path := s.path(server)
	return filelock.With(path, func() error { return s.saveUnlocked(path, server, serverURL, record) })
}

func (s *OAuthStore) Load(server, serverURL string) (OAuthRecord, error) {
	if err := validateOAuthKey(server, serverURL); err != nil {
		return OAuthRecord{}, err
	}
	var record OAuthRecord
	path := s.path(server)
	err := filelock.With(path, func() error {
		var err error
		record, err = s.loadUnlocked(path, server, serverURL)
		return err
	})
	return record, err
}

func (s *OAuthStore) Update(server, serverURL string, update func(*OAuthRecord) error) error {
	if err := validateOAuthKey(server, serverURL); err != nil {
		return err
	}
	path := s.path(server)
	return filelock.With(path, func() error {
		record, err := s.loadUnlocked(path, server, serverURL)
		if errors.Is(err, ErrOAuthRecordNotFound) {
			record = OAuthRecord{Version: 1, ServerURL: serverURL}
		} else if err != nil {
			return err
		}
		if err := update(&record); err != nil {
			return err
		}
		return s.saveUnlocked(path, server, serverURL, record)
	})
}

func (s *OAuthStore) Delete(server, serverURL string) error {
	if err := validateOAuthKey(server, serverURL); err != nil {
		return err
	}
	path := s.path(server)
	return filelock.With(path, func() error {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

func (s *OAuthStore) saveUnlocked(path, server, serverURL string, record OAuthRecord) error {
	record.Version = 1
	record.ServerURL = serverURL
	plain, err := json.Marshal(record)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := s.aead.Seal(nonce, nonce, plain, oauthAssociatedData(server, serverURL))
	body, err := json.Marshal(encryptedOAuthRecord{Version: 1, Ciphertext: base64.RawURLEncoding.EncodeToString(sealed)})
	if err != nil {
		return err
	}
	return atomicWriteOAuth(path, body)
}

func (s *OAuthStore) loadUnlocked(path, server, serverURL string) (OAuthRecord, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return OAuthRecord{}, ErrOAuthRecordNotFound
	}
	if err != nil {
		return OAuthRecord{}, err
	}
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

func atomicWriteOAuth(path string, body []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".oauth-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err == nil {
		err = dir.Sync()
		_ = dir.Close()
	}
	return err
}
