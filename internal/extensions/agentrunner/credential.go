package agentrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/auth/grants"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// patSection is the auth_records section holding each account's GitHub PAT.
const patSection = "agent_runner_pat"

// ErrNotConnected means the account has no GitHub PAT, or has disconnected it.
var ErrNotConnected = errors.New("no GitHub token connected for this account")

// patRecord is what is sealed: the token and what it was verified to be.
// Nothing here is returned to the browser except the login and access map.
type patRecord struct {
	Token        string          `json:"token"`
	Login        string          `json:"login"`
	Repositories map[string]bool `json:"repositories"`
}

// patWrapper is the stored row. Generation is plaintext because it is the
// associated data the sealed body is bound to, so it must be readable before
// the body can be opened. A disconnect leaves Sealed empty and still advances
// Generation, so authority granted under the old token cannot be revived.
type patWrapper struct {
	Generation int64           `json:"generation"`
	Sealed     json.RawMessage `json:"sealed,omitempty"`
}

// PATStatus is what the panel and Telegram may show about an account's token.
type PATStatus struct {
	Connected    bool            `json:"connected"`
	Login        string          `json:"login,omitempty"`
	Generation   int64           `json:"generation"`
	Repositories map[string]bool `json:"repositories,omitempty"`
}

// Credentials holds each account's own GitHub PAT, sealed in auth_records.
type Credentials struct {
	records grants.Records
	sealer  *grants.Sealer
	gh      *GitHub
}

// NewCredentials builds the store over records, sealed with encryptionKey.
func NewCredentials(records grants.Records, encryptionKey string, gh *GitHub) (*Credentials, error) {
	sealer, err := grants.NewSealer("agent runner PAT", encryptionKey)
	if err != nil {
		return nil, err
	}
	return &Credentials{records: records, sealer: sealer, gh: gh}, nil
}

// associatedData binds a sealed token to the account and generation it was
// stored under, so a row moved or replayed by hand fails to open.
func associatedData(account string, generation int64) []byte {
	return []byte(fmt.Sprintf("agent_runner|github|%s|%d", account, generation))
}

func accountOf(ctx context.Context) (string, error) {
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", err
	}
	return principal.AccountID, nil
}

func (c *Credentials) load(account string) (patWrapper, error) {
	raw, err := c.records.Read(patSection, account)
	if errors.Is(err, grants.ErrNotFound) {
		return patWrapper{}, nil
	}
	if err != nil {
		return patWrapper{}, err
	}
	var w patWrapper
	if err := json.Unmarshal(raw, &w); err != nil {
		return patWrapper{}, fmt.Errorf("agent runner PAT record: %w", err)
	}
	return w, nil
}

func (c *Credentials) save(account string, w patWrapper) error {
	raw, err := json.Marshal(w)
	if err != nil {
		return err
	}
	return c.records.Write(patSection, account, raw)
}

// Set verifies a new token before storing it: its identity from GitHub, and
// push access to each configured repository. Replacing a token advances the
// generation, which invalidates any authority granted under the old one.
func (c *Credentials) Set(ctx context.Context, token string, repos []config.RepositoryConfig) (PATStatus, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return PATStatus{}, err
	}
	identity, err := c.gh.Identity(ctx, token)
	if err != nil {
		return PATStatus{}, err
	}
	access := make(map[string]bool, len(repos))
	for _, repo := range repos {
		owner, name, err := ParseCloneURL(repo.CloneURL)
		if err != nil {
			access[repo.Name] = false
			continue
		}
		push, err := c.gh.CanPush(ctx, token, owner, name)
		if err != nil {
			return PATStatus{}, err
		}
		access[repo.Name] = push
	}
	current, err := c.load(account)
	if err != nil {
		return PATStatus{}, err
	}
	generation := current.Generation + 1
	sealed, err := c.sealer.Seal(patRecord{Token: token, Login: identity.Login, Repositories: access}, associatedData(account, generation))
	if err != nil {
		return PATStatus{}, err
	}
	if err := c.save(account, patWrapper{Generation: generation, Sealed: sealed}); err != nil {
		return PATStatus{}, err
	}
	return PATStatus{Connected: true, Login: identity.Login, Generation: generation, Repositories: access}, nil
}

// Status reports the account's token without its value.
func (c *Credentials) Status(ctx context.Context) (PATStatus, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return PATStatus{}, err
	}
	w, err := c.load(account)
	if err != nil {
		return PATStatus{}, err
	}
	if len(w.Sealed) == 0 {
		return PATStatus{Generation: w.Generation}, nil
	}
	var rec patRecord
	if err := c.sealer.Open(w.Sealed, associatedData(account, w.Generation), &rec); err != nil {
		return PATStatus{}, err
	}
	return PATStatus{Connected: true, Login: rec.Login, Generation: w.Generation, Repositories: rec.Repositories}, nil
}

// Disconnect removes the token and advances the generation.
func (c *Credentials) Disconnect(ctx context.Context) error {
	account, err := accountOf(ctx)
	if err != nil {
		return err
	}
	w, err := c.load(account)
	if err != nil {
		return err
	}
	return c.save(account, patWrapper{Generation: w.Generation + 1})
}

// token returns the account's token and the generation it belongs to. Only
// the publisher and the clone step call it, never a response or a tool.
func (c *Credentials) token(ctx context.Context) (string, int64, error) {
	account, err := accountOf(ctx)
	if err != nil {
		return "", 0, err
	}
	w, err := c.load(account)
	if err != nil {
		return "", 0, err
	}
	if len(w.Sealed) == 0 {
		return "", 0, ErrNotConnected
	}
	var rec patRecord
	if err := c.sealer.Open(w.Sealed, associatedData(account, w.Generation), &rec); err != nil {
		return "", 0, err
	}
	return rec.Token, w.Generation, nil
}
