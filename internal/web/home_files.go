package web

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/plugins/atomicfile"
	"github.com/nigelteosw/eggy/plugins/filelock"
	"gopkg.in/yaml.v3"
)

// maxEditableBytes caps both what the files API will serve and what it will
// accept. These are hand-edited config and prose files; anything larger is
// not something to load into a browser textarea.
const maxEditableBytes = 1 << 20

var (
	// ErrFileForbidden marks a path that exists in the home but is
	// deliberately not reachable through the API.
	ErrFileForbidden = errors.New("file is not accessible")
	// ErrFileReadOnly marks a path that may be read but never written.
	ErrFileReadOnly = errors.New("file is read-only")
	// ErrFileNotFound marks a path with nothing behind it.
	ErrFileNotFound = errors.New("file not found")
)

// FileAccess is what the owner may do with one entry through the web UI.
type FileAccess string

const (
	// AccessEdit means the raw text is served and can be written back.
	AccessEdit FileAccess = "edit"
	// AccessRead means the text is served but writes are refused: logs,
	// which Eggy owns, and state files an owner should not hand-edit.
	AccessRead FileAccess = "read"
	// AccessSecret means the entry is listed -- so an owner can see it
	// exists and when it changed -- but its contents never leave the host.
	// .env and auth.json carry live credentials, and an HTTP session is a
	// weaker boundary than the file mode they already have.
	AccessSecret FileAccess = "secret"
)

// HomeFile is one entry in the home listing.
type HomeFile struct {
	Path     string     `json:"path"`
	Access   FileAccess `json:"access"`
	Size     int64      `json:"size"`
	Modified string     `json:"modified,omitempty"`
	Language string     `json:"language,omitempty"`
	Missing  bool       `json:"missing,omitempty"`
	Note     string     `json:"note,omitempty"`
}

// homeArea describes one browsable slot in the layout. Fixed files are
// listed even when absent, so an owner can see that SOUL.md is a thing that
// exists before Eggy has written it.
type homeArea struct {
	// Path is the slash-separated path relative to the home root: either a
	// single file, or a directory scanned for Extensions.
	Path       string
	Directory  bool
	Access     FileAccess
	Extensions []string
	Note       string
}

// homeAreas is the whole allowlist. A path that does not match one of these
// is not reachable through the API at all -- state.json, eggy.db, runs/,
// changes/, and sessions/ are Eggy's bookkeeping and stay off the surface.
var homeAreas = []homeArea{
	{Path: "config.yaml", Access: AccessEdit, Note: "Settings. Eggy re-reads this on restart."},
	{Path: "SOUL.md", Access: AccessEdit, Note: "Agent identity, first in the system prompt."},
	{Path: "HEARTBEAT.md", Access: AccessEdit, Note: "Checklist for heartbeat turns."},
	{Path: "memories", Directory: true, Access: AccessEdit, Extensions: []string{".md"}},
	{Path: "skills", Directory: true, Access: AccessEdit, Extensions: []string{".md"}},
	{Path: "cron", Directory: true, Access: AccessEdit, Extensions: []string{".yaml", ".yml"}},
	{Path: "logs", Directory: true, Access: AccessRead, Extensions: []string{".log", ".1"}},
	{Path: ".env", Access: AccessSecret, Note: "Secrets. Edit on the host, never over HTTP."},
	{Path: "auth.json", Access: AccessSecret, Note: "OAuth credentials, written by Eggy."},
}

// HomeFiles is the files API's view of one home directory.
type HomeFiles struct{ layout home.Layout }

func NewHomeFiles(layout home.Layout) *HomeFiles { return &HomeFiles{layout: layout} }

// List returns every reachable entry, directories expanded, ordered by path.
func (h *HomeFiles) List() ([]HomeFile, error) {
	files := make([]HomeFile, 0, len(homeAreas))
	for _, area := range homeAreas {
		if !area.Directory {
			files = append(files, h.describe(area.Path, area))
			continue
		}
		entries, err := os.ReadDir(filepath.Join(h.layout.Root, area.Path))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", area.Path, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !matchesExtension(entry.Name(), area.Extensions) {
				continue
			}
			files = append(files, h.describe(area.Path+"/"+entry.Name(), area))
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// Read returns one file's raw text. A secret entry is refused outright, and
// a missing editable file reads as empty so the UI can offer to create it.
func (h *HomeFiles) Read(relative string) (string, FileAccess, error) {
	area, err := h.resolve(relative)
	if err != nil {
		return "", "", err
	}
	if area.Access == AccessSecret {
		return "", area.Access, ErrFileForbidden
	}
	body, err := os.ReadFile(filepath.Join(h.layout.Root, relative))
	if errors.Is(err, fs.ErrNotExist) {
		if area.Access == AccessRead {
			return "", area.Access, ErrFileNotFound
		}
		return "", area.Access, nil
	}
	if err != nil {
		return "", area.Access, err
	}
	if int64(len(body)) > maxEditableBytes {
		// Logs are the realistic case: show the tail, which is the part
		// anyone opening a log actually wants.
		if area.Access == AccessRead {
			return string(body[int64(len(body))-maxEditableBytes:]), area.Access, nil
		}
		return "", area.Access, fmt.Errorf("%s is larger than %d bytes", relative, maxEditableBytes)
	}
	return string(body), area.Access, nil
}

// Write replaces one editable file. YAML is parsed before it lands, and
// config.yaml is validated in full, so the UI cannot leave behind a home
// that Eggy will refuse to boot from.
func (h *HomeFiles) Write(relative, content string) error {
	area, err := h.resolve(relative)
	if err != nil {
		return err
	}
	switch area.Access {
	case AccessSecret:
		return ErrFileForbidden
	case AccessRead:
		return ErrFileReadOnly
	}
	if int64(len(content)) > maxEditableBytes {
		return fmt.Errorf("content is larger than %d bytes", maxEditableBytes)
	}
	if err := validateHomeFile(relative, content); err != nil {
		return err
	}
	target := filepath.Join(h.layout.Root, relative)
	return filelock.With(target, func() error {
		return atomicfile.Write(target, []byte(content), 0o600)
	})
}

// validateHomeFile refuses content that would break the file's own contract.
// It deliberately stops at parseability plus, for config.yaml, Eggy's own
// Validate: an owner editing raw YAML is entitled to write settings this
// build does not use yet.
func validateHomeFile(relative, content string) error {
	switch {
	case relative == "config.yaml":
		if err := config.ValidateDocument([]byte(content)); err != nil {
			return fmt.Errorf("config.yaml is not valid: %w", err)
		}
		return nil
	case strings.HasSuffix(relative, ".yaml"), strings.HasSuffix(relative, ".yml"):
		var any any
		if err := yaml.Unmarshal([]byte(content), &any); err != nil {
			return fmt.Errorf("%s is not valid YAML: %w", relative, err)
		}
		return nil
	default:
		return nil
	}
}

// resolve maps a request path onto its area, rejecting anything outside the
// allowlist. Traversal is impossible by construction: the cleaned path must
// equal the requested one and match an area exactly.
func (h *HomeFiles) resolve(relative string) (homeArea, error) {
	if relative == "" || relative != path.Clean(relative) || strings.HasPrefix(relative, "/") || strings.Contains(relative, "..") {
		return homeArea{}, ErrFileForbidden
	}
	for _, area := range homeAreas {
		if !area.Directory {
			if relative == area.Path {
				return area, nil
			}
			continue
		}
		directory, name := path.Split(relative)
		if strings.TrimSuffix(directory, "/") != area.Path || name == "" {
			continue
		}
		if !matchesExtension(name, area.Extensions) {
			return homeArea{}, ErrFileForbidden
		}
		return area, nil
	}
	return homeArea{}, ErrFileForbidden
}

func (h *HomeFiles) describe(relative string, area homeArea) HomeFile {
	file := HomeFile{Path: relative, Access: area.Access, Note: area.Note, Language: languageOf(relative)}
	info, err := os.Stat(filepath.Join(h.layout.Root, relative))
	if err != nil {
		file.Missing = true
		return file
	}
	file.Size = info.Size()
	file.Modified = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	return file
}

// matchesExtension reports whether a directory entry belongs to its area.
// An area with no extensions listed accepts every file.
func matchesExtension(name string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	lower := strings.ToLower(name)
	for _, extension := range extensions {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

func languageOf(relative string) string {
	switch strings.ToLower(filepath.Ext(relative)) {
	case ".yaml", ".yml":
		return "yaml"
	case ".md":
		return "markdown"
	case ".json":
		return "json"
	default:
		return "text"
	}
}
