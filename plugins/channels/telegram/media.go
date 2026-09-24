package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// maxFileBytes is also Telegram's own ceiling for a bot download.
const maxFileBytes int64 = 20 << 20

// DownloadFile resolves a Telegram-owned file ID and returns verified bytes
// as the part their sniffed type makes them: an image, or a PDF document
// named by filename. The authenticated download URL never leaves this
// adapter.
func (c *Client) DownloadFile(ctx context.Context, fileID string, declaredSize int64, declaredMediaType, filename string) (ports.ContentPart, error) {
	if strings.TrimSpace(fileID) == "" {
		return ports.ContentPart{}, errors.New("Telegram file is missing a file ID")
	}
	if declaredSize < 0 {
		return ports.ContentPart{}, errors.New("Telegram file has an invalid negative size")
	}
	if declaredSize > maxFileBytes {
		return ports.ContentPart{}, fmt.Errorf("Telegram file exceeds the %d MB limit", maxFileBytes>>20)
	}
	declaredType, _, ok := canonicalMediaType(declaredMediaType)
	if declaredMediaType != "" && !ok {
		return ports.ContentPart{}, fmt.Errorf("unsupported Telegram file media type %q", declaredMediaType)
	}

	result, err := c.call(ctx, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return ports.ContentPart{}, fmt.Errorf("resolve Telegram file: %w", err)
	}
	var file struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal(result, &file); err != nil {
		return ports.ContentPart{}, errors.New("decode Telegram file metadata")
	}
	if strings.TrimSpace(file.FilePath) == "" {
		return ports.ContentPart{}, errors.New("Telegram file metadata has no file path")
	}
	if file.FileSize < 0 {
		return ports.ContentPart{}, errors.New("Telegram file metadata has an invalid negative size")
	}
	if file.FileSize > maxFileBytes {
		return ports.ContentPart{}, fmt.Errorf("Telegram file exceeds the %d MB limit", maxFileBytes>>20)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/file/bot"+c.token+"/"+strings.TrimLeft(file.FilePath, "/"), nil)
	if err != nil {
		return ports.ContentPart{}, errors.New("build Telegram file download")
	}
	downloadClient := *c.http
	downloadClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := downloadClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ports.ContentPart{}, ctx.Err()
		}
		return ports.ContentPart{}, errors.New("download Telegram file")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ports.ContentPart{}, fmt.Errorf("download Telegram file: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxFileBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ports.ContentPart{}, ctx.Err()
		}
		return ports.ContentPart{}, errors.New("read Telegram file")
	}
	if int64(len(data)) > maxFileBytes {
		return ports.ContentPart{}, fmt.Errorf("Telegram file exceeds the %d MB limit", maxFileBytes>>20)
	}
	detectedType, kind, ok := canonicalMediaType(http.DetectContentType(data))
	if !ok {
		return ports.ContentPart{}, errors.New("Telegram file is not a supported image or PDF")
	}
	if declaredType != "" && declaredType != detectedType {
		return ports.ContentPart{}, fmt.Errorf("Telegram file media type mismatch: declared %s, detected %s", declaredType, detectedType)
	}
	part := ports.ContentPart{Type: kind, MediaType: detectedType, Data: data}
	if kind == ports.ModalityFile {
		part.Filename = strings.TrimSpace(filename)
		if part.Filename == "" {
			part.Filename = "document.pdf"
		}
	}
	return part, nil
}

// canonicalMediaType maps a declared or sniffed media type onto the one
// spelling Eggy sends and the modality it becomes. Anything else is not
// something a model is asked to read.
func canonicalMediaType(mediaType string) (canonical string, kind ports.Modality, ok bool) {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0])) {
	case "image/jpeg", "image/jpg":
		return "image/jpeg", ports.ModalityImage, true
	case "image/png":
		return "image/png", ports.ModalityImage, true
	case "image/webp":
		return "image/webp", ports.ModalityImage, true
	case "image/gif":
		return "image/gif", ports.ModalityImage, true
	case "application/pdf":
		return "application/pdf", ports.ModalityFile, true
	default:
		return "", "", false
	}
}
