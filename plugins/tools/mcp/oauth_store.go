package mcp

import (
	"encoding/json"
	"errors"
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
	file   *authfile.Store
	sealer *authfile.Sealer
}

const oauthSection = "mcp"

// OpenOAuthStore opens the store over an auth.json path.
func OpenOAuthStore(authPath, encodedKey string) (*OAuthStore, error) {
	sealer, err := authfile.NewSealer("MCP OAuth", encodedKey)
	if err != nil {
		return nil, err
	}
	return &OAuthStore{file: authfile.Open(authPath), sealer: sealer}, nil
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
	return s.sealer.Seal(record, oauthAssociatedData(server, serverURL))
}

func (s *OAuthStore) open(body json.RawMessage, server, serverURL string) (OAuthRecord, error) {
	var record OAuthRecord
	if err := s.sealer.Open(body, oauthAssociatedData(server, serverURL), &record); err != nil {
		return OAuthRecord{}, err
	}
	// The URL is checked as well as bound: associated data already makes a
	// record for another server fail to open, and this catches the same
	// mismatch inside a record that opened cleanly.
	if record.Version != 1 || record.ServerURL != serverURL {
		return OAuthRecord{}, s.sealer.Invalid()
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
