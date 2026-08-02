package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeToolCatalog struct{ listings []services.ToolListing }

func (c fakeToolCatalog) Catalog() []services.ToolListing { return c.listings }

func TestWebToolListReportsNameSourceAndDescriptionInCatalogOrder(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(now)
	config.Tools = fakeToolCatalog{listings: []services.ToolListing{
		{Source: services.SourceKernel, Definition: ports.ToolDefinition{Name: "current_time", Description: "The time now"}},
		{Source: "mcp", Definition: ports.ToolDefinition{Name: "calendar__list_events", Description: "List events"}},
	}}
	handler := NewWebHandler("", config)
	cookie := webLoginCookie(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result webResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"current_time", "kernel", "The time now"},
		{"calendar__list_events", "mcp", "List events"},
	}
	if len(result.TableRows) != len(want) {
		t.Fatalf("rows=%v", result.TableRows)
	}
	for i, row := range want {
		for j, cell := range row {
			if result.TableRows[i][j] != cell {
				t.Fatalf("row %d = %v, want %v", i, result.TableRows[i], row)
			}
		}
	}
}

func TestWebToolListRequiresSession(t *testing.T) {
	handler := NewWebHandler("", testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/tools", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", response.Code)
	}
}
