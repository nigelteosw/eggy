package panel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// marketplaceRequestMaxBytes caps a marketplace request body. A reviewed plan
// is the largest thing a request carries, and it is never near this bound.
const marketplaceRequestMaxBytes = 64 << 10

// ErrMarketplaceDigest means the content changed since the owner reviewed it.
var ErrMarketplaceDigest = errors.New("the content changed since you reviewed it; review again")

// ErrMarketplaceArchive means a download broke an archive rule and was refused.
var ErrMarketplaceArchive = errors.New("the download was refused")

// MarketplaceConflictError lists the names that would overwrite something the
// install does not own, so the review can ask for a rename.
type MarketplaceConflictError struct {
	Conflicts []string
}

func (e MarketplaceConflictError) Error() string {
	return "rename the conflicting skills before installing"
}

// MarketplaceRateLimitError reports that GitHub is rate-limiting this deployment.
type MarketplaceRateLimitError struct {
	Reset time.Time
}

func (e MarketplaceRateLimitError) Error() string {
	return "GitHub rate limit reached"
}

// MarketplaceService is the marketplace extension as the panel sees it. The
// panel declares these types itself and never imports the extension; bootstrap
// converts between them.
type MarketplaceService interface {
	Sources(ctx context.Context) ([]MarketplaceSourceStatus, error)
	Items(ctx context.Context, source string) ([]MarketplaceItem, error)
	Plan(ctx context.Context, req MarketplacePlanRequest) (MarketplacePlan, error)
	Install(ctx context.Context, req MarketplaceApproval) (MarketplaceInstall, error)
	Installs(ctx context.Context) ([]MarketplaceInstallStatus, error)
	Check(ctx context.Context, id string) (MarketplaceInstallStatus, MarketplacePlan, error)
	Update(ctx context.Context, id string, req MarketplaceApproval, replaceEdited []string) (MarketplaceInstall, error)
	SetDisabled(ctx context.Context, id string, disabled bool) error
	Remove(ctx context.Context, id string, deleteEdited []string) error
}

type MarketplaceSourceStatus struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Repo  string `json:"repo"`
	Path  string `json:"path,omitempty"`
	Ref   string `json:"ref,omitempty"`
	SHA   string `json:"sha,omitempty"`
	Error string `json:"error,omitempty"`
}

type MarketplaceItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Unsupported string `json:"unsupported,omitempty"`
}

type MarketplacePlanRequest struct {
	Source    string            `json:"source"`
	Item      string            `json:"item"`
	Renames   map[string]string `json:"renames,omitempty"`
	InstallID string            `json:"install_id,omitempty"`
}

// MarketplaceApproval is a plan the owner reviewed, with the commit and digest
// it was shown. The install proceeds only if the rebuilt plan matches.
type MarketplaceApproval struct {
	MarketplacePlanRequest
	SHA    string `json:"sha"`
	Digest string `json:"digest"`
}

type MarketplacePlanSkill struct {
	Name         string            `json:"name"`
	UpstreamName string            `json:"upstream_name"`
	Description  string            `json:"description"`
	Body         string            `json:"body"`
	Contents     map[string]string `json:"contents,omitempty"`
	Files        []string          `json:"files"`
	Warnings     []string          `json:"warnings,omitempty"`
}

type MarketplacePlanMCP struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type MarketplacePlanSkipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type MarketplacePlan struct {
	CatalogSHA string                   `json:"catalog_sha"`
	Source     string                   `json:"source"`
	Repo       string                   `json:"repo"`
	SHA        string                   `json:"sha"`
	Item       string                   `json:"item"`
	Digest     string                   `json:"digest"`
	Skills     []MarketplacePlanSkill   `json:"skills"`
	MCP        []MarketplacePlanMCP     `json:"mcp"`
	Skipped    []MarketplacePlanSkipped `json:"skipped"`
	Conflicts  []string                 `json:"conflicts"`
	IndexBytes int                      `json:"index_bytes"`
}

type MarketplaceFileRef struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

type MarketplaceInstall struct {
	ID          string               `json:"id"`
	Source      string               `json:"source"`
	Repo        string               `json:"repo"`
	Item        string               `json:"item"`
	CommitSHA   string               `json:"commit_sha"`
	InstalledBy string               `json:"installed_by"`
	InstalledAt time.Time            `json:"installed_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Disabled    bool                 `json:"disabled"`
	Files       []MarketplaceFileRef `json:"files"`
}

type MarketplaceInstallStatus struct {
	Install MarketplaceInstall `json:"install"`
	Status  string             `json:"status"`
	Edited  []string           `json:"edited"`
}

// marketplaceSourceJSON is one configured source, as the settings card edits it.
type marketplaceSourceJSON struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Repo string `json:"repo"`
	Path string `json:"path,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

func registerMarketplaceRoutes(mux *http.ServeMux, guard func(http.HandlerFunc) http.Handler, svc MarketplaceService, configPath string) {
	mux.Handle("GET /api/config/marketplace", guard(marketplaceSourcesGetRoute(configPath)))
	mux.Handle("PUT /api/config/marketplace", guard(marketplaceSourcesSetRoute(configPath)))
	mux.Handle("GET /api/marketplace/sources", guard(marketplaceSourcesRoute(svc)))
	mux.Handle("GET /api/marketplace/items", guard(marketplaceItemsRoute(svc)))
	mux.Handle("POST /api/marketplace/plan", guard(marketplacePlanRoute(svc)))
	mux.Handle("POST /api/marketplace/installs", guard(marketplaceInstallRoute(svc)))
	mux.Handle("GET /api/marketplace/installs", guard(marketplaceInstallsRoute(svc)))
	mux.Handle("POST /api/marketplace/installs/{id}/check", guard(marketplaceCheckRoute(svc)))
	mux.Handle("PUT /api/marketplace/installs/{id}", guard(marketplaceUpdateRoute(svc)))
	mux.Handle("POST /api/marketplace/installs/{id}/disabled", guard(marketplaceDisabledRoute(svc)))
	mux.Handle("DELETE /api/marketplace/installs/{id}", guard(marketplaceRemoveRoute(svc)))
}

func marketplaceSourcesGetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sources, err := config.GetMarketplaceSources(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not read the marketplace settings")
			return
		}
		out := make([]marketplaceSourceJSON, 0, len(sources))
		for _, s := range sources {
			out = append(out, marketplaceSourceJSON{Name: s.Name, Kind: s.Kind, Repo: s.Repo, Path: s.Path, Ref: s.Ref})
		}
		writeJSON(w, map[string]any{"sources": out})
	}
}

func marketplaceSourcesSetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Sources []marketplaceSourceJSON `json:"sources"`
		}
		if err := decodeMarketplaceBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		cfg, err := config.LoadDocument(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not read the marketplace settings")
			return
		}
		sources := make([]config.MarketplaceSource, 0, len(body.Sources))
		for _, s := range body.Sources {
			sources = append(sources, config.MarketplaceSource{Name: s.Name, Kind: s.Kind, Repo: s.Repo, Path: s.Path, Ref: s.Ref})
		}
		if err := config.SetMarketplace(configPath, cfg.Marketplace.Enabled, sources); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"saved": true})
	}
}

func marketplaceSourcesRoute(svc MarketplaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := svc.Sources(r.Context())
		if err != nil {
			writeMarketplaceFailure(w, err)
			return
		}
		writeJSON(w, out)
	}
}

func marketplaceItemsRoute(svc MarketplaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := svc.Items(r.Context(), r.URL.Query().Get("source"))
		if err != nil {
			writeMarketplaceFailure(w, err)
			return
		}
		writeJSON(w, out)
	}
}

func marketplacePlanRoute(svc MarketplaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body MarketplacePlanRequest
		if err := decodeMarketplaceBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		plan, err := svc.Plan(r.Context(), body)
		if err != nil {
			writeMarketplaceFailure(w, err)
			return
		}
		writeJSON(w, plan)
	}
}

func marketplaceInstallRoute(svc MarketplaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body MarketplaceApproval
		if err := decodeMarketplaceBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		install, err := svc.Install(r.Context(), body)
		if err != nil {
			writeMarketplaceFailure(w, err)
			return
		}
		writeJSON(w, install)
	}
}

func marketplaceInstallsRoute(svc MarketplaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := svc.Installs(r.Context())
		if err != nil {
			writeMarketplaceFailure(w, err)
			return
		}
		writeJSON(w, out)
	}
}

func marketplaceCheckRoute(svc MarketplaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, plan, err := svc.Check(r.Context(), r.PathValue("id"))
		if err != nil {
			writeMarketplaceFailure(w, err)
			return
		}
		writeJSON(w, map[string]any{"status": status, "plan": plan})
	}
}

func marketplaceUpdateRoute(svc MarketplaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MarketplaceApproval
			ReplaceEdited []string `json:"replace_edited,omitempty"`
		}
		if err := decodeMarketplaceBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		install, err := svc.Update(r.Context(), r.PathValue("id"), body.MarketplaceApproval, body.ReplaceEdited)
		if err != nil {
			writeMarketplaceFailure(w, err)
			return
		}
		writeJSON(w, install)
	}
}

func marketplaceDisabledRoute(svc MarketplaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Disabled bool `json:"disabled"`
		}
		if err := decodeMarketplaceBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := svc.SetDisabled(r.Context(), r.PathValue("id"), body.Disabled); err != nil {
			writeMarketplaceFailure(w, err)
			return
		}
		writeJSON(w, map[string]any{"disabled": body.Disabled})
	}
}

func marketplaceRemoveRoute(svc MarketplaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DeleteEdited []string `json:"delete_edited,omitempty"`
		}
		// A DELETE with no body is valid: it removes the install and keeps every edit.
		if err := decodeMarketplaceBody(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		id := r.PathValue("id")
		if err := svc.Remove(r.Context(), id, body.DeleteEdited); err != nil {
			writeMarketplaceFailure(w, err)
			return
		}
		writeJSON(w, map[string]any{"removed": id})
	}
}

func decodeMarketplaceBody(w http.ResponseWriter, r *http.Request, into any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, marketplaceRequestMaxBytes)).Decode(into)
}

// writeMarketplaceFailure says whose fault a refusal was: a stale review or an
// occupied name is a conflict, a missing install is not found, and a rate limit
// is the service's, not the owner's.
func writeMarketplaceFailure(w http.ResponseWriter, err error) {
	var conflicts MarketplaceConflictError
	var rateLimit MarketplaceRateLimitError
	switch {
	case errors.Is(err, ErrMarketplaceDigest):
		writeWebError(w, http.StatusConflict, ErrMarketplaceDigest.Error())
	case errors.As(err, &conflicts):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": conflicts.Error(), "conflicts": conflicts.Conflicts})
	case errors.Is(err, ports.ErrMarketplaceInstallExists):
		writeWebError(w, http.StatusConflict, "already installed")
	case errors.Is(err, ports.ErrMarketplaceInstallNotFound):
		writeWebError(w, http.StatusNotFound, "install not found")
	case errors.As(err, &rateLimit):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": rateLimit.Error(), "reset": rateLimit.Reset})
	case errors.Is(err, ErrMarketplaceArchive):
		writeWebError(w, http.StatusUnprocessableEntity, ErrMarketplaceArchive.Error())
	default:
		writeWebError(w, http.StatusInternalServerError, "could not complete the marketplace request")
	}
}
