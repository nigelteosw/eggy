package agentrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/auth/grants"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// memRecords is an in-memory grants.Records for tests.
type memRecords struct{ rows map[string]json.RawMessage }

func newMemRecords() *memRecords { return &memRecords{rows: map[string]json.RawMessage{}} }

func (m *memRecords) Read(section, key string) (json.RawMessage, error) {
	v, ok := m.rows[section+"/"+key]
	if !ok {
		return nil, grants.ErrNotFound
	}
	return v, nil
}
func (m *memRecords) Write(section, key string, record json.RawMessage) error {
	m.rows[section+"/"+key] = record
	return nil
}
func (m *memRecords) Update(section, key string, mutate func(json.RawMessage) (json.RawMessage, error)) error {
	cur, _ := m.Read(section, key)
	next, err := mutate(cur)
	if err != nil {
		return err
	}
	return m.Write(section, key, next)
}
func (m *memRecords) Delete(section, key string) error {
	delete(m.rows, section+"/"+key)
	return nil
}

const testEncryptionKey = "a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s=" // 32 bytes

func acct(id string) context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: id})
}

// fakeGitHubForPAT answers the identity and per-repository calls Set makes.
func fakeGitHubForPAT(t *testing.T, pushOK bool) (*Credentials, *memRecords) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"nigel","id":42}`))
		case "/repos/o/r":
			body := `{"permissions":{"push":false}}`
			if pushOK {
				body = `{"permissions":{"push":true}}`
			}
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	gh := NewGitHub(srv.Client())
	gh.baseURL = srv.URL
	records := newMemRecords()
	creds, err := NewCredentials(records, testEncryptionKey, gh)
	if err != nil {
		t.Fatal(err)
	}
	return creds, records
}

var testRepos = []config.RepositoryConfig{{Name: "r", CloneURL: "https://github.com/o/r.git", BaseBranch: "main"}}

func TestCredentialsSetVerifiesIdentityAndAccess(t *testing.T) {
	creds, _ := fakeGitHubForPAT(t, true)
	status, err := creds.Set(acct("a"), "ghp_secret", testRepos)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Connected || status.Login != "nigel" || status.Generation != 1 || !status.Repositories["r"] {
		t.Fatalf("status = %+v", status)
	}
	got, err := creds.Status(acct("a"))
	if err != nil || !got.Connected || got.Login != "nigel" {
		t.Fatalf("Status = %+v err=%v", got, err)
	}
}

func TestCredentialsNeverReturnToken(t *testing.T) {
	creds, _ := fakeGitHubForPAT(t, true)
	status, err := creds.Set(acct("a"), "ghp_secret", testRepos)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(status)
	if strings.Contains(string(body), "ghp_secret") {
		t.Fatalf("status JSON leaks the token: %s", body)
	}
}

func TestCredentialsScopedToAccount(t *testing.T) {
	creds, _ := fakeGitHubForPAT(t, true)
	if _, err := creds.Set(acct("a"), "ghp_secret", testRepos); err != nil {
		t.Fatal(err)
	}
	other, err := creds.Status(acct("b"))
	if err != nil || other.Connected {
		t.Fatalf("account b sees a's PAT: %+v err=%v", other, err)
	}
	if _, _, err := creds.token(acct("b")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("account b token = %v, want ErrNotConnected", err)
	}
}

func TestCredentialsReplaceAdvancesGeneration(t *testing.T) {
	creds, records := fakeGitHubForPAT(t, true)
	if _, err := creds.Set(acct("a"), "ghp_one", testRepos); err != nil {
		t.Fatal(err)
	}
	second, err := creds.Set(acct("a"), "ghp_two", testRepos)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 {
		t.Fatalf("generation = %d, want 2", second.Generation)
	}
	// A sealed body made under generation 1's associated data must not open
	// under generation 2.
	sealer, err := grants.NewSealer("agent runner PAT", testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := sealer.Seal(patRecord{Token: "ghp_stale"}, []byte("agent_runner|github|a|1"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(patWrapper{Generation: 2, Sealed: stale})
	_ = records.Write(patSection, "a", body)
	if _, _, err := creds.token(acct("a")); err == nil {
		t.Fatal("record sealed under an older generation opened")
	}
}

func TestCredentialsDisconnectInvalidates(t *testing.T) {
	creds, _ := fakeGitHubForPAT(t, true)
	if _, err := creds.Set(acct("a"), "ghp_secret", testRepos); err != nil {
		t.Fatal(err)
	}
	if err := creds.Disconnect(acct("a")); err != nil {
		t.Fatal(err)
	}
	status, err := creds.Status(acct("a"))
	if err != nil || status.Connected || status.Generation != 2 {
		t.Fatalf("after disconnect: %+v err=%v", status, err)
	}
	if _, _, err := creds.token(acct("a")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("token after disconnect = %v", err)
	}
}

func TestCredentialsRequirePrincipal(t *testing.T) {
	creds, _ := fakeGitHubForPAT(t, true)
	if _, err := creds.Set(context.Background(), "ghp_secret", testRepos); !errors.Is(err, ports.ErrNoPrincipal) {
		t.Fatalf("Set without principal = %v, want ErrNoPrincipal", err)
	}
}

func TestCredentialsKeyMustBeThirtyTwoBytes(t *testing.T) {
	if _, err := NewCredentials(newMemRecords(), base64.StdEncoding.EncodeToString([]byte("short")), NewGitHub(nil)); err == nil {
		t.Fatal("a short encryption key was accepted")
	}
}
