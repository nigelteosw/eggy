package bootstrap

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/services"
	"github.com/nigelteosw/eggy/internal/extensions/marketplace"
	"github.com/nigelteosw/eggy/internal/panel"
	"github.com/nigelteosw/eggy/internal/ports"
)

// newMarketplace builds the marketplace extension, or nothing at all. Disabled,
// it returns nil: no route, no outbound request, no store access, and no prompt
// bytes, the same rule newFinance holds.
func newMarketplace(cfg config.Config, configPath string, store ports.MarketplaceStore, skills *services.SkillsService, now func() time.Time) (*marketplace.Service, error) {
	if !cfg.Marketplace.Enabled {
		return nil, nil
	}
	return marketplace.New(marketplace.Options{
		Sources: func() ([]marketplace.Source, error) {
			configured, err := config.GetMarketplaceSources(configPath)
			if err != nil {
				return nil, err
			}
			sources := make([]marketplace.Source, 0, len(configured))
			for _, s := range configured {
				sources = append(sources, marketplace.Source{Name: s.Name, Kind: s.Kind, Repo: s.Repo, Path: s.Path, Ref: s.Ref})
			}
			return sources, nil
		},
		Skills: skills,
		MCP:    marketplaceMCP{configPath: configPath},
		Store:  store,
		Fetch:  marketplace.NewFetcher(),
		Now:    now,
	})
}

// marketplaceMCP writes plugin MCP entries through the config authority, so the
// Connectors page and the marketplace see one set of servers.
type marketplaceMCP struct {
	configPath string
}

func (m marketplaceMCP) Add(name, url string) error {
	return config.AddMCPServerDisabled(m.configPath, name, url)
}

func (m marketplaceMCP) Get(name string) (string, bool, bool, error) {
	servers, err := config.GetMCPServersConfig(m.configPath)
	if err != nil {
		return "", false, false, err
	}
	server, ok := servers[name]
	return server.URL, server.Enabled, ok, nil
}

func (m marketplaceMCP) SetEnabled(name string, enabled bool) error {
	return config.SetMCPServerEnabled(m.configPath, name, enabled)
}

func (m marketplaceMCP) Remove(name string) error {
	return config.RemoveMCPServer(m.configPath, name)
}

// panelMarketplace adapts the marketplace service to the panel's own types and
// error vocabulary. The panel never imports the extension.
type panelMarketplace struct {
	svc *marketplace.Service
}

func (p panelMarketplace) Sources(ctx context.Context) ([]panel.MarketplaceSourceStatus, error) {
	statuses, err := p.svc.Sources(ctx)
	if err != nil {
		return nil, translateMarketplaceError(err)
	}
	out := make([]panel.MarketplaceSourceStatus, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, panel.MarketplaceSourceStatus{
			Name: s.Source.Name, Kind: s.Source.Kind, Repo: s.Source.Repo, Path: s.Source.Path, Ref: s.Source.Ref,
			SHA: s.SHA, Error: s.Error,
		})
	}
	return out, nil
}

func (p panelMarketplace) Items(ctx context.Context, source string) ([]panel.MarketplaceItem, error) {
	items, err := p.svc.Items(ctx, source)
	if err != nil {
		return nil, translateMarketplaceError(err)
	}
	out := make([]panel.MarketplaceItem, 0, len(items))
	for _, item := range items {
		out = append(out, panel.MarketplaceItem{Name: item.Name, Description: item.Description, Kind: item.Kind, Unsupported: item.Unsupported})
	}
	return out, nil
}

func (p panelMarketplace) Plan(ctx context.Context, req panel.MarketplacePlanRequest) (panel.MarketplacePlan, error) {
	plan, err := p.svc.Plan(ctx, toPlanRequest(req))
	if err != nil {
		return panel.MarketplacePlan{}, translateMarketplaceError(err)
	}
	return planToPanel(plan), nil
}

func (p panelMarketplace) Install(ctx context.Context, req panel.MarketplaceApproval) (panel.MarketplaceInstall, error) {
	install, err := p.svc.Install(ctx, toApproveRequest(req))
	if err != nil {
		return panel.MarketplaceInstall{}, translateMarketplaceError(err)
	}
	return installToPanel(install), nil
}

func (p panelMarketplace) Installs(ctx context.Context) ([]panel.MarketplaceInstallStatus, error) {
	statuses, err := p.svc.Installs(ctx)
	if err != nil {
		return nil, translateMarketplaceError(err)
	}
	out := make([]panel.MarketplaceInstallStatus, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, statusToPanel(s))
	}
	return out, nil
}

func (p panelMarketplace) Check(ctx context.Context, id string) (panel.MarketplaceInstallStatus, panel.MarketplacePlan, error) {
	status, plan, err := p.svc.Check(ctx, id)
	if err != nil {
		return panel.MarketplaceInstallStatus{}, panel.MarketplacePlan{}, translateMarketplaceError(err)
	}
	return statusToPanel(status), planToPanel(plan), nil
}

func (p panelMarketplace) Update(ctx context.Context, id string, req panel.MarketplaceApproval, replaceEdited []string) (panel.MarketplaceInstall, error) {
	install, err := p.svc.Update(ctx, id, toApproveRequest(req), replaceEdited)
	if err != nil {
		return panel.MarketplaceInstall{}, translateMarketplaceError(err)
	}
	return installToPanel(install), nil
}

func (p panelMarketplace) SetDisabled(ctx context.Context, id string, disabled bool) error {
	return translateMarketplaceError(p.svc.SetDisabled(ctx, id, disabled))
}

func (p panelMarketplace) Remove(ctx context.Context, id string, deleteEdited []string) error {
	return translateMarketplaceError(p.svc.Remove(ctx, id, deleteEdited))
}

// translateMarketplaceError maps the extension's refusals onto the panel's
// vocabulary, so each one gets the right status. Errors the panel already
// recognises (the ports' not-found and already-installed) pass through.
func translateMarketplaceError(err error) error {
	if err == nil {
		return nil
	}
	var conflict marketplace.ConflictError
	var rateLimit marketplace.RateLimitError
	switch {
	case errors.Is(err, marketplace.ErrDigestMismatch):
		return panel.ErrMarketplaceDigest
	case errors.As(err, &conflict):
		return panel.MarketplaceConflictError{Conflicts: conflict.Conflicts}
	case errors.As(err, &rateLimit):
		return panel.MarketplaceRateLimitError{Reset: rateLimit.Reset}
	case errors.Is(err, marketplace.ErrArchiveRejected):
		return panel.ErrMarketplaceArchive
	}
	return err
}

func toPlanRequest(req panel.MarketplacePlanRequest) marketplace.PlanRequest {
	return marketplace.PlanRequest{Source: req.Source, Item: req.Item, Renames: req.Renames, InstallID: req.InstallID}
}

func toApproveRequest(req panel.MarketplaceApproval) marketplace.ApproveRequest {
	return marketplace.ApproveRequest{PlanRequest: toPlanRequest(req.MarketplacePlanRequest), SHA: req.SHA, Digest: req.Digest}
}

func planToPanel(plan marketplace.Plan) panel.MarketplacePlan {
	out := panel.MarketplacePlan{
		Source: plan.Source, Repo: plan.Repo, SHA: plan.SHA, Item: plan.Item, Digest: plan.Digest,
		Conflicts: append([]string{}, plan.Conflicts...), IndexBytes: plan.IndexBytes,
		Skills: []panel.MarketplacePlanSkill{}, MCP: []panel.MarketplacePlanMCP{}, Skipped: []panel.MarketplacePlanSkipped{},
	}
	for _, skill := range plan.Skills {
		files := make([]string, 0, len(skill.Files))
		for rel := range skill.Files {
			files = append(files, rel)
		}
		sort.Strings(files)
		out.Skills = append(out.Skills, panel.MarketplacePlanSkill{
			Name: skill.Name, UpstreamName: skill.UpstreamName, Description: skill.Description,
			Files: files, Warnings: skill.Warnings, Body: skill.Body, Contents: skill.Files,
		})
	}
	for _, server := range plan.MCP {
		out.MCP = append(out.MCP, panel.MarketplacePlanMCP{Name: server.Name, URL: server.URL})
	}
	for _, s := range plan.Skipped {
		out.Skipped = append(out.Skipped, panel.MarketplacePlanSkipped{Path: s.Path, Reason: s.Reason})
	}
	return out
}

func installToPanel(install ports.MarketplaceInstall) panel.MarketplaceInstall {
	files := make([]panel.MarketplaceFileRef, 0, len(install.Files))
	for _, f := range install.Files {
		files = append(files, panel.MarketplaceFileRef{Kind: f.Kind, Target: f.Target})
	}
	return panel.MarketplaceInstall{
		ID: install.ID, Source: install.SourceName, Repo: install.Repo, Item: install.Item,
		CommitSHA: install.CommitSHA, InstalledBy: install.InstalledBy,
		InstalledAt: install.InstalledAt, UpdatedAt: install.UpdatedAt,
		Disabled: install.Disabled, Files: files,
	}
}

func statusToPanel(status marketplace.InstallStatus) panel.MarketplaceInstallStatus {
	edited := append([]string{}, status.Edited...)
	return panel.MarketplaceInstallStatus{Install: installToPanel(status.Install), Status: status.Status, Edited: edited}
}
