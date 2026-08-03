// Package webui embeds Eggy's built web configuration UI and serves it. That
// is all it does: the signed session tokens and login throttling its login
// once carried live in plugins/auth/session, because session crypto is not an
// asset and an HTTP surface should not reach into an asset bundle for it.
package webui

import (
	"embed"
	"errors"
	"io/fs"
)

// Only placeholder.html is tracked in dist/; the bundle vite writes beside it
// (index.html and assets/) is ignored. Two names rather than one is what keeps
// `git status` clean across a build: git tracks a file the build never
// rewrites, and go:embed still has a directory to resolve at compile time, so
// a fresh clone builds and tests without the web toolchain.
//
//go:embed all:dist
var distFS embed.FS

// Assets returns the embedded web UI build, rooted so paths like "index.html"
// and "assets/app.js" resolve directly (stripping the "dist/" prefix embed.FS
// otherwise keeps). Until `make build-web` has run there is no index.html to
// serve, and the placeholder stands in for it.
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Unreachable: the go:embed directive above guarantees "dist" is a
		// directory in distFS at compile time.
		panic(err)
	}
	return placeholderFS{sub}
}

// placeholderFS serves placeholder.html in place of a missing index.html, so
// an unbuilt binary answers the root route with an explanation rather than a
// bare 404. It substitutes only that one name: a missing asset stays missing,
// because a page reporting success while its script 404s is the failure this
// whole arrangement exists to prevent.
type placeholderFS struct{ fs.FS }

func (f placeholderFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if name == "index.html" && errors.Is(err, fs.ErrNotExist) {
		return f.FS.Open("placeholder.html")
	}
	return file, err
}
