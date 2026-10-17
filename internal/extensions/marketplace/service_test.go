package marketplace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ---- fakes -----------------------------------------------------------------

type fakeFetch struct {
	refs  map[string]string // repo@ref -> sha
	trees map[string]Tree   // repo@sha -> tree
}

func (f *fakeFetch) resolve(_ context.Context, repo, ref string) (string, error) {
	sha, ok := f.refs[repo+"@"+ref]
	if !ok {
		return "", fmt.Errorf("no ref %s@%s", repo, ref)
	}
	return sha, nil
}

func (f *fakeFetch) tarball(_ context.Context, repo, sha string) (Tree, error) {
	tree, ok := f.trees[repo+"@"+sha]
	if !ok {
		return Tree{}, fmt.Errorf("no tree %s@%s", repo, sha)
	}
	return tree, nil
}

type fakeSkill struct {
	desc, body, source string
	disabled           bool
	files              map[string]string
	extra              map[string]any
}

type fakeSkills struct {
	skills    map[string]*fakeSkill
	deleteErr error
}

func (f *fakeSkills) List(context.Context) ([]ports.SkillSummary, error) {
	var out []ports.SkillSummary
	for name, s := range f.skills {
		out = append(out, ports.SkillSummary{Name: name, Description: s.desc, Disabled: s.disabled, Source: s.source})
	}
	return out, nil
}

func (f *fakeSkills) Inspect(_ context.Context, name string) (ports.Skill, error) {
	s, ok := f.skills[name]
	if !ok {
		return ports.Skill{}, fmt.Errorf("%w: %q", ports.ErrSkillNotFound, name)
	}
	var files []string
	for file := range s.files {
		files = append(files, file)
	}
	sort.Strings(files)
	return ports.Skill{Name: name, Description: s.desc, Body: s.body, Disabled: s.disabled, Source: s.source, Extra: s.extra, Files: files}, nil
}

func (f *fakeSkills) ReadFile(_ context.Context, name, file string) (string, error) {
	s, ok := f.skills[name]
	if !ok {
		return "", ports.ErrSkillNotFound
	}
	return s.files[file], nil
}

func (f *fakeSkills) WriteBundle(_ context.Context, b ports.SkillBundle) error {
	existing, exists := f.skills[b.Name]
	if exists && !b.Replace {
		return fmt.Errorf("%w: %q", ports.ErrSkillExists, b.Name)
	}
	disabled := false
	if exists {
		disabled = existing.disabled
	}
	f.skills[b.Name] = &fakeSkill{desc: b.Description, body: b.Body, source: b.Source, disabled: disabled, files: b.Files, extra: b.Extra}
	return nil
}

func (f *fakeSkills) SetDisabled(_ context.Context, name string, disabled bool) error {
	s, ok := f.skills[name]
	if !ok {
		return ports.ErrSkillNotFound
	}
	s.disabled = disabled
	return nil
}

func (f *fakeSkills) Delete(_ context.Context, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.skills[name]; !ok {
		return ports.ErrSkillNotFound
	}
	delete(f.skills, name)
	return nil
}

type fakeServer struct {
	url     string
	enabled bool
}

type fakeMCP struct {
	servers map[string]fakeServer
	addErr  error
}

func (m *fakeMCP) Add(name, url string) error {
	if m.addErr != nil {
		return m.addErr
	}
	if _, ok := m.servers[name]; ok {
		return fmt.Errorf("MCP server %q is already configured", name)
	}
	m.servers[name] = fakeServer{url: url}
	return nil
}

func (m *fakeMCP) Get(name string) (string, bool, bool, error) {
	s, ok := m.servers[name]
	return s.url, s.enabled, ok, nil
}

func (m *fakeMCP) SetEnabled(name string, enabled bool) error {
	s, ok := m.servers[name]
	if !ok {
		return errors.New("not configured")
	}
	s.enabled = enabled
	m.servers[name] = s
	return nil
}

func (m *fakeMCP) Remove(name string) error {
	delete(m.servers, name)
	return nil
}

type fakeInstalls struct {
	rows       map[string]ports.MarketplaceInstall
	replaceErr error
	deleteErr  error
}

func (f *fakeInstalls) CreateInstall(_ context.Context, in ports.MarketplaceInstall) error {
	for _, row := range f.rows {
		if row.SourceName == in.SourceName && row.Item == in.Item {
			return ports.ErrMarketplaceInstallExists
		}
	}
	f.rows[in.ID] = in
	return nil
}

func (f *fakeInstalls) ReplaceInstall(_ context.Context, in ports.MarketplaceInstall) error {
	if f.replaceErr != nil {
		return f.replaceErr
	}
	if _, ok := f.rows[in.ID]; !ok {
		return ports.ErrMarketplaceInstallNotFound
	}
	f.rows[in.ID] = in
	return nil
}

func (f *fakeInstalls) ListInstalls(context.Context) ([]ports.MarketplaceInstall, error) {
	var out []ports.MarketplaceInstall
	for _, row := range f.rows {
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeInstalls) GetInstall(_ context.Context, id string) (ports.MarketplaceInstall, error) {
	row, ok := f.rows[id]
	if !ok {
		return ports.MarketplaceInstall{}, ports.ErrMarketplaceInstallNotFound
	}
	return row, nil
}

func (f *fakeInstalls) SetInstallDisabled(_ context.Context, id string, disabled bool) error {
	row, ok := f.rows[id]
	if !ok {
		return ports.ErrMarketplaceInstallNotFound
	}
	row.Disabled = disabled
	f.rows[id] = row
	return nil
}

func (f *fakeInstalls) DeleteInstall(_ context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.rows[id]; !ok {
		return ports.ErrMarketplaceInstallNotFound
	}
	delete(f.rows, id)
	return nil
}

// ---- fixtures --------------------------------------------------------------

const (
	sha1Commit = "1111111111111111111111111111111111111111"
	sha2Commit = "2222222222222222222222222222222222222222"
)

func deployRepo(skillBody string) Tree {
	return memTree(map[string]string{
		".claude-plugin/marketplace.json":    `{"name":"team","plugins":[{"name":"deploy-tools","source":"./plugins/deploy"}]}`,
		"plugins/deploy/.mcp.json":           `{"mcpServers":{"api":{"url":"https://mcp.example.com/mcp"}}}`,
		"plugins/deploy/skills/pdf/SKILL.md": "---\nname: pdf\ndescription: Fill PDF forms\n---\n\n" + skillBody + "\n",
	})
}

type harness struct {
	svc      *Service
	fetch    *fakeFetch
	skills   *fakeSkills
	mcp      *fakeMCP
	installs *fakeInstalls
	ctx      context.Context
}

func newHarness(t *testing.T) harness {
	t.Helper()
	fetch := &fakeFetch{
		refs:  map[string]string{"o/team@main": sha1Commit},
		trees: map[string]Tree{"o/team@" + sha1Commit: deployRepo("Fill forms.")},
	}
	h := harness{
		fetch:    fetch,
		skills:   &fakeSkills{skills: map[string]*fakeSkill{}},
		mcp:      &fakeMCP{servers: map[string]fakeServer{}},
		installs: &fakeInstalls{rows: map[string]ports.MarketplaceInstall{}},
		ctx:      ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "a"}),
	}
	svc, err := New(Options{
		Sources: func() ([]Source, error) {
			return []Source{{Name: "team", Kind: KindClaudeMarketplace, Repo: "o/team", Ref: "main"}}, nil
		},
		Skills: h.skills,
		MCP:    h.mcp,
		Store:  h.installs,
		Fetch:  fetch,
		Now:    func() time.Time { return time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	return h
}

// approve plans the item at the current ref and approves that exact plan.
func (h harness) approve(t *testing.T, req PlanRequest) ApproveRequest {
	t.Helper()
	plan, err := h.svc.Plan(h.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return ApproveRequest{PlanRequest: req, SHA: plan.CatalogSHA, Digest: plan.Digest}
}

func deployReq() PlanRequest {
	return PlanRequest{Source: "team", Item: "deploy-tools"}
}

// ---- tests -----------------------------------------------------------------

func TestInstallWritesSkillsAndMCP(t *testing.T) {
	h := newHarness(t)
	req := h.approve(t, deployReq())
	inst, err := h.svc.Install(h.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	skill, ok := h.skills.skills["pdf"]
	if !ok || skill.source != "team/deploy-tools" {
		t.Fatalf("skill pdf = %+v", skill)
	}
	server, ok := h.mcp.servers["deploy-tools-api"]
	if !ok || server.enabled {
		t.Fatalf("mcp server = %+v, want present and disabled", server)
	}
	if inst.InstalledBy != "a" || inst.PlanDigest != req.Digest || inst.CommitSHA != sha1Commit || len(inst.Files) != 2 {
		t.Fatalf("record = %+v", inst)
	}
}

func TestInstallDigestMismatch(t *testing.T) {
	h := newHarness(t)
	req := h.approve(t, deployReq())
	req.Digest = "not-the-digest-you-reviewed"
	if _, err := h.svc.Install(h.ctx, req); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	if len(h.skills.skills) != 0 || len(h.mcp.servers) != 0 || len(h.installs.rows) != 0 {
		t.Fatal("a refused install wrote something")
	}
}

func TestInstallConflictRefused(t *testing.T) {
	h := newHarness(t)
	h.skills.skills["pdf"] = &fakeSkill{desc: "mine", body: "my own", files: map[string]string{}}
	req := h.approve(t, deployReq())
	if _, err := h.svc.Install(h.ctx, req); !errors.Is(err, ErrConflicts) {
		t.Fatalf("err = %v, want ErrConflicts", err)
	}
	if h.skills.skills["pdf"].body != "my own" {
		t.Fatal("hand-placed skill was overwritten")
	}
}

func TestInstallRollsBackOnFailure(t *testing.T) {
	h := newHarness(t)
	h.mcp.addErr = errors.New("config write failed")
	req := h.approve(t, deployReq())
	if _, err := h.svc.Install(h.ctx, req); err == nil {
		t.Fatal("install succeeded despite MCP failure")
	}
	if _, ok := h.skills.skills["pdf"]; ok {
		t.Fatal("skill written by a failed install was not rolled back")
	}
	if len(h.installs.rows) != 0 {
		t.Fatal("a failed install left a record")
	}
}

func TestInstallTwiceRefused(t *testing.T) {
	h := newHarness(t)
	req := h.approve(t, deployReq())
	if _, err := h.svc.Install(h.ctx, req); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.Install(h.ctx, req)
	if !errors.Is(err, ports.ErrMarketplaceInstallExists) {
		t.Fatalf("second install err = %v, want ErrMarketplaceInstallExists", err)
	}
	if h.skills.skills["pdf"] == nil || h.skills.skills["pdf"].source != "team/deploy-tools" {
		t.Fatal("first install's files were touched by the second")
	}
}

func TestNoPrincipalFailsClosed(t *testing.T) {
	h := newHarness(t)
	req := h.approve(t, deployReq())
	if _, err := h.svc.Install(context.Background(), req); err == nil {
		t.Fatal("install without a principal succeeded")
	}
	if len(h.skills.skills) != 0 {
		t.Fatal("install without a principal wrote skills")
	}
}

func TestCheckUpToDate(t *testing.T) {
	h := newHarness(t)
	req := h.approve(t, deployReq())
	inst, err := h.svc.Install(h.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := h.svc.Check(h.ctx, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "up_to_date" {
		t.Fatalf("status = %q, want up_to_date", status.Status)
	}
}

func TestUpdateKeepsLocalEdits(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	h.skills.skills["pdf"].body = "owner edited this"

	h.fetch.refs["o/team@main"] = sha2Commit
	h.fetch.trees["o/team@"+sha2Commit] = deployRepo("Fill forms v2.")

	req := ApproveRequest{PlanRequest: PlanRequest{Source: "team", Item: "deploy-tools", InstallID: inst.ID}}
	plan, err := h.svc.Plan(h.ctx, req.PlanRequest)
	if err != nil {
		t.Fatal(err)
	}
	req.SHA, req.Digest = plan.CatalogSHA, plan.Digest

	if _, err := h.svc.Update(h.ctx, inst.ID, req, nil); err != nil {
		t.Fatal(err)
	}
	if got := h.skills.skills["pdf"].body; got != "owner edited this" {
		t.Fatalf("local edit overwritten: %q", got)
	}
	if _, err := h.svc.Update(h.ctx, inst.ID, req, []string{"pdf"}); err != nil {
		t.Fatal(err)
	}
	if got := h.skills.skills["pdf"].body; !strings.Contains(got, "v2") {
		t.Fatalf("replaceEdited did not replace: %q", got)
	}
}

func TestUpdateRenamedUpstreamConflict(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	h.skills.skills["docs"] = &fakeSkill{desc: "mine", body: "hand written", files: map[string]string{}}

	h.fetch.refs["o/team@main"] = sha2Commit
	h.fetch.trees["o/team@"+sha2Commit] = memTree(map[string]string{
		".claude-plugin/marketplace.json":    `{"name":"team","plugins":[{"name":"deploy-tools","source":"./plugins/deploy"}]}`,
		"plugins/deploy/skills/pdf/SKILL.md": "---\nname: docs\ndescription: Docs\n---\n\nbody\n",
	})
	req := ApproveRequest{PlanRequest: PlanRequest{Source: "team", Item: "deploy-tools", InstallID: inst.ID}}
	plan, err := h.svc.Plan(h.ctx, req.PlanRequest)
	if err != nil {
		t.Fatal(err)
	}
	req.SHA, req.Digest = plan.CatalogSHA, plan.Digest

	if _, err := h.svc.Update(h.ctx, inst.ID, req, nil); !errors.Is(err, ErrConflicts) {
		t.Fatalf("err = %v, want ErrConflicts", err)
	}
	if h.skills.skills["docs"].body != "hand written" {
		t.Fatal("hand-placed docs overwritten by a conflicting update")
	}
	if h.skills.skills["pdf"] == nil {
		t.Fatal("refused update removed the installed skill")
	}
}

func TestDisableAsUnit(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.mcp.SetEnabled("deploy-tools-api", true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetDisabled(h.ctx, inst.ID, true); err != nil {
		t.Fatal(err)
	}
	if !h.skills.skills["pdf"].disabled || h.mcp.servers["deploy-tools-api"].enabled {
		t.Fatal("disable did not turn off skills and MCP together")
	}
	if err := h.svc.SetDisabled(h.ctx, inst.ID, false); err != nil {
		t.Fatal(err)
	}
	if h.skills.skills["pdf"].disabled {
		t.Fatal("enable did not restore skills")
	}
	if h.mcp.servers["deploy-tools-api"].enabled {
		t.Fatal("enable turned MCP back on; that is the owner's choice in Connectors")
	}
}

func TestRemoveKeepsEditedAsHandPlaced(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	h.skills.skills["pdf"].body = "owner edited this"
	if err := h.svc.Remove(h.ctx, inst.ID, nil); err != nil {
		t.Fatal(err)
	}
	kept, ok := h.skills.skills["pdf"]
	if !ok || kept.source != "" || kept.body != "owner edited this" {
		t.Fatalf("edited skill = %+v, want kept and hand-placed", kept)
	}
	if _, ok := h.mcp.servers["deploy-tools-api"]; ok {
		t.Fatal("MCP entry from the install survived removal")
	}
	if _, err := h.svc.Installs(h.ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.installs.rows) != 0 {
		t.Fatal("record survived removal")
	}
}

func TestRemovedUpstreamStatus(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	h.fetch.refs["o/team@main"] = sha2Commit
	h.fetch.trees["o/team@"+sha2Commit] = memTree(map[string]string{
		".claude-plugin/marketplace.json": `{"name":"team","plugins":[]}`,
	})
	status, _, err := h.svc.Check(h.ctx, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "removed_upstream" {
		t.Fatalf("status = %q, want removed_upstream", status.Status)
	}
}

func TestOwnerOfMCPServer(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Install(h.ctx, h.approve(t, deployReq())); err != nil {
		t.Fatal(err)
	}
	if got := h.svc.OwnerOf("deploy-tools-api"); got != "team/deploy-tools" {
		t.Fatalf("OwnerOf = %q", got)
	}
	if got := h.svc.OwnerOf("hand-added"); got != "" {
		t.Fatalf("OwnerOf hand-added = %q, want empty", got)
	}
}
