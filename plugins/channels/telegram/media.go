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

const maxImageBytes int64 = 20 << 20

// DownloadImage resolves a Telegram-owned file ID and returns verified image
// bytes. The authenticated download URL never leaves this adapter.
func (c *Client) DownloadImage(ctx context.Context, fileID string, declaredSize int64, declaredMediaType string) (ports.ContentPart, error) {
	if strings.TrimSpace(fileID) == "" {
		return ports.ContentPart{}, errors.New("Telegram image is missing a file ID")
	}
	if declaredSize < 0 {
		return ports.ContentPart{}, errors.New("Telegram image has an invalid negative size")
	}
	if declaredSize > maxImageBytes {
		return ports.ContentPart{}, fmt.Errorf("Telegram image exceeds the %d MB limit", maxImageBytes>>20)
	}
	declaredType, ok := canonicalImageType(declaredMediaType)
	if declaredMediaType != "" && !ok {
		return ports.ContentPart{}, fmt.Errorf("unsupported Telegram image media type %q", declaredMediaType)
	}

	result, err := c.call(ctx, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return ports.ContentPart{}, fmt.Errorf("resolve Telegram image: %w", err)
	}
	var file struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal(result, &file); err != nil {
		return ports.ContentPart{}, errors.New("decode Telegram image metadata")
	}
	if strings.TrimSpace(file.FilePath) == "" {
		return ports.ContentPart{}, errors.New("Telegram image metadata has no file path")
	}
	if file.FileSize < 0 {
		return ports.ContentPart{}, errors.New("Telegram image metadata has an invalid negative size")
	}
	if file.FileSize > maxImageBytes {
		return ports.ContentPart{}, fmt.Errorf("Telegram image exceeds the %d MB limit", maxImageBytes>>20)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/file/bot"+c.token+"/"+strings.TrimLeft(file.FilePath, "/"), nil)
	if err != nil {
		return ports.ContentPart{}, errors.New("build Telegram image download")
	}
	downloadClient := *c.http
	downloadClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := downloadClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ports.ContentPart{}, ctx.Err()
		}
		return ports.ContentPart{}, errors.New("download Telegram image")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ports.ContentPart{}, fmt.Errorf("download Telegram image: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ports.ContentPart{}, ctx.Err()
		}
		return ports.ContentPart{}, errors.New("read Telegram image")
	}
	if int64(len(data)) > maxImageBytes {
		return ports.ContentPart{}, fmt.Errorf("Telegram image exceeds the %d MB limit", maxImageBytes>>20)
	}
	detectedType, ok := canonicalImageType(http.DetectContentType(data))
	if !ok {
		return ports.ContentPart{}, errors.New("Telegram file is not a supported image")
	}
	if declaredType != "" && declaredType != detectedType {
		return ports.ContentPart{}, fmt.Errorf("Telegram image media type mismatch: declared %s, detected %s", declaredType, detectedType)
	}
	return ports.ContentPart{Type: ports.ContentTypeImage, MediaType: detectedType, Data: data}, nil
}

func canonicalImageType(mediaType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0])) {
	case "":
		return "", false
	case "image/jpeg", "image/jpg":
		return "image/jpeg", true
	case "image/png":
		return "image/png", true
	case "image/webp":
		return "image/webp", true
	case "image/gif":
		return "image/gif", true
	default:
		return "", false
	}
}
