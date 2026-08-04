package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

type File struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type,omitempty"`
	Modified string `json:"modified,omitempty"`
	Link     string `json:"link,omitempty"`
}

// fileFields is what every Drive answer carries. Requested explicitly because
// Drive's default projection is id, name and mimeType only, and a file with no
// link is one the owner cannot be pointed at.
const fileFields = "id,name,mimeType,modifiedTime,webViewLink"

// folderType is the mime type that makes a file a folder. Drive has no
// separate folder endpoint; a folder is a file that cannot hold content.
const folderType = "application/vnd.google-apps.folder"

// DriveSearch takes plain words by default and escapes them into a fullText
// query. raw passes the caller's string through as a Drive query expression --
// mimeType filters and the like -- which is a sharper tool and a much easier
// one to get a syntax error from.
func (w *Workspace) DriveSearch(ctx context.Context, query string, max int, raw bool) ([]File, error) {
	if max <= 0 || max > 50 {
		max = 10
	}
	expression := query
	if !raw {
		expression = fmt.Sprintf("fullText contains '%s'", strings.ReplaceAll(query, "'", `\'`))
	}
	values := url.Values{
		"q": {expression}, "pageSize": {fmt.Sprint(max)},
		"fields": {"files(" + fileFields + ")"},
	}
	var response struct {
		Files []driveFile `json:"files"`
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Drive+"/files", values, nil, &response); err != nil {
		return nil, err
	}
	files := make([]File, 0, len(response.Files))
	for _, file := range response.Files {
		files = append(files, file.file())
	}
	return files, nil
}

type driveFile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MimeType     string `json:"mimeType"`
	ModifiedTime string `json:"modifiedTime"`
	WebViewLink  string `json:"webViewLink"`
}

func (f driveFile) file() File {
	return File{ID: f.ID, Name: f.Name, MimeType: f.MimeType, Modified: f.ModifiedTime, Link: f.WebViewLink}
}

// exportTypes maps each Google-native format to the export a model can read.
//
// Hermes exports a Doc to PDF, which is right for an agent downloading to
// disk and useless here: this content goes into a model's context, where a PDF
// is bytes it cannot read. Plain text and CSV are the formats that survive the
// trip. A native format missing from this map has no text form worth asking
// for -- a Drawing is a picture.
var exportTypes = map[string]string{
	"application/vnd.google-apps.document":     "text/plain",
	"application/vnd.google-apps.spreadsheet":  "text/csv",
	"application/vnd.google-apps.presentation": "text/plain",
	"application/vnd.google-apps.script":       "application/vnd.google-apps.script+json",
}

// Content is a file read back as text, with what it took to get there.
type Content struct {
	File     File   `json:"file"`
	Text     string `json:"text,omitempty"`
	Exported string `json:"exported_as,omitempty"`
	Note     string `json:"note,omitempty"`
}

// DriveGet reads a file's content, which is the half of Drive that search
// alone cannot reach: a search returns ids, and without this nothing can open
// one.
//
// Google-native files must be exported rather than downloaded -- there are no
// bytes to fetch -- and everything else is downloaded as-is and returned only
// when it is text. The metadata lookup that decides which is not optional:
// exporting a PDF and downloading a Doc both fail, in different ways.
func (w *Workspace) DriveGet(ctx context.Context, id string) (Content, error) {
	if strings.TrimSpace(id) == "" {
		return Content{}, errors.New("a file id is required")
	}
	var metadata driveFile
	values := url.Values{"fields": {fileFields}}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Drive+"/files/"+url.PathEscape(id), values, nil, &metadata); err != nil {
		return Content{}, err
	}
	content := Content{File: metadata.file()}
	endpoint := w.endpoints.Drive + "/files/" + url.PathEscape(id)
	var query url.Values
	switch {
	case metadata.MimeType == folderType:
		content.Note = "that is a folder; list what is in it with a raw query of \"'" + id + "' in parents\""
		return content, nil
	case strings.HasPrefix(metadata.MimeType, "application/vnd.google-apps."):
		exportType, exportable := exportTypes[metadata.MimeType]
		if !exportable {
			content.Note = displayType(metadata.MimeType) + " has no text form; open the link instead"
			return content, nil
		}
		endpoint += "/export"
		query = url.Values{"mimeType": {exportType}}
		content.Exported = exportType
	case !isTextual(metadata.MimeType):
		content.Note = displayType(metadata.MimeType) + " is not text, so it was not read; open the link instead"
		return content, nil
	default:
		query = url.Values{"alt": {"media"}}
	}
	raw, _, err := w.callRaw(ctx, endpoint, query)
	if err != nil {
		return Content{}, err
	}
	content.Text = string(raw)
	return content, nil
}

// NewFile creates a file from text. Binary uploads are deliberately absent:
// content arrives here as a string in a tool call, so there is nothing binary
// to carry, and a base64 parameter would only invite a model to invent one.
type NewFile struct {
	Name     string
	MimeType string
	Content  string
	Parent   string
	Folder   bool
}

// DriveCreate makes a folder or writes a text file.
func (w *Workspace) DriveCreate(ctx context.Context, file NewFile) (File, error) {
	if strings.TrimSpace(file.Name) == "" {
		return File{}, errors.New("a name is required")
	}
	metadata := map[string]any{"name": file.Name}
	if strings.TrimSpace(file.Parent) != "" {
		metadata["parents"] = []string{file.Parent}
	}
	var created driveFile
	if file.Folder {
		// A folder has no content, so it is an ordinary metadata create rather
		// than an upload with an empty second part.
		metadata["mimeType"] = folderType
		values := url.Values{"fields": {fileFields}}
		if err := w.call(ctx, http.MethodPost, w.endpoints.Drive+"/files", values, metadata, &created); err != nil {
			return File{}, err
		}
		return created.file(), nil
	}
	mimeType := strings.TrimSpace(file.MimeType)
	if mimeType == "" {
		mimeType = "text/plain"
	}
	if err := w.upload(ctx, metadata, mimeType, []byte(file.Content), &created); err != nil {
		return File{}, err
	}
	return created.file(), nil
}

// FileChange renames a file, moves it, or both. Moving is expressed as a
// parent swap because Drive has no move: a file's location is its parent list,
// and adding without removing leaves it in two places at once.
type FileChange struct {
	ID     string
	Name   string
	Parent string
}

func (w *Workspace) DriveUpdate(ctx context.Context, change FileChange) (File, error) {
	if strings.TrimSpace(change.ID) == "" {
		return File{}, errors.New("a file id is required")
	}
	if strings.TrimSpace(change.Name) == "" && strings.TrimSpace(change.Parent) == "" {
		return File{}, errors.New("nothing to change: give a name, a parent, or both")
	}
	values := url.Values{"fields": {fileFields}}
	request := map[string]any{}
	if name := strings.TrimSpace(change.Name); name != "" {
		request["name"] = name
	}
	if parent := strings.TrimSpace(change.Parent); parent != "" {
		// The old parents have to be named to be removed, and only the file
		// itself knows them.
		var current struct {
			Parents []string `json:"parents"`
		}
		if err := w.call(ctx, http.MethodGet, w.endpoints.Drive+"/files/"+url.PathEscape(change.ID), url.Values{"fields": {"parents"}}, nil, &current); err != nil {
			return File{}, err
		}
		values.Set("addParents", parent)
		if len(current.Parents) > 0 {
			values.Set("removeParents", strings.Join(current.Parents, ","))
		}
	}
	var updated driveFile
	if err := w.call(ctx, http.MethodPatch, w.endpoints.Drive+"/files/"+url.PathEscape(change.ID), values, request, &updated); err != nil {
		return File{}, err
	}
	return updated.file(), nil
}

// DriveDelete trashes by default. Drive keeps a trashed file for 30 days and
// the owner can restore it; a permanent delete cannot be undone by anyone, so
// it has to be asked for explicitly rather than being what "delete" means.
func (w *Workspace) DriveDelete(ctx context.Context, id string, permanent bool) (map[string]any, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("a file id is required")
	}
	endpoint := w.endpoints.Drive + "/files/" + url.PathEscape(id)
	if permanent {
		if err := w.call(ctx, http.MethodDelete, endpoint, nil, nil, nil); err != nil {
			return nil, err
		}
		return map[string]any{"status": "deleted", "file_id": id, "permanent": true}, nil
	}
	if err := w.call(ctx, http.MethodPatch, endpoint, nil, map[string]any{"trashed": true}, nil); err != nil {
		return nil, err
	}
	return map[string]any{"status": "trashed", "file_id": id, "permanent": false, "note": "in the trash for 30 days; restorable from Drive"}, nil
}

// Share grants access. Type decides what the other fields mean, which is why
// they are validated together rather than each on its own.
type Share struct {
	FileID  string
	Email   string
	Domain  string
	Role    string
	Type    string
	Notify  bool
	Message string
}

// DriveShare adds one permission.
//
// type=anyone is a public link, and it is the one setting here that cannot be
// walked back by asking the recipient nicely: anyone who has the URL keeps
// access until the permission is removed. It is allowed, and it is the reason
// this action is gated by default.
func (w *Workspace) DriveShare(ctx context.Context, share Share) (map[string]any, error) {
	if strings.TrimSpace(share.FileID) == "" {
		return nil, errors.New("a file id is required")
	}
	kind := strings.TrimSpace(share.Type)
	if kind == "" {
		kind = "user"
	}
	role := strings.TrimSpace(share.Role)
	if role == "" {
		role = "reader"
	}
	if !slices.Contains([]string{"reader", "commenter", "writer"}, role) {
		return nil, fmt.Errorf("role %q must be reader, commenter or writer", role)
	}
	permission := map[string]any{"type": kind, "role": role}
	switch kind {
	case "user", "group":
		if strings.TrimSpace(share.Email) == "" {
			return nil, fmt.Errorf("an email address is required to share with a %s", kind)
		}
		permission["emailAddress"] = share.Email
	case "domain":
		if strings.TrimSpace(share.Domain) == "" {
			return nil, errors.New("a domain is required to share with a domain")
		}
		permission["domain"] = share.Domain
	case "anyone":
		// Nothing to name: that is the point, and the caller was told.
	default:
		return nil, fmt.Errorf("type %q must be user, group, domain or anyone", kind)
	}
	values := url.Values{"sendNotificationEmail": {fmt.Sprint(share.Notify)}}
	if share.Notify && strings.TrimSpace(share.Message) != "" {
		values.Set("emailMessage", share.Message)
	}
	var created struct {
		ID string `json:"id"`
	}
	endpoint := w.endpoints.Drive + "/files/" + url.PathEscape(share.FileID) + "/permissions"
	if err := w.call(ctx, http.MethodPost, endpoint, values, permission, &created); err != nil {
		return nil, err
	}
	result := map[string]any{"status": "shared", "permission_id": created.ID, "file_id": share.FileID, "role": role, "type": kind}
	if kind == "anyone" {
		result["warning"] = "anyone with the link can now open this file"
	}
	return result, nil
}
