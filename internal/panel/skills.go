// The panel's skill administration. Skills are owner-authored Markdown kept in
// the skills directory; these routes are a thin view over the skills service,
// which owns name validation, size limits, and the disabled flag. The panel
// declares the interface it needs rather than importing the service.
package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nigelteosw/eggy/internal/ports"
)

// skillRequestMaxBytes caps a skill write body. It is larger than a skill
// file's own 32 KiB limit because JSON escapes characters, so a valid file can
// need more bytes than it occupies on disk.
const skillRequestMaxBytes = 64 << 10

// SkillsAdmin is the owner's view of the skills directory.
type SkillsAdmin interface {
	List(context.Context) ([]ports.SkillSummary, error)
	Inspect(context.Context, string) (ports.Skill, error)
	Write(context.Context, string, string, string) error
	SetDisabled(context.Context, string, bool) error
	Delete(context.Context, string) error
}

type skillWrite struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Disabled    bool   `json:"disabled"`
}

func skillsListRoute(admin SkillsAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := admin.List(r.Context())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rows == nil {
			rows = []ports.SkillSummary{}
		}
		writeJSON(w, rows)
	}
}

func skillsGetRoute(admin SkillsAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		skill, err := admin.Inspect(r.Context(), r.PathValue("name"))
		if err != nil {
			writeSkillError(w, err, http.StatusBadRequest)
			return
		}
		writeJSON(w, skill)
	}
}

func skillsCreateRoute(admin SkillsAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		input, ok := decodeSkillWrite(w, r)
		if !ok {
			return
		}
		if _, err := admin.Inspect(r.Context(), input.Name); err == nil {
			writeWebError(w, http.StatusConflict, "a skill named "+input.Name+" already exists")
			return
		} else if !errors.Is(err, ports.ErrSkillNotFound) {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := admin.Write(r.Context(), input.Name, input.Description, input.Body); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if input.Disabled {
			if err := admin.SetDisabled(r.Context(), input.Name, true); err != nil {
				writeWebError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Created skill " + input.Name + "."})
	}
}

// skillsUpdateRoute replaces a skill's text and its enabled choice. The name
// comes from the path and is never changed here: renaming is a create plus a
// delete, so an owner cannot silently orphan references to the old name.
func skillsUpdateRoute(admin SkillsAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		current, err := admin.Inspect(r.Context(), name)
		if err != nil {
			writeSkillError(w, err, http.StatusBadRequest)
			return
		}
		input, ok := decodeSkillWrite(w, r)
		if !ok {
			return
		}
		if err := admin.Write(r.Context(), name, input.Description, input.Body); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if input.Disabled != current.Disabled {
			if err := admin.SetDisabled(r.Context(), name, input.Disabled); err != nil {
				writeWebError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Saved skill " + name + "."})
	}
}

func skillsDeleteRoute(admin SkillsAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := admin.Delete(r.Context(), r.PathValue("name")); err != nil {
			writeSkillError(w, err, http.StatusBadRequest)
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Deleted skill " + r.PathValue("name") + "."})
	}
}

func decodeSkillWrite(w http.ResponseWriter, r *http.Request) (skillWrite, bool) {
	var input skillWrite
	body := http.MaxBytesReader(w, r.Body, skillRequestMaxBytes)
	if err := json.NewDecoder(body).Decode(&input); err != nil {
		writeWebError(w, http.StatusBadRequest, "invalid request body")
		return skillWrite{}, false
	}
	return input, true
}

func writeSkillError(w http.ResponseWriter, err error, otherwise int) {
	if errors.Is(err, ports.ErrSkillNotFound) {
		writeWebError(w, http.StatusNotFound, err.Error())
		return
	}
	writeWebError(w, otherwise, err.Error())
}
