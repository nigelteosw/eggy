package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type fakePreviewer struct {
	tools []MCPToolPreview
	err   error
}

func (f fakePreviewer) Preview(context.Context, string) ([]MCPToolPreview, error) {
	return f.tools, f.err
}

func passThroughGuard(next http.HandlerFunc) http.Handler { return next }

func previewResponse(t *testing.T, previewer MCPPreviewer) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerMCPApprovalRoutes(mux, passThroughGuard, "", previewer)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config/mcp/example/preview", nil))
	return rec
}

func TestMCPPreviewListsTools(t *testing.T) {
	rec := previewResponse(t, fakePreviewer{tools: []MCPToolPreview{{Name: "read", Description: "Reads"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Tools []MCPToolPreview `json:"tools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) != 1 || body.Tools[0].Name != "read" {
		t.Fatalf("tools = %+v", body.Tools)
	}
}

func TestMCPPreviewRestartIsConflict(t *testing.T) {
	rec := previewResponse(t, fakePreviewer{err: ErrMCPRestartRequired})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"restart":true`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestMCPPreviewLoginIsConflict(t *testing.T) {
	rec := previewResponse(t, fakePreviewer{err: ErrMCPLoginRequired})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"login":true`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

// With no MCP manager (no server at boot), a newly added server needs a
// restart, and the preview route says so rather than answering 404.
func TestMCPPreviewWithoutAManagerAsksForARestart(t *testing.T) {
	mux := http.NewServeMux()
	registerMCPApprovalRoutes(mux, passThroughGuard, "", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config/mcp/example/preview", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"restart":true`) {
		t.Fatalf("status %d body %s, want a restart instruction", rec.Code, rec.Body.String())
	}
}

func TestMCPApprovalRefusesUnknownServer(t *testing.T) {
	mux := http.NewServeMux()
	registerMCPApprovalRoutes(mux, passThroughGuard, filepath.Join(t.TempDir(), "config.yaml"), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/config/mcp/missing/approval", strings.NewReader(`{"enabled":true,"require_approval":["read"]}`))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 for an unreadable config", rec.Code)
	}
}
