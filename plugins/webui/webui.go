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
