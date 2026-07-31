// Package webui embeds Eggy's built web configuration UI and serves it. That
// is all it does: the signed session tokens and login throttling its login
// once carried live in plugins/auth/session, because session crypto is not an
// asset and an HTTP surface should not reach into an asset bundle for it.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Assets returns the embedded web UI build, rooted so paths like
// "index.html" and "assets/app.js" resolve directly (stripping the "dist/"
// prefix embed.FS otherwise keeps). Until `make build-web` has run, this
// serves the committed placeholder in dist/index.html.
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Unreachable: the go:embed directive above guarantees "dist" is a
		// directory in distFS at compile time.
		panic(err)
	}
	return sub
}
