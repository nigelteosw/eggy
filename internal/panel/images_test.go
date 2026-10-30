package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeImageReader map[string]ports.ContentPart

func (f fakeImageReader) Image(_ context.Context, id string) (ports.ContentPart, bool, error) {
	image, found := f[id]
	return image, found, nil
}

// A drawn image is served to a signed-in session as the type it was stored
// with, never sniffed into something else, and kept out of shared caches: it
// is one account's, read through that account's session.
func TestImageRouteServesAStoredImage(t *testing.T) {
	t.Parallel()

	webConfig := testWebConfig(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Images = fakeImageReader{"img-1": {Type: ports.ModalityImage, MediaType: "image/png", Data: []byte("\x89PNG pixels")}}
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)

	get := func(path string, signedIn bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if signedIn {
			attachSession(request, cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	response := get("/api/images/img-1", true)
	if response.Code != http.StatusOK || response.Body.String() != "\x89PNG pixels" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type=%q", got)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "private, max-age=86400" {
		t.Fatalf("headers=%v", response.Header())
	}
	if code := get("/api/images/absent", true).Code; code != http.StatusNotFound {
		t.Fatalf("absent status=%d, want 404", code)
	}
	if code := get("/api/images/img-1", false).Code; code == http.StatusOK {
		t.Fatal("an image must not be served without a session")
	}
}

// With tracing off nothing is stored, and there is no route to ask.
func TestImageRouteIsAbsentWithoutAStore(t *testing.T) {
	t.Parallel()

	handler := NewWebHandler("", testWebConfig(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	cookie := webLoginCookie(t, handler)
	request := httptest.NewRequest(http.MethodGet, "/api/images/img-1", nil)
	attachSession(request, cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Content-Type") == "image/png" {
		t.Fatalf("served an image with no store: %d", response.Code)
	}
}
