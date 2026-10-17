package marketplace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Skills is the part of the skills service the marketplace writes through. The
// installer never touches the skills directory itself.
type Skills interface {
	List(context.Context) ([]ports.SkillSummary, error)
	Inspect(context.Context, string) (ports.Skill, error)
	ReadFile(context.Context, string, string) (string, error)
	WriteBundle(context.Context, ports.SkillBundle) error
	SetDisabled(context.Context, string, bool) error
	Delete(context.Context, string) error
}

// MCPConfig is the part of the config authority the marketplace writes through.
type MCPConfig interface {
	Add(name, url string) error
	Get(name string) (url string, enabled, exists bool, err error)
	SetEnabled(name string, enabled bool) error
	Remove(name string) error
}

// Fetcher is the GitHub adapter. It is an interface only so the service can be
// tested without a network.
type Fetcher interface {
	resolve(ctx context.Context, repo, ref string) (string, error)
	tarball(ctx context.Context, repo, sha string) (Tree, error)
}

// Options are the service's collaborators. Sources is read on every call, so a
// source edit takes effect without a restart.
type Options struct {
	Sources func() ([]Source, error)
	Skills  Skills
	MCP     MCPConfig
	Store   ports.MarketplaceStore
	Fetch   Fetcher
	Now     func() time.Time
}

// Service is the one write path for marketplace installs. The panel and the
// tests are thin callers of it.
type Service struct {
	opts Options
}

// ErrDigestMismatch means the content at the approved commit no longer matches
// what the owner reviewed. Nothing is written.
var ErrDigestMismatch = errors.New("the content changed since you reviewed it; review again")

// ErrConflicts means a skill or MCP entry would overwrite something the install
// does not own. The owner renames it and reviews again.
var ErrConflicts = errors.New("rename the conflicting skills before installing")

var errItemNotFound = errors.New("item is not in the catalog")

// SourceStatus is one configured source with the commit its ref names now.
type SourceStatus struct {
	Source Source
	SHA    string
	Error  string
}

// PlanRequest names one item and the renames the owner chose. InstallID, when
// set, makes the plan an update of that install.
type PlanRequest struct {
	Source    string
	Item      string
	Renames   map[string]string
	InstallID string
}

// ApproveRequest is a PlanRequest plus the commit and digest the owner reviewed.
// An install or update proceeds only if the rebuilt plan has the same digest.
type ApproveRequest struct {
	PlanRequest
	SHA    string
	Digest string
}

// InstallStatus is one install with what has changed since it was written.
// Status is "unknown" when listed without checking upstream; Check sets it.
type InstallStatus struct {
	Install ports.MarketplaceInstall
	Status  string
	Edited  []string
}

// New checks that every collaborator is present, so a missing one is a boot
// error rather than a nil dereference during a request.
func New(opts Options) (*Service, error) {
	switch {
	case opts.Sources == nil:
		return nil, errors.New("marketplace: Sources is required")
	case opts.Skills == nil:
		return nil, errors.New("marketplace: Skills is required")
	case opts.MCP == nil:
		return nil, errors.New("marketplace: MCP is required")
	case opts.Store == nil:
		return nil, errors.New("marketplace: Store is required")
	case opts.Fetch == nil:
		return nil, errors.New("marketplace: Fetch is required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts}, nil
}

// Sources reports each configured source with the commit its ref names now.
func (s *Service) Sources(ctx context.Context) ([]SourceStatus, error) {
	sources, err := s.opts.Sources()
	if err != nil {
		return nil, err
	}
	out := make([]SourceStatus, 0, len(sources))
	for _, src := range sources {
		status := SourceStatus{Source: src}
		sha, err := s.opts.Fetch.resolve(ctx, src.Repo, src.Ref)
		if err != nil {
			status.Error = err.Error()
		} else {
			status.SHA = sha
		}
		out = append(out, status)
	}
	return out, nil
}

// Items lists what one source offers at its current ref.
func (s *Service) Items(ctx context.Context, source string) ([]Item, error) {
	src, err := s.findSource(source)
	if err != nil {
		return nil, err
	}
	sha, err := s.opts.Fetch.resolve(ctx, src.Repo, src.Ref)
	if err != nil {
		return nil, err
	}
	tree, err := s.opts.Fetch.tarball(ctx, src.Repo, sha)
	if err != nil {
		return nil, err
	}
	return catalogItems(src, tree)
}

// Plan builds the review for an item at its source's current ref. Nothing is
// written.
func (s *Service) Plan(ctx context.Context, req PlanRequest) (Plan, error) {
	replacing, err := s.ownedTargets(ctx, req.InstallID)
	if err != nil {
		return Plan{}, err
	}
	return s.planAt(ctx, req, "", replacing)
}

// Install writes one reviewed item. Duplicate installs are refused before any
// file is touched, and a failure part-way removes what this call wrote.
func (s *Service) Install(ctx context.Context, req ApproveRequest) (ports.MarketplaceInstall, error) {
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	installs, err := s.opts.Store.ListInstalls(ctx)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	for _, row := range installs {
		if row.SourceName == req.Source && row.Item == req.Item {
			return ports.MarketplaceInstall{}, ports.ErrMarketplaceInstallExists
		}
	}

	plan, err := s.planAt(ctx, req.PlanRequest, req.SHA, nil)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	if plan.Digest != req.Digest {
		return ports.MarketplaceInstall{}, ErrDigestMismatch
	}
	if len(plan.Conflicts) > 0 {
		return ports.MarketplaceInstall{}, ErrConflicts
	}

	var writtenSkills, writtenMCP []string
	rollback := func() {
		s.rollbackWrites(ctx, writtenSkills, writtenMCP)
	}
	for _, skill := range plan.Skills {
		err := s.opts.Skills.WriteBundle(ctx, ports.SkillBundle{
			Name: skill.Name, Description: skill.Description, Body: skill.Body,
			Source: req.Source + "/" + req.Item, Extra: skill.Extra, Files: skill.Files,
		})
		if err != nil {
			rollback()
			return ports.MarketplaceInstall{}, err
		}
		writtenSkills = append(writtenSkills, skill.Name)
	}
	for _, server := range plan.MCP {
		if err := s.opts.MCP.Add(server.Name, server.URL); err != nil {
			rollback()
			return ports.MarketplaceInstall{}, err
		}
		writtenMCP = append(writtenMCP, server.Name)
	}

	now := s.opts.Now()
	id, err := newInstallID()
	if err != nil {
		rollback()
		return ports.MarketplaceInstall{}, err
	}
	install := ports.MarketplaceInstall{
		ID: id, SourceName: req.Source, Repo: plan.Repo, Item: req.Item,
		CommitSHA: plan.SHA, PlanDigest: plan.Digest, InstalledBy: principal.AccountID,
		InstalledAt: now, UpdatedAt: now, Files: recordedFiles(plan),
	}
	if err := s.opts.Store.CreateInstall(ctx, install); err != nil {
		rollback()
		return ports.MarketplaceInstall{}, err
	}
	return install, nil
}

// Installs lists every install. It makes no network call, so Status is unknown
// here; Check asks upstream.
func (s *Service) Installs(ctx context.Context) ([]InstallStatus, error) {
	installs, err := s.opts.Store.ListInstalls(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InstallStatus, 0, len(installs))
	for _, install := range installs {
		edited, err := s.editedNames(ctx, install)
		if err != nil {
			return nil, err
		}
		out = append(out, InstallStatus{Install: install, Status: "unknown", Edited: edited})
	}
	return out, nil
}

// Check compares one install with its source's current ref.
func (s *Service) Check(ctx context.Context, id string) (InstallStatus, Plan, error) {
	install, err := s.opts.Store.GetInstall(ctx, id)
	if err != nil {
		return InstallStatus{}, Plan{}, err
	}
	edited, err := s.editedNames(ctx, install)
	if err != nil {
		return InstallStatus{}, Plan{}, err
	}
	owned, err := s.ownedTargets(ctx, id)
	if err != nil {
		return InstallStatus{}, Plan{}, err
	}
	plan, err := s.planAt(ctx, PlanRequest{Source: install.SourceName, Item: install.Item, InstallID: id}, "", owned)
	if errors.Is(err, errItemNotFound) {
		return InstallStatus{Install: install, Status: "removed_upstream", Edited: edited}, Plan{}, nil
	}
	if err != nil {
		return InstallStatus{}, Plan{}, err
	}
	status := "update_available"
	if plan.Digest == install.PlanDigest {
		status = "up_to_date"
	}
	return InstallStatus{Install: install, Status: status, Edited: edited}, plan, nil
}

// Update applies a reviewed new version of an install. Local edits are kept
// unless the owner names them in replaceEdited, and MCP entries the owner has
// changed are never replaced.
func (s *Service) Update(ctx context.Context, id string, req ApproveRequest, replaceEdited []string) (ports.MarketplaceInstall, error) {
	install, err := s.opts.Store.GetInstall(ctx, id)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	req.InstallID = id
	owned, err := s.ownedTargets(ctx, id)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	plan, err := s.planAt(ctx, req.PlanRequest, req.SHA, owned)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	if plan.Digest != req.Digest {
		return ports.MarketplaceInstall{}, ErrDigestMismatch
	}
	if len(plan.Conflicts) > 0 {
		return ports.MarketplaceInstall{}, ErrConflicts
	}

	edited, err := s.editedSet(ctx, install)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	replace := toSet(replaceEdited)
	oldHash := map[string]string{}
	for _, f := range install.Files {
		oldHash[f.Kind+"/"+f.Target] = f.ContentHash
	}

	planned := map[string]bool{}
	var files []ports.MarketplaceFile
	for _, skill := range plan.Skills {
		planned[skill.Name] = true
		if owned[skill.Name] && edited[skill.Name] && !replace[skill.Name] {
			files = append(files, ports.MarketplaceFile{Kind: "skill", Target: skill.Name, ContentHash: oldHash["skill/"+skill.Name]})
			continue
		}
		err := s.opts.Skills.WriteBundle(ctx, ports.SkillBundle{
			Name: skill.Name, Description: skill.Description, Body: skill.Body,
			Source: req.Source + "/" + req.Item, Extra: skill.Extra, Files: skill.Files,
			Replace: owned[skill.Name],
		})
		if err != nil {
			return ports.MarketplaceInstall{}, err
		}
		files = append(files, ports.MarketplaceFile{Kind: "skill", Target: skill.Name, ContentHash: skill.Hash})
	}
	for _, f := range install.Files {
		if f.Kind != "skill" || planned[f.Target] {
			continue
		}
		if edited[f.Target] && !replace[f.Target] {
			if err := s.releaseSkill(ctx, f.Target); err != nil {
				return ports.MarketplaceInstall{}, err
			}
			continue
		}
		if err := s.opts.Skills.Delete(ctx, f.Target); err != nil && !errors.Is(err, ports.ErrSkillNotFound) {
			return ports.MarketplaceInstall{}, err
		}
	}

	for _, server := range plan.MCP {
		url, _, exists, err := s.opts.MCP.Get(server.Name)
		if err != nil {
			return ports.MarketplaceInstall{}, err
		}
		switch {
		case !owned[server.Name]:
			if err := s.opts.MCP.Add(server.Name, server.URL); err != nil {
				return ports.MarketplaceInstall{}, err
			}
		case edited[server.Name]:
			files = append(files, ports.MarketplaceFile{Kind: "mcp_server", Target: server.Name, ContentHash: oldHash["mcp_server/"+server.Name]})
			continue
		case exists && mcpHash(url) == server.Hash:
		default:
			if err := s.opts.MCP.Remove(server.Name); err != nil {
				return ports.MarketplaceInstall{}, err
			}
			if err := s.opts.MCP.Add(server.Name, server.URL); err != nil {
				return ports.MarketplaceInstall{}, err
			}
		}
		planned["mcp/"+server.Name] = true
		files = append(files, ports.MarketplaceFile{Kind: "mcp_server", Target: server.Name, ContentHash: server.Hash})
	}
	for _, f := range install.Files {
		if f.Kind != "mcp_server" || planned["mcp/"+f.Target] {
			continue
		}
		if edited[f.Target] {
			files = append(files, f)
			continue
		}
		if err := s.opts.MCP.Remove(f.Target); err != nil {
			return ports.MarketplaceInstall{}, err
		}
	}

	install.Repo = plan.Repo
	install.CommitSHA = plan.SHA
	install.PlanDigest = plan.Digest
	install.UpdatedAt = s.opts.Now()
	install.Files = files
	if err := s.opts.Store.ReplaceInstall(ctx, install); err != nil {
		return ports.MarketplaceInstall{}, err
	}
	return install, nil
}

// SetDisabled turns an install's skills and MCP entries off or on together.
// Enabling restores skills only: an MCP server stays off until the owner enables
// it in Connectors, because enabling a server is a trust decision.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool) error {
	install, err := s.opts.Store.GetInstall(ctx, id)
	if err != nil {
		return err
	}
	for _, f := range install.Files {
		switch f.Kind {
		case "skill":
			if err := s.opts.Skills.SetDisabled(ctx, f.Target, disabled); err != nil && !errors.Is(err, ports.ErrSkillNotFound) {
				return err
			}
		case "mcp_server":
			if !disabled {
				continue
			}
			if _, _, exists, err := s.opts.MCP.Get(f.Target); err != nil {
				return err
			} else if exists {
				if err := s.opts.MCP.SetEnabled(f.Target, false); err != nil {
					return err
				}
			}
		}
	}
	return s.opts.Store.SetInstallDisabled(ctx, id, disabled)
}

// Remove deletes an install's skills and MCP entries, except local edits the
// owner did not name in deleteEdited. A kept skill becomes hand-placed.
func (s *Service) Remove(ctx context.Context, id string, deleteEdited []string) error {
	install, err := s.opts.Store.GetInstall(ctx, id)
	if err != nil {
		return err
	}
	edited, err := s.editedSet(ctx, install)
	if err != nil {
		return err
	}
	del := toSet(deleteEdited)
	for _, f := range install.Files {
		keep := edited[f.Target] && !del[f.Target]
		switch f.Kind {
		case "skill":
			if keep {
				if err := s.releaseSkill(ctx, f.Target); err != nil {
					return err
				}
				continue
			}
			if err := s.opts.Skills.Delete(ctx, f.Target); err != nil && !errors.Is(err, ports.ErrSkillNotFound) {
				return err
			}
		case "mcp_server":
			if keep {
				continue
			}
			if err := s.opts.MCP.Remove(f.Target); err != nil {
				return err
			}
		}
	}
	return s.opts.Store.DeleteInstall(ctx, id)
}

// OwnerOf names the install that brought an MCP server, for the Connectors label.
// It returns empty for a server the owner added by hand.
func (s *Service) OwnerOf(mcpServer string) string {
	installs, err := s.opts.Store.ListInstalls(context.Background())
	if err != nil {
		return ""
	}
	for _, install := range installs {
		for _, f := range install.Files {
			if f.Kind == "mcp_server" && f.Target == mcpServer {
				return install.SourceName + "/" + install.Item
			}
		}
	}
	return ""
}

// ---- helpers ---------------------------------------------------------------

func (s *Service) findSource(name string) (Source, error) {
	sources, err := s.opts.Sources()
	if err != nil {
		return Source{}, err
	}
	for _, src := range sources {
		if src.Name == name {
			return src, nil
		}
	}
	return Source{}, fmt.Errorf("marketplace source %q is not configured", name)
}

// planAt builds a plan for one item at sha, or at the source's current ref when
// sha is empty. replacing holds the names this install already owns.
func (s *Service) planAt(ctx context.Context, req PlanRequest, sha string, replacing map[string]bool) (Plan, error) {
	src, err := s.findSource(req.Source)
	if err != nil {
		return Plan{}, err
	}
	if sha == "" {
		if sha, err = s.opts.Fetch.resolve(ctx, src.Repo, src.Ref); err != nil {
			return Plan{}, err
		}
	}
	tree, err := s.opts.Fetch.tarball(ctx, src.Repo, sha)
	if err != nil {
		return Plan{}, err
	}
	items, err := catalogItems(src, tree)
	if err != nil {
		return Plan{}, err
	}
	item, ok := findItem(items, req.Item)
	if !ok {
		return Plan{}, fmt.Errorf("%w: %s", errItemNotFound, req.Item)
	}
	if item.Unsupported != "" {
		return Plan{}, fmt.Errorf("%s cannot be installed: %s", req.Item, item.Unsupported)
	}

	repo := src.Repo
	if ext := item.External; ext != nil {
		extSHA := ext.SHA
		if extSHA == "" {
			if extSHA, err = s.opts.Fetch.resolve(ctx, ext.Repo, ext.Ref); err != nil {
				return Plan{}, err
			}
		}
		if tree, err = s.opts.Fetch.tarball(ctx, ext.Repo, extSHA); err != nil {
			return Plan{}, err
		}
		repo, sha = ext.Repo, extSHA
		item = Item{Name: item.Name, Description: item.Description, Kind: "plugin", Root: ext.Path}
	}

	skillSummaries, err := s.opts.Skills.List(ctx)
	if err != nil {
		return Plan{}, err
	}
	installed := map[string]bool{}
	for _, summary := range skillSummaries {
		installed[summary.Name] = true
	}
	var mcpErr error
	in := PlanInput{
		Source: src, Repo: repo, SHA: sha, Item: item, Renames: req.Renames,
		SkillExists: func(name string) bool { return installed[name] },
		MCPExists: func(name string) bool {
			_, _, exists, err := s.opts.MCP.Get(name)
			if err != nil && mcpErr == nil {
				mcpErr = err
			}
			return exists
		},
		Replacing: replacing,
	}
	plan, err := buildPlan(in, tree)
	if mcpErr != nil {
		return Plan{}, mcpErr
	}
	return plan, err
}

// ownedTargets returns the skill and MCP names an install owns, or nil when
// there is no install.
func (s *Service) ownedTargets(ctx context.Context, id string) (map[string]bool, error) {
	if id == "" {
		return nil, nil
	}
	install, err := s.opts.Store.GetInstall(ctx, id)
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	for _, f := range install.Files {
		owned[f.Target] = true
	}
	return owned, nil
}

// editedNames lists the skills and MCP entries an install owns that differ from
// what was written.
func (s *Service) editedNames(ctx context.Context, install ports.MarketplaceInstall) ([]string, error) {
	edited, err := s.editedSet(ctx, install)
	if err != nil {
		return nil, err
	}
	var names []string
	for name := range edited {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (s *Service) editedSet(ctx context.Context, install ports.MarketplaceInstall) (map[string]bool, error) {
	edited := map[string]bool{}
	for _, f := range install.Files {
		changed, err := s.fileChanged(ctx, f)
		if err != nil {
			return nil, err
		}
		if changed {
			edited[f.Target] = true
		}
	}
	return edited, nil
}

func (s *Service) fileChanged(ctx context.Context, f ports.MarketplaceFile) (bool, error) {
	switch f.Kind {
	case "skill":
		hash, err := s.currentSkillHash(ctx, f.Target)
		if errors.Is(err, ports.ErrSkillNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return hash != f.ContentHash, nil
	case "mcp_server":
		url, _, exists, err := s.opts.MCP.Get(f.Target)
		if err != nil {
			return false, err
		}
		return !exists || mcpHash(url) != f.ContentHash, nil
	}
	return false, nil
}

func (s *Service) currentSkillHash(ctx context.Context, name string) (string, error) {
	skill, err := s.opts.Skills.Inspect(ctx, name)
	if err != nil {
		return "", err
	}
	files := map[string]string{}
	for _, rel := range skill.Files {
		content, err := s.opts.Skills.ReadFile(ctx, name, rel)
		if err != nil {
			return "", err
		}
		files[rel] = content
	}
	return skillHash(skill.Description, skill.Body, files), nil
}

// releaseSkill keeps an owner-edited skill and clears its install marker, so it
// is hand-placed from then on.
func (s *Service) releaseSkill(ctx context.Context, name string) error {
	skill, err := s.opts.Skills.Inspect(ctx, name)
	if err != nil {
		return err
	}
	files := map[string]string{}
	for _, rel := range skill.Files {
		content, err := s.opts.Skills.ReadFile(ctx, name, rel)
		if err != nil {
			return err
		}
		files[rel] = content
	}
	return s.opts.Skills.WriteBundle(ctx, ports.SkillBundle{
		Name: name, Description: skill.Description, Body: skill.Body, Files: files, Replace: true,
	})
}

func (s *Service) rollbackWrites(ctx context.Context, skills, mcp []string) {
	for i := len(mcp) - 1; i >= 0; i-- {
		if err := s.opts.MCP.Remove(mcp[i]); err != nil {
			slog.Warn("marketplace rollback: MCP server left in place", "server", mcp[i], "error", err)
		}
	}
	for i := len(skills) - 1; i >= 0; i-- {
		if err := s.opts.Skills.Delete(ctx, skills[i]); err != nil && !errors.Is(err, ports.ErrSkillNotFound) {
			slog.Warn("marketplace rollback: skill left in place", "skill", skills[i], "error", err)
		}
	}
}

func recordedFiles(plan Plan) []ports.MarketplaceFile {
	var files []ports.MarketplaceFile
	for _, skill := range plan.Skills {
		files = append(files, ports.MarketplaceFile{Kind: "skill", Target: skill.Name, ContentHash: skill.Hash})
	}
	for _, server := range plan.MCP {
		files = append(files, ports.MarketplaceFile{Kind: "mcp_server", Target: server.Name, ContentHash: server.Hash})
	}
	return files
}

func findItem(items []Item, name string) (Item, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return Item{}, false
}

func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func newInstallID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
