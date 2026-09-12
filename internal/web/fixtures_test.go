package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/nigelteosw/eggy/internal/ports"
)

// validConfig is the config.yaml the web tests write when they need a
// file that survives config.LoadConfig and Validate. internal/config keeps its
// own copy for its own tests: the two packages exercise different things, and
// a shared fixture would mean either exporting a test helper or letting a
// change made for one package's tests silently alter the other's.
func validConfig() string {
	return `
server:
  listen: ':8080'
  public_base_url: https://eggy.example
  telegram_webhook_path: /webhooks/telegram
data_dir: /data
telegram:
  owner_id: 42
agent:
  default_model: deepseek-pro
  timezone: UTC
providers:
  deepseek:
    adapter: openai_compatible
    base_url: https://api.deepseek.com
    api_key_env: DEEPSEEK_API_KEY
models:
  deepseek-pro:
    provider: deepseek
    model: deepseek-v4-pro
repositories:
  - name: repo
    clone_url: https://github.com/acme/repo.git
    base_branch: main
    protected_branches: [main]
runner:
  root: /data/runs
  timeout: 5m
  retention: 15m
  max_output_bytes: 1048576
  allowed_env: [PATH]
`
}

// asOwner is a context acting as the test deployment's one account, for
// tests that reach the private stores directly rather than through the
// session guard that would otherwise put the principal there.
func asOwner() context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "42"})
}

// ownerRequest is httptest.NewRequest with the owner's principal already on
// the context, as requireWebSession would have left it.
func ownerRequest(method, target string, body io.Reader) *http.Request {
	return httptest.NewRequest(method, target, body).WithContext(asOwner())
}
