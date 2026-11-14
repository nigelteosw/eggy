package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// countingMarketplace answers every call with nothing and counts the calls, so
// a test can tell a guarded-out request never reached the service.
type countingMarketplace struct{ calls int }

func (m *countingMarketplace) Sources(context.Context) ([]MarketplaceSourceStatus, error) {
	m.calls++
	return nil, nil
}

func (m *countingMarketplace) Items(context.Context, string) ([]MarketplaceItem, error) {
	m.calls++
	return nil, nil
}

func (m *countingMarketplace) Plan(context.Context, MarketplacePlanRequest) (MarketplacePlan, error) {
	m.calls++
	return MarketplacePlan{}, nil
}

func (m *countingMarketplace) Install(context.Context, MarketplaceApproval) (MarketplaceInstall, error) {
	m.calls++
	return MarketplaceInstall{}, nil
}

func (m *countingMarketplace) Installs(context.Context) ([]MarketplaceInstallStatus, error) {
	m.calls++
	return nil, nil
}

func (m *countingMarketplace) Check(context.Context, string) (MarketplaceInstallStatus, MarketplacePlan, error) {
	m.calls++
	return MarketplaceInstallStatus{}, MarketplacePlan{}, nil
}

func (m *countingMarketplace) Update(context.Context, string, MarketplaceApproval, []string) (MarketplaceInstall, error) {
	m.calls++
	return MarketplaceInstall{}, nil
}

func (m *countingMarketplace) SetDisabled(context.Context, string, bool) error {
	m.calls++
	return nil
}

func (m *countingMarketplace) Remove(context.Context, string, []MarketplaceFileRef) error {
	m.calls++
	return nil
}

func (m *countingMarketplace) OwnerOf(string) string { return "" }

// Every marketplace route needs a session, and every one that writes needs the
// panel's CSRF token and a same-origin request. A refused request never reaches
// the service.
func TestMarketplaceRoutesRequireSessionAndCSRF(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	cfg, database, _ := accountWebConfig(t, now)
	svc := &countingMarketplace{}
	cfg.Marketplace = svc
	handler := NewWebHandler(writeConfigFile(t, validConfig()), cfg)
	cookie, csrf := signIn(t, handler, database, "partner", now)

	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/config/marketplace/sources", ""},
		{http.MethodPut, "/api/config/marketplace/sources", `{"sources":[]}`},
		{http.MethodGet, "/api/marketplace/sources", ""},
		{http.MethodGet, "/api/marketplace/items?source=team", ""},
		{http.MethodPost, "/api/marketplace/plan", `{}`},
		{http.MethodPost, "/api/marketplace/installs", `{}`},
		{http.MethodGet, "/api/marketplace/installs", ""},
		{http.MethodPost, "/api/marketplace/installs/abc/check", ""},
		{http.MethodPut, "/api/marketplace/installs/abc", `{}`},
		{http.MethodPost, "/api/marketplace/installs/abc/disabled", `{"disabled":true}`},
		{http.MethodDelete, "/api/marketplace/installs/abc", `{}`},
	}
	send := func(method, path, body string, withCookie bool, token, origin string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if withCookie {
			req.AddCookie(cookie)
		}
		if token != "" {
			req.Header.Set(csrfHeader, token)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, r := range routes {
		if code := send(r.method, r.path, r.body, false, csrf, ""); code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", r.method, r.path, code)
		}
		if r.method == http.MethodGet {
			continue
		}
		if code := send(r.method, r.path, r.body, true, "", ""); code != http.StatusForbidden {
			t.Errorf("%s %s without CSRF = %d, want 403", r.method, r.path, code)
		}
		if code := send(r.method, r.path, r.body, true, "wrong-token", ""); code != http.StatusForbidden {
			t.Errorf("%s %s with a wrong CSRF token = %d, want 403", r.method, r.path, code)
		}
		if code := send(r.method, r.path, r.body, true, csrf, "https://evil.example"); code != http.StatusForbidden {
			t.Errorf("%s %s cross-origin = %d, want 403", r.method, r.path, code)
		}
	}
	if svc.calls != 0 {
		t.Fatalf("refused requests reached the service %d time(s)", svc.calls)
	}
	// The same requests with a session and CSRF get through the guard.
	for _, r := range routes {
		if code := send(r.method, r.path, r.body, true, csrf, ""); code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Errorf("%s %s with a session and CSRF = %d, want the guard to pass", r.method, r.path, code)
		}
	}
}
