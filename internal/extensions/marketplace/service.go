package marketplace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
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
	// Retarget points an existing entry at a new URL and leaves it disabled and
	// awaiting review, keeping the owner's tuning (OAuth client, timeouts,
	// filters).
	Retarget(name, url string) error
	// Snapshot captures the whole entry, or its absence, and returns how to put
	// it back exactly.
	Snapshot(name string) (restore func() error, err error)
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
	// mu serializes Install, Update, SetDisabled and Remove. Each snapshots
	// state before changing it, so two running at once could restore over the
	// other's work; one at a time keeps every rollback honest.
	mu sync.Mutex
}

// ErrDigestMismatch means the content at the approved commit no longer matches
// what the owner reviewed. Nothing is written.
var ErrDigestMismatch = errors.New("the content changed since you reviewed it; review again")

// ErrConflicts means a skill or MCP entry would overwrite something the install
// does not own. The owner renames it and reviews again.
var ErrConflicts = errors.New("rename the conflicting skills before installing")

// ConflictError is ErrConflicts with the names that conflict, so the review can
// ask for a rename of each one.
type ConflictError struct {
	Conflicts []string
}

func (e ConflictError) Error() string { return ErrConflicts.Error() }

func (e ConflictError) Unwrap() error { return ErrConflicts }

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
	Edited  []EditedFile
}

// EditedFile names one skill or MCP entry an install wrote that the owner has
// since changed. It carries the kind because a skill and an MCP entry may share
// a name, and the owner's choice about one must not reach the other.
type EditedFile struct {
	Kind   string
	Target string
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
			status.Error = sourceError(err)
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
	scope, err := s.updateScope(ctx, req.InstallID)
	if err != nil {
		return Plan{}, err
	}
	return s.planAt(ctx, req, "", scope)
}

// Install writes one reviewed item. Duplicate installs are refused before any
// file is touched, and a failure part-way removes what this call wrote.
func (s *Service) Install(ctx context.Context, req ApproveRequest) (ports.MarketplaceInstall, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

	plan, err := s.planAt(ctx, req.PlanRequest, req.SHA, updateScope{})
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	if plan.Digest != req.Digest {
		return ports.MarketplaceInstall{}, ErrDigestMismatch
	}
	if len(plan.Conflicts) > 0 {
		return ports.MarketplaceInstall{}, ConflictError{Conflicts: plan.Conflicts}
	}

	// The same journal Update and Remove use: a failure at any step removes
	// what this call wrote.
	var undo journal
	fail := func(err error) (ports.MarketplaceInstall, error) {
		return ports.MarketplaceInstall{}, undo.rollback(ctx, err)
	}
	for _, skill := range plan.Skills {
		if err := s.snapshotSkill(ctx, &undo, skill.Name); err != nil {
			return fail(err)
		}
		err := s.opts.Skills.WriteBundle(ctx, ports.SkillBundle{
			Name: skill.Name, Description: skill.Description, Body: skill.Body,
			Source: req.Source + "/" + req.Item, Extra: skill.Extra, Files: skill.Files,
		})
		if err != nil {
			return fail(err)
		}
	}
	for _, server := range plan.MCP {
		if err := s.snapshotMCP(&undo, server.Name); err != nil {
			return fail(err)
		}
		if err := s.opts.MCP.Add(server.Name, server.URL); err != nil {
			return fail(err)
		}
	}

	now := s.opts.Now()
	id, err := newInstallID()
	if err != nil {
		return fail(err)
	}
	install := ports.MarketplaceInstall{
		ID: id, SourceName: req.Source, Repo: plan.Repo, Item: req.Item,
		CommitSHA: plan.SHA, PlanDigest: plan.Digest, InstalledBy: principal.AccountID,
		InstalledAt: now, UpdatedAt: now, Files: recordedFiles(plan),
	}
	if err := s.opts.Store.CreateInstall(ctx, install); err != nil {
		return fail(err)
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
		edited, err := s.editedFiles(ctx, install)
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
	edited, err := s.editedFiles(ctx, install)
	if err != nil {
		return InstallStatus{}, Plan{}, err
	}
	scope, err := s.updateScope(ctx, id)
	if err != nil {
		return InstallStatus{}, Plan{}, err
	}
	plan, err := s.planAt(ctx, PlanRequest{Source: install.SourceName, Item: install.Item, InstallID: id}, "", scope)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	install, err := s.opts.Store.GetInstall(ctx, id)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	// The record, not the request, names what is being updated, so a request
	// cannot write another item's content under this install.
	req.InstallID = id
	req.Source, req.Item = install.SourceName, install.Item
	states, err := s.fileStates(ctx, install)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	scope := scopeOf(install, states)
	owned := scope.owned
	plan, err := s.planAt(ctx, req.PlanRequest, req.SHA, scope)
	if err != nil {
		return ports.MarketplaceInstall{}, err
	}
	if plan.Digest != req.Digest {
		return ports.MarketplaceInstall{}, ErrDigestMismatch
	}
	if len(plan.Conflicts) > 0 {
		return ports.MarketplaceInstall{}, ConflictError{Conflicts: plan.Conflicts}
	}
	replace := toSet(replaceEdited)
	recorded := map[string]ports.MarketplaceFile{}
	for _, f := range install.Files {
		recorded[fileKey(f.Kind, f.Target)] = f
	}

	// Every change below is snapshotted first, so a failure at any step puts the
	// skills and MCP entries back as they were and leaves the record unchanged.
	var undo journal
	fail := func(err error) (ports.MarketplaceInstall, error) {
		return ports.MarketplaceInstall{}, undo.rollback(ctx, err)
	}

	planned := map[string]bool{}
	var files []ports.MarketplaceFile
	// What the owner deleted and upstream still offers is withheld from the plan
	// while its name is free. An existing tombstone is kept; a fresh deletion
	// becomes one, so no later update brings it back, and it owns nothing.
	for _, key := range plan.Withheld {
		planned[key] = true
		f := recorded[key]
		if states[key].tombstone {
			files = append(files, f)
		} else {
			files = append(files, tombstone(f.Kind, f.Target))
		}
	}
	for _, skill := range plan.Skills {
		key := fileKey(kindSkill, skill.Name)
		planned[key] = true
		switch {
		case owned[key] && states[key].edited && !replace[skill.Name]:
			files = append(files, recorded[key])
			continue
		}
		if err := s.snapshotSkill(ctx, &undo, skill.Name); err != nil {
			return fail(err)
		}
		err := s.opts.Skills.WriteBundle(ctx, ports.SkillBundle{
			Name: skill.Name, Description: skill.Description, Body: skill.Body,
			Source: req.Source + "/" + req.Item, Extra: skill.Extra, Files: skill.Files,
			Replace: owned[key],
		})
		if err != nil {
			return fail(err)
		}
		files = append(files, ports.MarketplaceFile{Kind: kindSkill, Target: skill.Name, ContentHash: skill.Hash})
	}
	for _, f := range install.Files {
		key := fileKey(f.Kind, f.Target)
		if f.Kind != kindSkill || planned[key] {
			continue
		}
		if states[key].missing || states[key].tombstone {
			// Gone by the owner's hand and gone upstream, or a tombstone whose
			// name may now be someone else's: nothing to keep or delete.
			continue
		}
		if err := s.snapshotSkill(ctx, &undo, f.Target); err != nil {
			return fail(err)
		}
		if states[key].edited && !replace[f.Target] {
			if err := s.releaseSkill(ctx, f.Target); err != nil {
				return fail(err)
			}
			continue
		}
		if err := s.opts.Skills.Delete(ctx, f.Target); err != nil && !errors.Is(err, ports.ErrSkillNotFound) {
			return fail(err)
		}
	}

	for _, server := range plan.MCP {
		key := fileKey(kindMCP, server.Name)
		planned[key] = true
		state := states[key]
		switch {
		case !owned[key]:
			if err := s.snapshotMCP(&undo, server.Name); err != nil {
				return fail(err)
			}
			if err := s.opts.MCP.Add(server.Name, server.URL); err != nil {
				return fail(err)
			}
		case state.edited:
			// The owner enabled or changed it: their trust decision stands.
			files = append(files, recorded[key])
			continue
		case recorded[key].ContentHash != server.Hash:
			if err := s.snapshotMCP(&undo, server.Name); err != nil {
				return fail(err)
			}
			// A new URL is a new server to trust, so it goes back to review; the
			// owner's tuning of the entry is kept.
			if err := s.opts.MCP.Retarget(server.Name, server.URL); err != nil {
				return fail(err)
			}
		}
		files = append(files, ports.MarketplaceFile{Kind: kindMCP, Target: server.Name, ContentHash: server.Hash})
	}
	for _, f := range install.Files {
		key := fileKey(f.Kind, f.Target)
		if f.Kind != kindMCP || planned[key] || states[key].missing || states[key].tombstone {
			continue
		}
		if states[key].edited {
			files = append(files, f)
			continue
		}
		if err := s.snapshotMCP(&undo, f.Target); err != nil {
			return fail(err)
		}
		if err := s.opts.MCP.Remove(f.Target); err != nil {
			return fail(err)
		}
	}

	install.Repo = plan.Repo
	install.CommitSHA = plan.SHA
	install.PlanDigest = plan.Digest
	install.UpdatedAt = s.opts.Now()
	install.Files = files
	if err := s.opts.Store.ReplaceInstall(ctx, install); err != nil {
		return fail(err)
	}
	return install, nil
}

// SetDisabled turns an install's skills and MCP entries off or on together.
// Enabling restores skills only: an MCP server stays off until the owner enables
// it in Connectors, because enabling a server is a trust decision.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	install, err := s.opts.Store.GetInstall(ctx, id)
	if err != nil {
		return err
	}
	for _, f := range install.Files {
		if f.ContentHash == tombstoneHash {
			continue
		}
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
// owner did not name in deleteEdited, which names each by kind as well. A kept skill becomes hand-placed. Anything
// the owner already deleted by hand is skipped. A failure at any step restores
// what was changed and leaves the record in place.
func (s *Service) Remove(ctx context.Context, id string, deleteEdited []EditedFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	install, err := s.opts.Store.GetInstall(ctx, id)
	if err != nil {
		return err
	}
	states, err := s.fileStates(ctx, install)
	if err != nil {
		return err
	}
	del := make(map[string]bool, len(deleteEdited))
	for _, f := range deleteEdited {
		del[fileKey(f.Kind, f.Target)] = true
	}
	var undo journal
	fail := func(err error) error {
		return undo.rollback(ctx, err)
	}
	for _, f := range install.Files {
		state := states[fileKey(f.Kind, f.Target)]
		if state.missing || state.tombstone {
			continue
		}
		keep := state.edited && !del[fileKey(f.Kind, f.Target)]
		switch f.Kind {
		case kindSkill:
			if err := s.snapshotSkill(ctx, &undo, f.Target); err != nil {
				return fail(err)
			}
			if keep {
				if err := s.releaseSkill(ctx, f.Target); err != nil {
					return fail(err)
				}
				continue
			}
			if err := s.opts.Skills.Delete(ctx, f.Target); err != nil && !errors.Is(err, ports.ErrSkillNotFound) {
				return fail(err)
			}
		case kindMCP:
			if keep {
				continue
			}
			if err := s.snapshotMCP(&undo, f.Target); err != nil {
				return fail(err)
			}
			if err := s.opts.MCP.Remove(f.Target); err != nil {
				return fail(err)
			}
		}
	}
	if err := s.opts.Store.DeleteInstall(ctx, id); err != nil {
		return fail(err)
	}
	return nil
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
			if f.Kind == kindMCP && f.Target == mcpServer && f.ContentHash != tombstoneHash {
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
func (s *Service) planAt(ctx context.Context, req PlanRequest, sha string, scope updateScope) (Plan, error) {
	src, err := s.findSource(req.Source)
	if err != nil {
		return Plan{}, err
	}
	if sha != "" && !shaPattern.MatchString(sha) {
		// The approved commit must be a full SHA, or the install would record a
		// branch name as its commit and lose the pin.
		return Plan{}, fmt.Errorf("commit %q is not a full SHA", sha)
	}
	if sha == "" {
		if sha, err = s.opts.Fetch.resolve(ctx, src.Repo, src.Ref); err != nil {
			return Plan{}, err
		}
	}
	catalogSHA := sha
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
		if extSHA != "" && !shaPattern.MatchString(extSHA) {
			return Plan{}, fmt.Errorf("the catalog pins %s to %q, which is not a full commit", ext.Repo, extSHA)
		}
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
		Source: src, Repo: repo, SHA: sha, CatalogSHA: catalogSHA, Item: item, Renames: req.Renames,
		SkillExists: func(name string) bool { return installed[name] },
		MCPExists: func(name string) bool {
			_, _, exists, err := s.opts.MCP.Get(name)
			if err != nil && mcpErr == nil {
				mcpErr = err
			}
			return exists
		},
		Replacing: scope.owned,
		Withheld:  scope.withheld,
	}
	plan, err := buildPlan(in, tree)
	if mcpErr != nil {
		return Plan{}, mcpErr
	}
	return plan, err
}

const (
	kindSkill = "skill"
	kindMCP   = "mcp_server"
)

// fileKey names one thing an install wrote by its kind as well as its name, so
// an MCP entry can never count as owning a skill that happens to share its name.
func fileKey(kind, target string) string { return kind + "/" + target }

// ownedKeys is what an install owns, keyed by fileKey.
func ownedKeys(install ports.MarketplaceInstall) map[string]bool {
	owned := map[string]bool{}
	for _, f := range install.Files {
		if f.ContentHash == tombstoneHash {
			continue
		}
		owned[fileKey(f.Kind, f.Target)] = true
	}
	return owned
}

// tombstoneHash marks a record entry the owner deleted by hand. It can never be a
// real content hash, so the entry owns nothing and is never treated as an edit.
const tombstoneHash = "deleted-by-owner"

func tombstone(kind, target string) ports.MarketplaceFile {
	return ports.MarketplaceFile{Kind: kind, Target: target, ContentHash: tombstoneHash}
}

// updateScope is what an update plan is built against: the names the install
// owns, which are not conflicts, and the ones the owner deleted, which the
// update leaves out. Both are keyed by fileKey; a fresh install has neither.
type updateScope struct {
	owned, withheld map[string]bool
}

func (s *Service) updateScope(ctx context.Context, id string) (updateScope, error) {
	if id == "" {
		return updateScope{}, nil
	}
	install, err := s.opts.Store.GetInstall(ctx, id)
	if err != nil {
		return updateScope{}, err
	}
	states, err := s.fileStates(ctx, install)
	if err != nil {
		return updateScope{}, err
	}
	return scopeOf(install, states), nil
}

func scopeOf(install ports.MarketplaceInstall, states map[string]fileState) updateScope {
	withheld := map[string]bool{}
	for key, state := range states {
		// Only a name that is still free: a tombstoned name someone else now holds
		// stays a conflict, so the update cannot overwrite it.
		if state.missing {
			withheld[key] = true
		}
	}
	return updateScope{owned: ownedKeys(install), withheld: withheld}
}

// fileState is how one written thing compares with what was written. A missing
// file is the owner's deletion, not an edit, so it is skipped rather than kept.
type fileState struct {
	edited    bool
	missing   bool
	tombstone bool // the owner deleted it earlier; missing says whether the name is still free
}

// fileStates reads every file an install owns. A skill is edited when its
// content hash differs. An MCP entry is edited when its URL differs or the owner
// has enabled it: enabling is a trust decision, which an update must not undo.
func (s *Service) fileStates(ctx context.Context, install ports.MarketplaceInstall) (map[string]fileState, error) {
	states := map[string]fileState{}
	for _, f := range install.Files {
		key := fileKey(f.Kind, f.Target)
		if f.ContentHash == tombstoneHash {
			exists, err := s.targetExists(ctx, f)
			if err != nil {
				return nil, err
			}
			states[key] = fileState{tombstone: true, missing: !exists}
			continue
		}
		switch f.Kind {
		case kindSkill:
			hash, err := s.currentSkillHash(ctx, f.Target)
			if errors.Is(err, ports.ErrSkillNotFound) {
				states[key] = fileState{missing: true}
				continue
			}
			if err != nil {
				return nil, err
			}
			states[key] = fileState{edited: hash != f.ContentHash}
		case kindMCP:
			url, enabled, exists, err := s.opts.MCP.Get(f.Target)
			if err != nil {
				return nil, err
			}
			if !exists {
				states[key] = fileState{missing: true}
				continue
			}
			states[key] = fileState{edited: enabled || mcpHash(url) != f.ContentHash}
		}
	}
	return states, nil
}

// editedFiles lists the skills and MCP entries an install owns that the owner
// has changed, for the Installed view, ordered by name and then kind.
func (s *Service) editedFiles(ctx context.Context, install ports.MarketplaceInstall) ([]EditedFile, error) {
	states, err := s.fileStates(ctx, install)
	if err != nil {
		return nil, err
	}
	var edited []EditedFile
	for _, f := range install.Files {
		if states[fileKey(f.Kind, f.Target)].edited {
			edited = append(edited, EditedFile{Kind: f.Kind, Target: f.Target})
		}
	}
	sort.Slice(edited, func(i, j int) bool {
		if edited[i].Target != edited[j].Target {
			return edited[i].Target < edited[j].Target
		}
		return edited[i].Kind < edited[j].Kind
	})
	return edited, nil
}

func (s *Service) targetExists(ctx context.Context, f ports.MarketplaceFile) (bool, error) {
	if f.Kind == kindMCP {
		_, _, exists, err := s.opts.MCP.Get(f.Target)
		return exists, err
	}
	_, err := s.opts.Skills.Inspect(ctx, f.Target)
	if errors.Is(err, ports.ErrSkillNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) currentSkillHash(ctx context.Context, name string) (string, error) {
	skill, err := s.opts.Skills.Inspect(ctx, name)
	if err != nil {
		return "", err
	}
	files, err := s.readSkillFiles(ctx, name, skill.Files)
	if err != nil {
		return "", err
	}
	return skillHash(skill.Description, skill.Body, files), nil
}

func (s *Service) readSkillFiles(ctx context.Context, name string, rels []string) (map[string]string, error) {
	files := map[string]string{}
	for _, rel := range rels {
		content, err := s.opts.Skills.ReadFile(ctx, name, rel)
		if err != nil {
			return nil, err
		}
		files[rel] = content
	}
	return files, nil
}

// releaseSkill keeps an owner-edited skill and clears its install marker, so it
// is hand-placed from then on. Its unknown frontmatter keys are carried over.
func (s *Service) releaseSkill(ctx context.Context, name string) error {
	skill, err := s.opts.Skills.Inspect(ctx, name)
	if err != nil {
		return err
	}
	files, err := s.readSkillFiles(ctx, name, skill.Files)
	if err != nil {
		return err
	}
	return s.opts.Skills.WriteBundle(ctx, ports.SkillBundle{
		Name: name, Description: skill.Description, Body: skill.Body, Files: files, Extra: skill.Extra, Replace: true,
	})
}

// journal records how to undo each change an update or removal makes, so a
// failure part-way can put everything back. Rollback runs newest first.
type journal struct {
	steps []func(context.Context) error
}

func (j *journal) add(step func(context.Context) error) { j.steps = append(j.steps, step) }

// rollback undoes every recorded step and returns cause, saying so when some
// step could not be undone, so the owner is not told a failure left nothing behind.
func (j *journal) rollback(ctx context.Context, cause error) error {
	failed := 0
	for i := len(j.steps) - 1; i >= 0; i-- {
		if err := j.steps[i](ctx); err != nil {
			failed++
			slog.Warn("marketplace rollback step failed", "error", err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%w (rollback incomplete: %d change(s) could not be undone; see the logs)", cause, failed)
	}
	return cause
}

// snapshotSkill records how to restore a skill to what it is now: its content,
// marker, frontmatter and disabled flag, or its absence.
func (s *Service) snapshotSkill(ctx context.Context, j *journal, name string) error {
	skill, err := s.opts.Skills.Inspect(ctx, name)
	if errors.Is(err, ports.ErrSkillNotFound) {
		j.add(func(ctx context.Context) error {
			if err := s.opts.Skills.Delete(ctx, name); err != nil && !errors.Is(err, ports.ErrSkillNotFound) {
				return err
			}
			return nil
		})
		return nil
	}
	if err != nil {
		return err
	}
	files, err := s.readSkillFiles(ctx, name, skill.Files)
	if err != nil {
		return err
	}
	bundle := ports.SkillBundle{
		Name: name, Description: skill.Description, Body: skill.Body,
		Source: skill.Source, Extra: skill.Extra, Files: files, Replace: true,
	}
	disabled := skill.Disabled
	j.add(func(ctx context.Context) error {
		if err := s.opts.Skills.WriteBundle(ctx, bundle); err != nil {
			return err
		}
		return s.opts.Skills.SetDisabled(ctx, name, disabled)
	})
	return nil
}

// snapshotMCP records how to restore an MCP entry to exactly what it is now,
// tuning and trust decisions included, or to its absence.
func (s *Service) snapshotMCP(j *journal, name string) error {
	restore, err := s.opts.MCP.Snapshot(name)
	if err != nil {
		return err
	}
	j.add(func(context.Context) error { return restore() })
	return nil
}

// sourceError is what the panel shows for a source that cannot be read. A rate
// limit says so; anything else is a fixed message, and the detail goes to the log.
func sourceError(err error) string {
	var limited RateLimitError
	if errors.As(err, &limited) {
		return limited.Error()
	}
	slog.Warn("marketplace: source could not be read", "error", err)
	return "could not read this source from GitHub"
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
