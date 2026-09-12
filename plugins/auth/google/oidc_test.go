package google

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// fakeProvider stands in for Google: it serves a JWKS and a token endpoint
// that mints whatever ID token the test asks for. It signs with a key it
// generated, so a token signed by any other key -- "wrong signature" -- is
// one signed by a second fakeProvider.
type fakeProvider struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	issuer   string
	tokenFor func(r *http.Request) map[string]any
	lastForm url.Values
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/certs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.lastForm = r.PostForm
		claims := p.tokenFor(r)
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(claims)
		signed, err := signer.Sign(body)
		if err != nil {
			t.Fatal(err)
		}
		compact, _ := signed.CompactSerialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "dropped", "token_type": "Bearer", "id_token": compact})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	p.issuer = p.server.URL
	return p
}

// install points the package's fixed endpoints at the fake for one test.
func (p *fakeProvider) install(t *testing.T) {
	t.Helper()
	oldIssuer, oldAuth, oldToken, oldJWKS := issuerURL, authURL, tokenURL, jwksURL
	issuerURL, authURL, tokenURL, jwksURL = p.issuer, p.server.URL+"/auth", p.server.URL+"/token", p.server.URL+"/certs"
	t.Cleanup(func() { issuerURL, authURL, tokenURL, jwksURL = oldIssuer, oldAuth, oldToken, oldJWKS })
}

func (p *fakeProvider) claims(nonce string, overrides map[string]any) map[string]any {
	claims := map[string]any{
		"iss": p.issuer, "aud": "web-client", "sub": "subject-1",
		"email": "Nigel@Example.com", "email_verified": true,
		"nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
	for key, value := range overrides {
		claims[key] = value
	}
	return claims
}

func newClient(t *testing.T) *Client {
	t.Helper()
	client, err := New("web-client", "secret", "https://eggy.example/auth/google/callback", nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestBeginBuildsAnAuthorizationURLWithStateNonceAndPKCE(t *testing.T) {
	newFakeProvider(t).install(t)
	client := newClient(t)
	raw, err := client.Begin(context.Background(), "state-1", "nonce-1", "verifier-verifier-verifier-verifier-verifier")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("state") != "state-1" || q.Get("nonce") != "nonce-1" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("query=%v", q)
	}
	if q.Get("scope") != "openid email profile" || q.Get("redirect_uri") != "https://eggy.example/auth/google/callback" || q.Get("client_id") != "web-client" {
		t.Fatalf("query=%v", q)
	}
	if strings.Contains(raw, "verifier-verifier") {
		t.Fatal("the raw verifier must never travel in the URL")
	}
	if _, err := client.Begin(context.Background(), "", "n", "v"); err == nil {
		t.Fatal("blank state accepted")
	}
}

func TestCompleteVerifiesTheIDToken(t *testing.T) {
	provider := newFakeProvider(t)
	provider.install(t)
	client := newClient(t)
	provider.tokenFor = func(*http.Request) map[string]any { return provider.claims("nonce-1", nil) }
	identity, err := client.Complete(context.Background(), "code", "nonce-1", "verifier-verifier-verifier-verifier-verifier")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Issuer != provider.issuer || identity.Subject != "subject-1" || identity.Email != "nigel@example.com" || !identity.EmailVerified {
		t.Fatalf("identity=%+v", identity)
	}
	if provider.lastForm.Get("code_verifier") == "" || provider.lastForm.Get("code") != "code" {
		t.Fatalf("exchange form=%v", provider.lastForm)
	}
}

func TestCompleteRejectsABadToken(t *testing.T) {
	other := newFakeProvider(t)
	cases := map[string]func(p *fakeProvider) map[string]any{
		"wrong audience": func(p *fakeProvider) map[string]any {
			return p.claims("nonce-1", map[string]any{"aud": "someone-else"})
		},
		"wrong issuer": func(p *fakeProvider) map[string]any {
			return p.claims("nonce-1", map[string]any{"iss": "https://evil.example"})
		},
		"wrong nonce": func(p *fakeProvider) map[string]any { return p.claims("nonce-2", nil) },
		"expired": func(p *fakeProvider) map[string]any {
			return p.claims("nonce-1", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
		},
		"missing subject": func(p *fakeProvider) map[string]any { return p.claims("nonce-1", map[string]any{"sub": ""}) },
	}
	for name, claims := range cases {
		t.Run(name, func(t *testing.T) {
			provider := newFakeProvider(t)
			provider.install(t)
			client := newClient(t)
			provider.tokenFor = func(*http.Request) map[string]any { return claims(provider) }
			if _, err := client.Complete(context.Background(), "code", "nonce-1", "verifier-verifier-verifier-verifier-verifier"); err == nil {
				t.Fatal("bad token accepted")
			}
		})
	}
	t.Run("wrong signature", func(t *testing.T) {
		provider := newFakeProvider(t)
		provider.install(t)
		// Signed by another provider's key, but claiming this issuer.
		provider.key = other.key
		client := newClient(t)
		provider.tokenFor = func(*http.Request) map[string]any { return provider.claims("nonce-1", nil) }
		if _, err := client.Complete(context.Background(), "code", "nonce-1", "verifier-verifier-verifier-verifier-verifier"); err == nil {
			t.Fatal("token with a foreign signature accepted")
		}
	})
	t.Run("unverified email is reported not hidden", func(t *testing.T) {
		provider := newFakeProvider(t)
		provider.install(t)
		client := newClient(t)
		provider.tokenFor = func(*http.Request) map[string]any {
			return provider.claims("nonce-1", map[string]any{"email_verified": false})
		}
		identity, err := client.Complete(context.Background(), "code", "nonce-1", "verifier-verifier-verifier-verifier-verifier")
		if err != nil {
			t.Fatal(err)
		}
		if identity.EmailVerified {
			t.Fatal("unverified email reported as verified")
		}
	})
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New("", "secret", "https://x/cb", nil); err == nil {
		t.Fatal("blank client id accepted")
	}
	if _, err := New("id", "", "https://x/cb", nil); err == nil {
		t.Fatal("blank secret accepted")
	}
	if _, err := New("id", "secret", "", nil); err == nil {
		t.Fatal("blank redirect accepted")
	}
}
