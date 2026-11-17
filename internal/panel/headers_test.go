package panel

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	handler := NewHTTPHandler(Routes{Web: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})})
	for _, path := range []string{"/", "/api/mode", "/healthz", DefaultTelegramWebhookPath} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		header := response.Header()
		csp := header.Get("Content-Security-Policy")
		for _, directive := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'", "connect-src 'self'"} {
			if !strings.Contains(csp, directive) {
				t.Fatalf("%s: CSP %q lacks %q", path, csp, directive)
			}
		}
		if strings.Contains(csp, "'unsafe-eval'") || strings.Contains(scriptSources(csp), "'unsafe-inline'") {
			t.Fatalf("%s: CSP allows inline or eval script: %q", path, csp)
		}
		want := map[string]string{
			"X-Content-Type-Options":    "nosniff",
			"X-Frame-Options":           "DENY",
			"Referrer-Policy":           "same-origin",
			"Strict-Transport-Security": "max-age=31536000",
		}
		for name, value := range want {
			if got := header.Get(name); got != value {
				t.Fatalf("%s: %s=%q want %q", path, name, got, value)
			}
		}
	}
}

// The login-link page's stricter referrer policy is set by the handler after
// the defaults and must win.
func TestHandlerCanTightenTheDefaultReferrerPolicy(t *testing.T) {
	handler := NewHTTPHandler(Routes{Web: NewWebHandler("", WebUIConfig{})})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/auth/link", nil))
	if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy=%q", got)
	}
}

func TestInlineScriptHashesCoverOnlyInlineScripts(t *testing.T) {
	page := "<html><head><script>\n  boot();\n</script>" +
		`<script type="module" crossorigin src="/assets/app.js"></script>` +
		"<SCRIPT nonce=x>second()</SCRIPT></head></html>"
	got := inlineScriptHashes([]byte(page))
	want := []string{scriptHash("\n  boot();\n"), scriptHash("second()")}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("hashes=%v want %v", got, want)
	}
}

// The shipped index.html's inline boot script must be allowed by the policy
// the server sends, or the panel paints light before every dark-mode load.
func TestPolicyAllowsTheShippedInlineScripts(t *testing.T) {
	csp := contentSecurityPolicy()
	for _, hash := range inlineScriptHashes(shippedIndex()) {
		if !strings.Contains(scriptSources(csp), "'"+hash+"'") {
			t.Fatalf("CSP %q does not allow %s", csp, hash)
		}
	}
}

func scriptHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

func scriptSources(csp string) string {
	for _, directive := range strings.Split(csp, ";") {
		if strings.HasPrefix(strings.TrimSpace(directive), "script-src ") {
			return directive
		}
	}
	return ""
}
