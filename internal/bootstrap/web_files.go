package bootstrap

import (
	"encoding/json"
	"errors"
	"net/http"
)

// The files API exposes the owner-facing part of the home directory
// (internal/home) as raw text: list what is there, read one file, write it
// back. It is the web counterpart of editing the home on the host, so the
// same allowlist and validation in home_files.go govern both.
//
// Every route sits behind requireWebSession, like the rest of /api.

type homeFileListResponse struct {
	Root  string     `json:"root"`
	Files []HomeFile `json:"files"`
}

type homeFileResponse struct {
	Path    string     `json:"path"`
	Access  FileAccess `json:"access"`
	Content string     `json:"content"`
}

func webFilesListRoute(files *HomeFiles) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if files == nil {
			writeWebError(w, http.StatusServiceUnavailable, "home directory is unavailable")
			return
		}
		listing, err := files.List()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, http.StatusOK, homeFileListResponse{Root: files.layout.Root, Files: listing})
	}
}

func webFileReadRoute(files *HomeFiles) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if files == nil {
			writeWebError(w, http.StatusServiceUnavailable, "home directory is unavailable")
			return
		}
		relative := r.PathValue("path")
		content, access, err := files.Read(relative)
		if err != nil {
			writeWebError(w, fileErrorStatus(err), err.Error())
			return
		}
		writeWebJSON(w, http.StatusOK, homeFileResponse{Path: relative, Access: access, Content: content})
	}
}

func webFileWriteRoute(files *HomeFiles) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if files == nil {
			writeWebError(w, http.StatusServiceUnavailable, "home directory is unavailable")
			return
		}
		var input struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		relative := r.PathValue("path")
		if err := files.Write(relative, input.Content); err != nil {
			writeWebError(w, fileErrorStatus(err), err.Error())
			return
		}
		result := CommandResult{State: ResultSuccess, Title: "Saved " + relative + "."}
		// config.yaml is read once at boot, so saving it changes nothing
		// until Eggy restarts. Say so rather than letting the owner believe
		// a new provider is already live.
		if relative == "config.yaml" {
			result.Detail = "Restart Eggy for this to take effect."
		}
		writeWebResult(w, result)
	}
}

// fileErrorStatus maps a files-API failure onto its HTTP status. A path
// outside the allowlist is 404 rather than 403: the API should not confirm
// which unexposed paths exist in the home.
func fileErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrFileForbidden), errors.Is(err, ErrFileNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrFileReadOnly):
		return http.StatusForbidden
	default:
		return http.StatusBadRequest
	}
}

func writeWebJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, "failed to render response")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
