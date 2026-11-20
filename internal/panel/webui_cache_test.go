package panel

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// The panel's own files are cached by what they are. Vite names everything
// under assets/ by its content, so those are kept for a year and never
// revalidated; the shell that names them is revalidated on every open, so a
// deploy is seen at once and an unchanged shell costs a 304. Embedded files
// have no modification time, so without the ETag there was nothing to
// revalidate against and a phone downloaded the bundle again on each cold open.
func TestWebUIIsCachedByWhatEachFileIs(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":             {Data: []byte(`<!doctype html><script src="/assets/index-abc123.js"></script>`)},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
		"icon-192.png":           {Data: []byte("png")},
	}
	handler := webUIHandlerFor(assets)
	get := func(path string, header http.Header) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		for name, values := range header {
			request.Header[name] = values
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	asset := get("/assets/index-abc123.js", nil)
	if asset.Code != http.StatusOK || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset status=%d cache=%q", asset.Code, asset.Header().Get("Cache-Control"))
	}

	// A missing asset is not cached as if it were a hashed file: a 404 kept for
	// a year would outlive the deploy that fixed it.
	if missing := get("/assets/index-gone.js", nil); missing.Code != http.StatusNotFound || missing.Header().Get("Cache-Control") == "public, max-age=31536000, immutable" {
		t.Fatalf("missing asset status=%d cache=%q", missing.Code, missing.Header().Get("Cache-Control"))
	}

	for _, path := range []string{"/", "/home", "/settings"} {
		shell := get(path, nil)
		tag := shell.Header().Get("ETag")
		if shell.Code != http.StatusOK || shell.Header().Get("Cache-Control") != "no-cache" || tag == "" {
			t.Fatalf("%s status=%d cache=%q etag=%q", path, shell.Code, shell.Header().Get("Cache-Control"), tag)
		}
		again := get(path, http.Header{"If-None-Match": {tag}})
		if again.Code != http.StatusNotModified {
			t.Fatalf("%s revalidation status=%d, want 304", path, again.Code)
		}
	}

	// The sign-in link's landing page carries a token in its fragment and is
	// never stored, ETag or not.
	link := get("/auth/link", nil)
	if link.Header().Get("Cache-Control") != "no-store" || link.Header().Get("ETag") != "" {
		t.Fatalf("/auth/link cache=%q etag=%q", link.Header().Get("Cache-Control"), link.Header().Get("ETag"))
	}

	if icon := get("/icon-192.png", nil); icon.Header().Get("Cache-Control") != "public, max-age=86400" {
		t.Fatalf("icon cache=%q", icon.Header().Get("Cache-Control"))
	}
}
