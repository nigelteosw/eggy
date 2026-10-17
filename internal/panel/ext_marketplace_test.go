package panel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
