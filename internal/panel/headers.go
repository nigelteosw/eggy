// Response headers every route carries: a content security policy, framing
// and sniffing refusals, a referrer policy and HSTS. They are set once, around
// the whole mux in NewHTTPHandler, so normal, setup and safe mode and every
// route added later get them without remembering to.
package panel

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/panel/webui"
)

// withSecurityHeaders sets the defaults before the handler runs, so a handler
// that needs something stricter (the login-link page's no-referrer) replaces
// the default rather than losing to it.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Security-Policy", contentSecurityPolicy())
		header.Set("X-Content-Type-Options", "nosniff")
		// frame-ancestors is the modern form; X-Frame-Options covers browsers
		// that predate it. Nothing frames the panel, so an approve button can
		// never be clicked through someone else's page.
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "same-origin")
		// The session cookie is Secure, so the panel is HTTPS-only already;
		// this keeps a browser from ever trying plain HTTP first. Browsers
		// ignore it on a plain-HTTP response, so a local dev server is
		// unaffected. No includeSubDomains: sibling hosts are not Eggy's to
		// decide.
		header.Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy is built once from the embedded bundle. Scripts come
// only from the panel itself plus the exact inline boot script index.html
// carries, allowed by hash rather than by 'unsafe-inline', so injected markup
// cannot run script. Images are same-origin, data: and blob: (pasted
// attachments) only: a remote image in rendered chat markdown is refused,
// which closes the image-URL route for exfiltrating conversation text.
// Styles allow inline because components set them; Google Fonts is the one
// third-party origin, matching the font list the look picker offers.
var contentSecurityPolicy = sync.OnceValue(func() string {
	scripts := []string{"'self'"}
	for _, hash := range inlineScriptHashes(shippedIndex()) {
		scripts = append(scripts, "'"+hash+"'")
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(scripts, " "),
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' https://fonts.gstatic.com",
		"img-src 'self' data: blob:",
		"connect-src 'self'",
		"manifest-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
})

// shippedIndex is the index.html the panel serves, or nothing in a binary
// built before the web bundle (the placeholder has no script).
func shippedIndex() []byte {
	page, err := fs.ReadFile(webui.Assets(), "index.html")
	if err != nil {
		return nil
	}
	return page
}

var scriptElement = regexp.MustCompile(`(?is)<script([^>]*)>(.*?)</script\s*>`)
var scriptSrcAttribute = regexp.MustCompile(`(?i)\ssrc\s*=`)

// inlineScriptHashes returns the CSP hash source of every script element
// without a src, over its exact text: the browser hashes the bytes between
// the tags, whitespace included.
func inlineScriptHashes(page []byte) []string {
	var hashes []string
	for _, match := range scriptElement.FindAllSubmatch(page, -1) {
		if scriptSrcAttribute.Match(match[1]) {
			continue
		}
		sum := sha256.Sum256(match[2])
		hashes = append(hashes, "sha256-"+base64.StdEncoding.EncodeToString(sum[:]))
	}
	return hashes
}
