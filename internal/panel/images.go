package panel

import (
	"context"
	"net/http"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ImageReader is the read half of ports.ImageStore. Storing an image belongs
// to the turn that drew it; the panel only shows it back to its owner.
type ImageReader interface {
	Image(ctx context.Context, id string) (image ports.ContentPart, found bool, err error)
}

// newImageHandler serves one stored image to the account that owns it. It is
// how the web chat draws an image marker in a reply, live or after a reload.
func newImageHandler(images ImageReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		image, found, err := images.Image(r.Context(), r.PathValue("id"))
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Only a raster type is ever stored, but the header is written from
		// what the store returned, so it is checked again here rather than
		// trusted: this route answers from the panel's own origin.
		if !found || !strings.HasPrefix(image.MediaType, "image/") || image.MediaType == "image/svg+xml" {
			writeWebError(w, http.StatusNotFound, "image not found")
			return
		}
		w.Header().Set("Content-Type", image.MediaType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// An image never changes under its id, so a day in the browser's own
		// cache is safe; private keeps it out of anything shared.
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = w.Write(image.Data)
	}
}
