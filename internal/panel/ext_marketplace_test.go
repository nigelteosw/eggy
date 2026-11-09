package panel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestMarketplaceFailureStatuses(t *testing.T) {
	reset := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"digest", ErrMarketplaceDigest, http.StatusConflict},
		{"conflict", MarketplaceConflictError{Conflicts: []string{"pdf"}}, http.StatusConflict},
		{"already installed", ports.ErrMarketplaceInstallExists, http.StatusConflict},
		{"not found", ports.ErrMarketplaceInstallNotFound, http.StatusNotFound},
		{"rate limit", MarketplaceRateLimitError{Reset: reset}, http.StatusServiceUnavailable},
		{"archive", fmt.Errorf("wrapped: %w", ErrMarketplaceArchive), http.StatusUnprocessableEntity},
		{"unknown", fmt.Errorf("disk on fire"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeMarketplaceFailure(rec, tc.err)
		if rec.Code != tc.status {
			t.Fatalf("%s: status %d, want %d", tc.name, rec.Code, tc.status)
		}
	}
}

func TestMarketplaceConflictBodyListsNames(t *testing.T) {
	rec := httptest.NewRecorder()
	writeMarketplaceFailure(rec, MarketplaceConflictError{Conflicts: []string{"pdf", "docx"}})
	var body struct {
		Conflicts []string `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Conflicts) != 2 || body.Conflicts[0] != "pdf" {
		t.Fatalf("conflicts = %v", body.Conflicts)
	}
}

// A server a plugin brought is labelled with the install it came from; one the
// owner added by hand is not, and with the marketplace off there is no column.
func TestMCPListLabelsServersFromAPlugin(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	for _, name := range []string{"deploy-api", "railway"} {
		body := strings.NewReader(`{"name":"` + name + `","url":"https://mcp.example.com/` + name + `","auth":"none","enabled":"false"}`)
		rec := httptest.NewRecorder()
		webMCPSetRoute(path)(rec, httptest.NewRequest(http.MethodPost, "/api/config/mcp", body))
		if rec.Code != http.StatusOK {
			t.Fatalf("add %s: status %d body %s", name, rec.Code, rec.Body.String())
		}
	}
	list := func(ownerOf func(string) string) webResult {
		t.Helper()
		rec := httptest.NewRecorder()
		webMCPListRoute(path, ownerOf)(rec, httptest.NewRequest(http.MethodGet, "/api/config/mcp", nil))
		var result webResult
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	labelled := list(func(name string) string {
		if name == "deploy-api" {
			return "team/deploy-tools"
		}
		return ""
	})
	last := len(labelled.TableHeaders) - 1
	if labelled.TableHeaders[last] != "From" {
		t.Fatalf("headers = %v, want a trailing From column", labelled.TableHeaders)
	}
	from := map[string]string{}
	for _, row := range labelled.TableRows {
		from[row[0]] = row[last]
	}
	if from["deploy-api"] != "Plugin team/deploy-tools" || from["railway"] != "" {
		t.Fatalf("from = %v", from)
	}

	plain := list(nil)
	if slices.Contains(plain.TableHeaders, "From") {
		t.Fatalf("headers = %v, want no From column with the marketplace off", plain.TableHeaders)
	}
}
