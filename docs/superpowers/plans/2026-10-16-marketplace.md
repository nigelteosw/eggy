# Marketplace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an owner install, update, disable, and remove skills and remote MCP servers from Claude Code plugin marketplaces and `SKILL.md` repositories on GitHub, through a reviewed, SHA-pinned, digest-bound install in the web panel.

**Architecture:** Core first: directory skills, a `source` frontmatter key, and a names-stay index budget in `internal/skills` and `SkillsService`, plus an MCP `Preview` and a "Review and enable" flow in Connectors. Then a `marketplace` feature extension (`internal/extensions/marketplace/`) that fetches GitHub tarballs, parses catalogs into install plans, and writes through the skills service and `internal/config`, recording installs in two additive SQLite tables. The SPA gains a Plugins tab in Customize.

**Tech Stack:** Go 1.26 standard library (`archive/tar`, `compress/gzip`, `net/http`, `crypto/sha256`), `gopkg.in/yaml.v3` (already a dependency), SQLite store in `internal/storage/sqlite`, React + TypeScript SPA in `website/` tested with `bun test`.

**Spec:** `docs/superpowers/specs/2026-10-16-marketplace-design.md`

## Global Constraints

- `marketplace.enabled` defaults to false; when false, bootstrap passes nil: no route, no goroutine, no store, no outbound request, no prompt bytes, and `marketplace` absent from `/api/session` `features` (never added to `/api/mode`).
- No new tool. `skill_read` gains one optional `file` argument and stays `ports.ReadOnlyTool()`.
- No background loop, no auto-update, no `git` process, no credential on catalog traffic (`GITHUB_TOKEN` is never sent).
- GitHub API host `https://api.github.com` is an unexported package `var` in the extension, never config.
- Archive limits: 20 MiB compressed, 64 MiB uncompressed, 5,000 entries, regular files only; symlink, hard link, device, absolute path, or `..` rejects the whole archive.
- Skill bounds: `SKILL.md` 32 KiB (existing store limit), reference file 64 KiB, skill directory 256 KiB and 64 files; reference extensions `.md .txt .json .yaml .yml .csv`, UTF-8 only.
- Skill names: `^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`; descriptions ≤ 1,024 bytes (existing `maxDescriptionBytes`).
- `ports.SkillIndexBudget` stays 16 KiB. Name-only line text: `name: (description omitted; call skill_read)`.
- `source` frontmatter value: `<source-name>/<item>`.
- Plugin MCP entries: name `<plugin>-<server>`, `transport: streamable-http`, `auth: oauth`, `enabled: false`.
- New SQLite tables are `CREATE TABLE IF NOT EXISTS`; no `sqlite.MachineStateVersion` bump.
- Extension files in shared packages are named `ext_marketplace.go`; SPA code under `website/src/extensions/marketplace/`.
- Every panel mutation requires a session and passes CSRF (`guard(...)`).
- Every task ends with `make fmt vet test race build` passing; SPA tasks also `cd website && bun test && bun run build`.
- Commit directly to `main`; end commit messages with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. Upstream `SKILL.md` with CRLF line endings or a UTF-8 BOM — the store's frontmatter regex needs `\n`; expected: normalized to LF without BOM at plan time and installs cleanly (Task 7).
2. Upstream description over 1,024 bytes (Claude allows 1,536 with `when_to_use`) — expected: that skill is listed as skipped with "description exceeds 1024 bytes", the rest of the plugin still installs (Task 7).
3. Frontmatter `name` that is not a valid slug or differs from its directory — expected: frontmatter name is authoritative; an invalid one is a conflict needing a rename in the review, never a silent rewrite (Task 7).
4. Double-submitted Install for the same item — expected: the second request gets "already installed" (UNIQUE `(source_name, item)`), and no file written by the first is touched or rolled back (Task 11).
5. Update where upstream renamed a skill and the new name collides with a hand-placed skill — expected: the update plan shows a conflict and writes nothing until renamed; the hand-placed skill is never overwritten (Task 11).

---

### Task 1: Skill frontmatter keeps unknown keys and gains `source`

**Files:**
- Modify: `internal/ports/skills.go`
- Modify: `internal/skills/store.go` (`frontmatter`, `render`, `Write`, `SetDisabled`, `List`, `Read`)
- Test: `internal/skills/store_test.go`

**Interfaces:**
- Produces: `ports.SkillSummary.Source string` (`json:"source,omitempty"`), `ports.Skill.Source string`. Empty means hand-placed.
- Produces (internal): `frontmatter{Name, Description string; Disabled bool; Source string \`yaml:"source,omitempty"\`; Extra map[string]any \`yaml:",inline"\`}`; `render(front frontmatter, body string) ([]byte, error)`.

- [ ] **Step 1: Write failing tests**

```go
func TestWritePreservesSourceAndUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "pdf.md", "---\nname: pdf\ndescription: Fill PDFs\nsource: anthropic-skills/pdf\nlicense: MIT\nallowed-tools: [Bash]\n---\n\nbody\n")
	s := Open(dir, 0)
	if err := s.Write(ctx, "pdf", "Fill PDF forms", "new body"); err != nil { t.Fatal(err) }
	got := readRaw(t, dir, "pdf.md")
	for _, want := range []string{"source: anthropic-skills/pdf", "license: MIT", "allowed-tools:"} {
		if !strings.Contains(got, want) { t.Fatalf("lost %q:\n%s", want, got) }
	}
	sk, _ := s.Read(ctx, "pdf")
	if sk.Source != "anthropic-skills/pdf" || sk.Body != "new body" { t.Fatalf("%+v", sk) }
}

func TestSetDisabledPreservesSource(t *testing.T) // source survives a disable/enable round trip
func TestListReportsSource(t *testing.T)          // List()[0].Source == "anthropic-skills/pdf"; hand-placed == ""
```

- [ ] **Step 2: Run** `go test ./internal/skills/ -run 'Source|UnknownKeys'` — Expected: FAIL (field missing / keys dropped).
- [ ] **Step 3: Implement.** Add the fields; `render` marshals the whole `frontmatter` (yaml.v3 inline map keeps extras). `Write` copies `Source`, `Disabled`, `Extra` from the existing file, as it already does `Disabled`. Known keys must not be duplicated into `Extra` (yaml.v3 does not, given the struct fields).
- [ ] **Step 4: Run** `go test ./internal/skills/` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(skills): keep unknown frontmatter keys and record source`

---

### Task 2: Directory skills in the store

**Files:**
- Modify: `internal/skills/store.go`
- Create: `internal/skills/dir.go` (directory-layout helpers, bounds, allowlist)
- Modify: `internal/ports/skills.go`
- Modify: every test fake implementing `ports.SkillsStore` (grep `SkillsStore` in `_test.go` files across `internal/`; fakes are duplicated per package, not shared) — add `ReadFile` and `WriteBundle`, since this task widens the interface
- Test: `internal/skills/dir_test.go`

**Interfaces:**
- Produces on `ports.SkillsStore`:
  - `ReadFile(ctx context.Context, name, file string) (string, error)` — a reference file of a directory skill.
  - `WriteBundle(ctx context.Context, b ports.SkillBundle) error`.
- Produces: `ports.Skill.Files []string` (sorted relative paths of reference files; nil for flat skills).
- Produces: `ports.SkillBundle{Name, Description, Body, Source string; Extra map[string]any; Files map[string]string; Replace bool}`.
- Produces: `ports.ErrSkillExists = errors.New("skill already exists")`.
- Constants in `dir.go`: `maxReferenceBytes = 64 << 10`, `maxSkillDirBytes = 256 << 10`, `maxSkillDirFiles = 64`, `referenceExtensions = []string{".md", ".txt", ".json", ".yaml", ".yml", ".csv"}`.

Rules:
- `List` finds `skills/<name>.md` and `skills/<name>/SKILL.md`; a name in both is listed `Invalid: "both skills/<n>.md and skills/<n>/ exist"`.
- Directory names starting with `.` are ignored (staging dirs).
- `ReadFile` cleans `file`, refuses an absolute path, `..`, a non-allowlisted extension, invalid UTF-8, or more than `maxReferenceBytes`; it resolves with `filepath.Join` and checks the result stays inside the skill directory and is a regular file (`os.Lstat`, no symlinks).
- `WriteBundle` validates name, description, body as `Write` does, plus every `Files` key by the `ReadFile` rules and the dir totals. With `Replace == false` and the name present in either layout it returns `ErrSkillExists`. It writes the whole directory to `skills/.stage-<name>-<random>/`, then renames into place; on replace it renames the old dir to `skills/.old-<name>-<random>/`, renames the new one in, removes the old one. The new `SKILL.md` keeps the replaced skill's `disabled`. Held under the store mutex and `filelock.With(skills/<name>)`.
- `Write` on a directory skill rewrites only `SKILL.md`. `SetDisabled` likewise. `Delete` on a directory skill removes the directory.

- [ ] **Step 1: Write failing tests**

```go
func TestListFindsBothLayouts(t *testing.T)            // flat "a" and dir "b" both listed, valid
func TestBothLayoutsIsInvalid(t *testing.T)            // a.md + a/SKILL.md -> one entry, Invalid contains "both"
func TestReadFileRefusesTraversal(t *testing.T)        // "../x.md", "/etc/passwd", "a/../../x.md" -> error
func TestReadFileRefusesExtensionAndSymlink(t *testing.T) // "run.py" refused; symlink ref.md -> error
func TestReadFileBounded(t *testing.T)                 // 64 KiB ok, 64 KiB+1 refused
func TestWriteBundleCreateOnly(t *testing.T)           // second WriteBundle{Replace:false} -> errors.Is(err, ports.ErrSkillExists)
func TestWriteBundleReplaceKeepsDisabled(t *testing.T) // disable, replace, Read().Disabled == true; old files gone
func TestWriteBundleDirBounds(t *testing.T)            // 65 files or 256 KiB+1 total -> error, nothing on disk
func TestWriteBundleLeavesNoStagingOnFailure(t *testing.T) // invalid file path -> no ".stage-" dir remains
func TestDeleteRemovesDirectory(t *testing.T)
func TestReadListsFiles(t *testing.T)                  // Read().Files == []string{"examples/fill.md", "reference.md"}
```

- [ ] **Step 2: Run** `go test ./internal/skills/ -run 'Layouts|ReadFile|WriteBundle|DeleteRemoves|ReadLists'` — Expected: FAIL.
- [ ] **Step 3: Implement** as specified above. Update the package doc comment (it says "no bundled scripts/assets": keep that — references are text, never executed).
- [ ] **Step 4: Run** `go test ./internal/skills/` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(skills): directory skills with bounded text references`

---

### Task 3: `skill_read` reads reference files; service passthroughs

**Files:**
- Modify: `internal/core/services/skills.go`, `internal/core/services/skills_tools.go`
- Test: `internal/core/services/skills_test.go`

**Interfaces:**
- Consumes: Task 2 `ReadFile`, `WriteBundle`, `Skill.Files`.
- Produces: `(*SkillsService).ReadFile(ctx, name, file string) (string, error)` — refuses disabled skills like `Show`; `(*SkillsService).WriteBundle(ctx, ports.SkillBundle) error` — validates the name then delegates.
- `skill_read` schema: `{"type":"object","properties":{"name":{"type":"string","minLength":1},"file":{"type":"string","minLength":1}},"required":["name"],"additionalProperties":false}`. Without `file`, the result JSON gains `"files": [...]` when the skill has references. With `file`, the result is `{"name","file","content"}`.

- [ ] **Step 1: Write failing tests**

```go
func TestSkillReadListsFiles(t *testing.T)        // result JSON has files ["reference.md"]
func TestSkillReadFile(t *testing.T)              // {"name":"pdf","file":"reference.md"} -> content
func TestSkillReadFileRefusedWhenDisabled(t *testing.T)
func TestSkillReadStillReadOnly(t *testing.T)     // Definition().Effect == ports.ReadOnlyTool()
```

- [ ] **Step 2: Run** `go test ./internal/core/services/ -run SkillRead` — Expected: FAIL.
- [ ] **Step 3: Implement**; update the tool description to mention `file` in one clause.
- [ ] **Step 4: Run** `go test ./internal/core/... ./internal/bootstrap/` — Expected: PASS. Grep `internal/bootstrap` tests for `skill_read` schema or schema-byte assertions; where one pins the old schema, update its expected value, never loosen the assertion.
- [ ] **Step 5: Commit** `feat(skills): skill_read loads a skill's reference files`

---

### Task 4: Names-stay index budget

**Files:**
- Modify: `internal/core/services/skills.go` (`Enabled`)
- Modify: `internal/core/agent/prompt.go` (`renderSkills`)
- Test: `internal/core/services/skills_test.go`, `internal/core/agent/prompt_test.go`

**Interfaces:**
- `Enabled` returns enabled, valid skills in name order; a summary whose description did not fit has `Description == ""`. Valid skills always have a non-empty description, so empty is unambiguous.
- `renderSkills` renders an empty description as `name: (description omitted; call skill_read)`.

Algorithm (costs are the rendered line bytes plus a newline, so the budget measures what the model receives):

```
hint := "(description omitted; call skill_read)"
nameCost(s) = len(s.Name) + 2 + len(hint) + 1
fullCost(s) = len(s.Name) + 2 + len(s.Description) + 1
1. keep = all; while Σ nameCost(keep) > budget: drop the installed (Source != "") skill
   with the greatest name, else the hand-placed one with the greatest name; warn.
2. used = Σ nameCost(keep); for s in hand-placed by name, then installed by name:
   delta = fullCost(s) - nameCost(s); if used+delta <= budget { keep description; used += delta }
   else { s.Description = "" }
```

- [ ] **Step 1: Write failing tests**

```go
func TestIndexKeepsEveryNameBeforeAnyDescription(t *testing.T) // 200 skills, 400-byte descriptions: all 200 names present
func TestIndexHandPlacedDescriptionsFirst(t *testing.T)        // "zz" hand-placed keeps description; "aa" installed loses it
func TestIndexDropsInstalledNamesFirstWhenNamesOverflow(t *testing.T)
func TestIndexIsDeterministic(t *testing.T)                    // same input, shuffled store order -> identical output
func TestRenderSkillsNameOnlyHint(t *testing.T)                // "pdf: (description omitted; call skill_read)"
```

- [ ] **Step 2: Run** `go test ./internal/core/services/ ./internal/core/agent/ -run 'Index|NameOnly'` — Expected: FAIL.
- [ ] **Step 3: Implement** the algorithm in `Enabled`; keep disabled/invalid filtering before budgeting. Replace the existing capacity warning with one per dropped name.
- [ ] **Step 4: Run** `go test ./internal/core/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(skills): keep every skill name in the index; shed installed descriptions first`

---

### Task 5: Archive reader

**Files:**
- Create: `internal/extensions/marketplace/archive.go`, `internal/extensions/marketplace/doc.go`
- Test: `internal/extensions/marketplace/archive_test.go` (fixtures built in-test with `archive/tar` + `compress/gzip`)

**Interfaces:**
- Produces: `type Tree struct { Files map[string][]byte; Skipped map[string]int64 }` — paths are slash-separated with GitHub's top-level `<owner>-<repo>-<sha>/` prefix stripped. `Files` holds content for paths with an allowlisted reference extension, `SKILL.md`, or `.json`, each ≤ 256 KiB; `Skipped` maps every other regular file to its size.
- Produces: `func readArchive(r io.Reader) (Tree, error)` with limits `maxCompressed = 20 << 20`, `maxUncompressed = 64 << 20`, `maxEntries = 5000`.
- Produces: `var ErrArchiveRejected = errors.New("archive rejected")`; errors wrap it with the rule, e.g. `archive rejected: symlink skills/x/ref.md`.

- [ ] **Step 1: Write failing tests**

```go
func TestReadArchiveStripsPrefix(t *testing.T)    // "o-r-abc/skills/pdf/SKILL.md" -> Files["skills/pdf/SKILL.md"]
func TestReadArchiveSkipsNonText(t *testing.T)    // "skills/pdf/scripts/fill.py" in Skipped with size
func TestReadArchiveRejects(t *testing.T) {       // table: symlink, hardlink, "../x", "/abs", char device,
	// 5001 entries, >64 MiB uncompressed (sparse zero stream), >20 MiB compressed
	// each -> errors.Is(err, ErrArchiveRejected)
}
```

- [ ] **Step 2: Run** `go test ./internal/extensions/marketplace/ -run ReadArchive` — Expected: FAIL.
- [ ] **Step 3: Implement** with `io.LimitReader` on both the compressed and uncompressed streams (limit+1, refuse if reached).
- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit** `feat(marketplace): bounded GitHub tarball reader`

---

### Task 6: Catalog parsing

**Files:**
- Create: `internal/extensions/marketplace/catalog.go`
- Test: `internal/extensions/marketplace/catalog_test.go`, fixtures in `internal/extensions/marketplace/testdata/` as plain directories turned into `Tree`s by a test helper

**Interfaces:**
- Produces: `type Source struct { Name, Kind, Repo, Path, Ref string }` with `KindClaudeMarketplace = "claude_marketplace"`, `KindSkillsRepo = "skills_repo"`.
- Produces: `type Item struct { Name, Description, Kind string; Root string; External *RepoRef; Unsupported string }` — `Kind` is `"plugin"` or `"skill"`; `Root` is the item's directory inside the tree; `External` is set for a `github`/`git-subdir` source on github.com (`RepoRef{Repo, Path, Ref, SHA string}`); `Unsupported` is a reason such as `unsupported source: npm`.
- Produces: `func catalogItems(src Source, tree Tree) ([]Item, error)`.

Rules:
- `claude_marketplace`: parse `.claude-plugin/marketplace.json` (`name`, `plugins[].name`, `.description`, `.source`). `source` as a string is a relative path from the repo root (refuse `..`). As an object: `github` (`repo`, `ref`, `sha`), `git-subdir` (`url` as `owner/repo` or `https://github.com/owner/repo(.git)`, `path`, `ref`, `sha`) → `External`; any other `source` value or non-github.com URL → `Unsupported`.
- `skills_repo`: every directory directly under `src.Path` (default `skills`) holding `SKILL.md` is one `Kind: "skill"` item; description from its frontmatter.
- Malformed JSON returns an error naming the file; it does not panic.

- [ ] **Step 1: Write failing tests**

```go
func TestCatalogClaudeMarketplace(t *testing.T) // relative, github, git-subdir entries parsed; npm/url/archive -> Unsupported
func TestCatalogSkillsRepo(t *testing.T)        // anthropics/skills-shaped fixture -> one item per skill dir
func TestCatalogRefusesDotDot(t *testing.T)     // "./../x" -> Unsupported "path leaves the repository"
func TestCatalogMalformedJSON(t *testing.T)     // error mentions ".claude-plugin/marketplace.json"
```

- [ ] **Step 2–4:** run `go test ./internal/extensions/marketplace/ -run Catalog` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(marketplace): read Claude marketplaces and skills repositories`

---

### Task 7: Install plan and digest

**Files:**
- Create: `internal/extensions/marketplace/plan.go`
- Test: `internal/extensions/marketplace/plan_test.go`

**Interfaces:**
- Consumes: `Tree`, `Item`, `Source` (Tasks 5–6); `ports.SkillBundle` (Task 2).
- Produces:

```go
type PlanInput struct {
	Source   Source
	Repo     string            // repository the item's files came from
	SHA      string
	Item     Item
	Renames  map[string]string // upstream skill name -> chosen name
	SkillExists func(name string) bool
	MCPExists   func(name string) bool
	Replacing   map[string]bool // names this install already owns (update)
}
type PlannedSkill struct {
	Name, UpstreamName, Description, Body, Hash string
	Extra    map[string]any
	Files    map[string]string
	Warnings []string
}
type PlannedMCP struct{ Name, URL, Hash string }
type Skipped struct{ Path, Reason string }
type Plan struct {
	Source, Repo, SHA, Item string
	Skills     []PlannedSkill
	MCP        []PlannedMCP
	Skipped    []Skipped
	Conflicts  []string // names needing a rename
	IndexBytes int      // Σ fullCost of planned skills (Task 4 formula)
	Digest     string   // hex sha256 of the canonical plan
}
func buildPlan(in PlanInput, tree Tree) (Plan, error)
func skillHash(description, body string, files map[string]string) string
func mcpHash(url string) string
```

Rules (spec, *Mapping a plugin into Eggy*):
- Normalize every text file: strip a leading UTF-8 BOM, CRLF → LF.
- Parse `SKILL.md` frontmatter with yaml.v3 into `name`, `description`, rest into `Extra` (drop `source`, `disabled`). Frontmatter `name` is authoritative. An invalid slug, or a name for which `SkillExists` is true and `Replacing` is false, goes to `Conflicts` unless `Renames` supplies a valid free name.
- Description > 1,024 bytes, `SKILL.md` > 32 KiB, a reference > 64 KiB, or directory totals over bound: the skill goes to `Skipped` with the bound named.
- A file under the skill directory that is not allowlisted goes to `Skipped` with `Eggy never runs skill files`; if the body contains that file's relative path, add the warning `this skill refers to files Eggy will not install and may not work`.
- `.mcp.json` / `plugin.json` `mcpServers`: an entry with `url` and no `headers`, and no `${` in any value → `PlannedMCP{Name: plugin+"-"+server}`; with `headers` or `${…}` → `Skipped` "needs configuration Eggy cannot infer"; with `command` → `Skipped` "runs a downloaded program". A name for which `MCPExists` is true and not owned → `Conflicts`.
- `commands/`, `agents/`, `hooks/`, `bin/`, `.lsp.json`, `monitors/`, `themes/`, `userConfig` → `Skipped` "not supported in Eggy".
- Digest: `json.Marshal` of a struct holding Source.Name, Repo, SHA, Item, then skills sorted by Name (Name, Description, Body, sorted Files, sorted Extra keys) and MCP sorted by Name; sha256 hex. `Skipped` and `Warnings` are not in the digest.

- [ ] **Step 1: Write failing tests**

```go
func TestPlanMapsPluginComponents(t *testing.T)   // fixture plugin: 2 skills, 1 http MCP, 1 stdio MCP, hooks/, bin/
	// -> 2 skills, MCP [{"deploy-tools-api", ...}], Skipped has stdio, hooks, bin with spec reasons
func TestPlanNormalizesCRLFAndBOM(t *testing.T)    // Review Focus 1
func TestPlanSkipsLongDescription(t *testing.T)    // Review Focus 2: 1025-byte description -> Skipped, other skill planned
func TestPlanInvalidNameIsConflict(t *testing.T)   // Review Focus 3: name "PDF Tools" -> Conflicts; with Renames -> planned as rename
func TestPlanExistingNameIsConflict(t *testing.T)  // SkillExists("pdf") && !Replacing -> Conflicts
func TestPlanWarnsOnSkippedReference(t *testing.T) // body mentions scripts/fill.py -> warning
func TestPlanDigestStable(t *testing.T)            // two builds equal; changing one body byte changes Digest
func TestPlanDigestCoversRenames(t *testing.T)     // different Renames -> different Digest
```

- [ ] **Step 2–4:** run `go test ./internal/extensions/marketplace/ -run Plan` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(marketplace): digest-bound install plans`

---

### Task 8: GitHub fetcher

**Files:**
- Create: `internal/extensions/marketplace/github.go`
- Test: `internal/extensions/marketplace/github_test.go` (`httptest.Server`)

**Interfaces:**
- Produces: `var apiBase = "https://api.github.com"` (unexported; tests reassign it).
- Produces: `type github struct{ client *http.Client }` with `resolve(ctx, repo, ref string) (sha string, err error)` and `tarball(ctx, repo, sha string) (Tree, error)` (calls `readArchive`).
- `resolve`: empty `ref` → `GET /repos/{repo}` for `default_branch`; then `GET /repos/{repo}/commits/{ref}` with `Accept: application/vnd.github.sha` (body is the SHA).
- `tarball`: `GET /repos/{repo}/tarball/{sha}`; follows GitHub's redirect to `codeload.github.com` only over HTTPS.
- Produces: `type RateLimitError struct{ Reset time.Time }` for 403/429 with `X-RateLimit-Remaining: 0`; other non-2xx → error with status. 15-minute in-memory cache of up to 4 trees keyed by `repo@sha`.
- Requests carry `User-Agent: eggy` and never an `Authorization` header.

- [ ] **Step 1: Write failing tests**

```go
func TestResolveDefaultBranch(t *testing.T)
func TestResolveRef(t *testing.T)
func TestRateLimitError(t *testing.T)       // errors.As(err, &RateLimitError{}) with Reset from X-RateLimit-Reset
func TestNoAuthorizationHeader(t *testing.T) // server asserts r.Header.Get("Authorization") == "" even with GITHUB_TOKEN set via t.Setenv
func TestTarballCached(t *testing.T)        // second call: no request
```

- [ ] **Step 2–4:** run `go test ./internal/extensions/marketplace/ -run 'Resolve|RateLimit|Authorization|Tarball'` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(marketplace): unauthenticated GitHub ref and tarball fetch`

---

### Task 9: Install records in SQLite

**Files:**
- Create: `internal/ports/ext_marketplace.go`
- Create: `internal/storage/sqlite/ext_marketplace.go`
- Modify: `internal/storage/sqlite/store.go:150` (append `marketplaceSchema` to the schema list)
- Test: `internal/storage/sqlite/ext_marketplace_test.go`

**Interfaces:**
- Produces in `ports`:

```go
var ErrMarketplaceInstallNotFound = errors.New("install not found")
var ErrMarketplaceInstallExists   = errors.New("already installed")
type MarketplaceFile struct{ Kind, Target, ContentHash string } // Kind "skill" | "mcp_server"
type MarketplaceInstall struct {
	ID, SourceName, Repo, Item, CommitSHA, PlanDigest, InstalledBy string
	InstalledAt, UpdatedAt time.Time
	Disabled bool
	Files    []MarketplaceFile
}
type MarketplaceStore interface {
	CreateInstall(context.Context, MarketplaceInstall) error  // ErrMarketplaceInstallExists on UNIQUE(source_name,item)
	ReplaceInstall(context.Context, MarketplaceInstall) error // same ID; replaces Files
	ListInstalls(context.Context) ([]MarketplaceInstall, error)
	GetInstall(context.Context, string) (MarketplaceInstall, error)
	SetInstallDisabled(context.Context, string, bool) error
	DeleteInstall(context.Context, string) error
}
```

- Schema exactly as the spec, with `marketplace_files.kind` holding `'skill' | 'mcp_server'` (per-skill, per the spec's per-skill edit tracking). Writes run in one transaction.

- [ ] **Step 1: Write failing tests**

```go
func TestMarketplaceInstallRoundTrip(t *testing.T)
func TestMarketplaceInstallUnique(t *testing.T)   // errors.Is(err, ports.ErrMarketplaceInstallExists)
func TestMarketplaceReplaceInstallSwapsFiles(t *testing.T)
func TestMarketplaceDeleteRemovesFiles(t *testing.T)
func TestMarketplaceSchemaIsAdditive(t *testing.T) // opening an existing db without the tables works; MachineStateVersion unchanged
```

- [ ] **Step 2–4:** run `go test ./internal/storage/sqlite/ -run Marketplace` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(marketplace): install records in SQLite`

---

### Task 10: Config section and MCP approval setter

**Files:**
- Create: `internal/config/ext_marketplace.go`
- Modify: `internal/config/config.go` (add `Marketplace MarketplaceConfig \`yaml:"marketplace,omitempty"\`` beside `Finance`)
- Modify: `internal/config/config_mutate.go` (add `SetMCPServerApproval`, `AddMCPServerDisabled`)
- Test: `internal/config/ext_marketplace_test.go`, `internal/config/config_mutate_test.go`

**Interfaces:**
- Produces:

```go
type MarketplaceSource struct {
	Name string `yaml:"name"`; Kind string `yaml:"kind"`; Repo string `yaml:"repo"`
	Path string `yaml:"path,omitempty"`; Ref string `yaml:"ref,omitempty"`
}
type MarketplaceConfig struct {
	Enabled bool `yaml:"enabled"`
	Sources []MarketplaceSource `yaml:"sources,omitempty"`
}
func SetMarketplace(path string, enabled bool, sources []MarketplaceSource) error
func GetMarketplaceSources(path string) ([]MarketplaceSource, error)
// Plugin MCP entries: streamable-http, auth oauth, enabled false, defaults from applyDefaults.
// Refuses an existing name.
func AddMCPServerDisabled(path, name, url string) error
// One locked write of enabled + require_approval; works for any http server.
func SetMCPServerApproval(path, name string, enabled bool, requireApproval []string) error
```

- Validation (in `validateMarketplace`, called from `Validate`): `name` matches the skill slug pattern and is unique; `kind` ∈ {`claude_marketplace`, `skills_repo`}; `repo` matches `^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`; `path` relative, no `..`; `ref` has no whitespace.

- [ ] **Step 1: Write failing tests**

```go
func TestMarketplaceConfigValidates(t *testing.T) // table: bad kind, dup name, "../x" path, "notarepo" -> error
func TestSetMarketplaceRoundTrip(t *testing.T)
func TestAddMCPServerDisabled(t *testing.T)       // entry enabled=false, auth=oauth, transport=streamable-http; existing name -> error
func TestSetMCPServerApproval(t *testing.T)       // enabled=true and require_approval written in one write
func TestExampleConfigStillLoads(t *testing.T)    // existing TestLoadConfigAcceptsExample stays green
```

- [ ] **Step 2–4:** run `go test ./internal/config/ -run 'Marketplace|MCPServerDisabled|MCPServerApproval'` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(config): marketplace sources and MCP approval setter`

---

### Task 11: Marketplace service

**Files:**
- Create: `internal/extensions/marketplace/service.go`
- Test: `internal/extensions/marketplace/service_test.go` (fake fetcher returning fixture `Tree`s, fake skills, fake MCP config, in-memory `ports.MarketplaceStore` fake)

**Interfaces:**
- Consumes: Tasks 2–3 (skills), 6–9, and these interfaces declared in `service.go`:

```go
type Skills interface {
	List(context.Context) ([]ports.SkillSummary, error)
	Inspect(context.Context, string) (ports.Skill, error)
	ReadFile(context.Context, string, string) (string, error)
	WriteBundle(context.Context, ports.SkillBundle) error
	SetDisabled(context.Context, string, bool) error
	Delete(context.Context, string) error
}
type MCPConfig interface {
	Add(name, url string) error
	Get(name string) (url string, enabled, exists bool, err error)
	SetEnabled(name string, enabled bool) error
	Remove(name string) error
}
type Options struct {
	Sources func() ([]Source, error) // read live from config, so source edits need no restart
	Skills  Skills
	MCP     MCPConfig
	Store   ports.MarketplaceStore
	Now     func() time.Time
}
func New(opts Options) (*Service, error)
```

- Produces (panel-facing):

```go
func (s *Service) Sources(ctx) ([]SourceStatus, error)              // {Source, SHA, Error string}
func (s *Service) Items(ctx, source string) ([]Item, error)
func (s *Service) Plan(ctx, PlanRequest) (Plan, error)              // PlanRequest{Source, Item string; Renames map[string]string; InstallID string}
func (s *Service) Install(ctx, ApproveRequest) (ports.MarketplaceInstall, error) // ApproveRequest{PlanRequest; SHA, Digest string}
func (s *Service) Installs(ctx) ([]InstallStatus, error)            // {Install; Status: "up_to_date"|"update_available"|"removed_upstream"|"unknown"; Edited []string}
func (s *Service) Check(ctx, id string) (InstallStatus, Plan, error)
func (s *Service) Update(ctx, id string, req ApproveRequest, replaceEdited []string) (ports.MarketplaceInstall, error)
func (s *Service) SetDisabled(ctx, id string, disabled bool) error
func (s *Service) Remove(ctx, id string, deleteEdited []string) error
func (s *Service) OwnerOf(mcpServer string) string               // "<source>/<item>" or ""; for Connectors' from_plugin label
var ErrDigestMismatch = errors.New("the content changed since you reviewed it; review again")
var ErrConflicts      = errors.New("rename the conflicting skills before installing")
```

Behaviour:
- `InstalledBy` comes from `ports.PrincipalFromContext(ctx)`; no principal → error (fail closed).
- `Install`: rebuild the plan at `req.SHA` and compare `Digest` → `ErrDigestMismatch`; non-empty `Conflicts` → `ErrConflicts`; `CreateInstall` is attempted **after** writes but uniqueness is checked first via `ListInstalls`, and `CreateInstall`'s `ErrMarketplaceInstallExists` triggers rollback of this request's writes only. Writes: `WriteBundle{Replace:false, Source: source+"/"+item}` per skill, `MCP.Add` per server. On any failure, delete what this request wrote, in reverse, and return the first error; log any rollback failure.
- Local edit: a skill whose `skillHash(Inspect + ReadFile of each file)` differs from its recorded hash; an MCP entry whose URL hash differs.
- `Update`: plan with `Replacing` = names the install owns; edited skills are skipped unless listed in `replaceEdited`; edited MCP entries are never replaced; new MCP entries are added disabled; owned skills absent upstream are deleted unless edited. `ReplaceInstall` records the new SHA, digest, and hashes.
- `SetDisabled`: `Skills.SetDisabled` for each owned skill and `MCP.SetEnabled(false)` for each owned server when disabling; enabling restores skills only (MCP stays off until the owner enables it in Connectors), then records.
- `Remove`: delete unedited owned skills and MCP entries, plus edited ones listed in `deleteEdited`; for kept edited skills, rewrite via `WriteBundle{Replace:true}` with `Source: ""` so they become hand-placed; then `DeleteInstall`.

- [ ] **Step 1: Write failing tests**

```go
func TestInstallWritesSkillsAndMCP(t *testing.T)     // source key set; MCP added disabled; record has hashes and principal
func TestInstallDigestMismatch(t *testing.T)         // errors.Is(err, ErrDigestMismatch); nothing written
func TestInstallConflictRefused(t *testing.T)        // hand-placed "pdf" exists -> ErrConflicts; hand-placed file unchanged
func TestInstallRollsBackOnFailure(t *testing.T)     // MCP.Add fails -> skills written by this call deleted; no record
func TestInstallTwiceRefused(t *testing.T)           // Review Focus 4: second -> ErrMarketplaceInstallExists; first install's files intact
func TestUpdateKeepsLocalEdits(t *testing.T)         // edited skill untouched unless in replaceEdited
func TestUpdateRenamedUpstreamConflict(t *testing.T) // Review Focus 5: new upstream name == hand-placed -> Conflicts; nothing written
func TestDisableAsUnit(t *testing.T)                 // all skills disabled, MCP disabled; enable restores skills only
func TestRemoveKeepsEditedAsHandPlaced(t *testing.T) // kept skill has Source ""
func TestRemovedUpstreamStatus(t *testing.T)
func TestNoPrincipalFailsClosed(t *testing.T)
```

- [ ] **Step 2–4:** run `go test ./internal/extensions/marketplace/ -run 'Install|Update|Disable|Remove|Principal'` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(marketplace): install, update, disable, and remove service`

---

### Task 12: Panel routes and bootstrap wiring

**Files:**
- Create: `internal/panel/ext_marketplace.go`, `internal/bootstrap/ext_marketplace.go`
- Modify: `internal/panel/web.go` (field `Marketplace MarketplaceService`; feature `"marketplace"` when non-nil; mount routes), `internal/bootstrap/app.go` (construct beside finance; pass nil when disabled)
- Test: `internal/panel/ext_marketplace_test.go`, `internal/bootstrap/ext_marketplace_test.go`

**Interfaces:**
- Consumes: Task 11 `*marketplace.Service`; Task 10 config functions.
- Produces: `panel.MarketplaceService` interface declaring exactly the Task 11 methods the routes call (the panel never imports the extension; it declares its own JSON types).
- Produces: `bootstrap.newMarketplace(cfg config.Config, configPath string, store ports.MarketplaceStore, skills *services.SkillsService, now func() time.Time) (*marketplace.Service, error)` — returns nil, nil when `!cfg.Marketplace.Enabled`; adapts config functions to `marketplace.MCPConfig` and `GetMarketplaceSources` to `Options.Sources`.
- Routes (all `guard(...)`): exactly the spec's route table, plus `GET/PUT /api/config/marketplace` for the sources card (PUT → `config.SetMarketplace`). Errors: `ErrDigestMismatch` → 409, `ErrConflicts` → 409 with the conflict list, `ErrMarketplaceInstallExists` → 409, `ErrMarketplaceInstallNotFound` → 404, `RateLimitError` → 503 with `reset`, `ErrArchiveRejected` → 422; request bodies capped at 64 KiB; DELETE returns a JSON body.

- [ ] **Step 1: Write failing tests**

```go
func TestMarketplaceDisabledRegistersNothing(t *testing.T) // bootstrap: GET /api/marketplace/sources -> 404; features lacks "marketplace"; /api/mode lacks it
func TestMarketplaceEnabledFeature(t *testing.T)
func TestMarketplaceRoutesRequireSession(t *testing.T)      // every route 401 without session; mutations 403 without CSRF
func TestMarketplaceErrorMapping(t *testing.T)              // table of the error -> status mapping above
```

- [ ] **Step 2–4:** run `go test ./internal/panel/ ./internal/bootstrap/ -run Marketplace` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(marketplace): panel routes and bootstrap wiring`

---

### Task 13: MCP Preview and the enable-with-approval route

**Files:**
- Modify: `internal/mcp/manager.go` (add `Preview`), `internal/mcp/types.go` (add `ToolPreview`, `ErrRestartRequired`)
- Modify: `internal/panel/mcp.go`, `internal/panel/web.go`, `internal/bootstrap/mcp.go` (adapter)
- Test: `internal/mcp/manager_test.go`, `internal/panel/mcp_test.go` (create if absent)

**Interfaces:**
- Produces: `type ToolPreview struct{ Name, Description string }`; `var ErrRestartRequired = errors.New("restart to load this server")`.
- Produces: `func (m *Manager) Preview(ctx context.Context, name string) ([]ToolPreview, error)` — unknown runtime → `ErrRestartRequired`; uses `connectServer`'s connect-and-discover path with the runtime's config regardless of `Enabled`, closes the session, registers nothing, does not change status; `StateLoginRequired` → `ErrLoginRequired`.
- Produces: panel interface `MCPPreviewer{ Preview(ctx, name string) ([]MCPToolPreview, error) }` (panel's own type); routes `POST /api/config/mcp/{name}/preview` and `PUT /api/config/mcp/{name}/approval` (`{enabled bool, require_approval []string}` → `config.SetMCPServerApproval`). `ErrRestartRequired` → 409 `{"restart": true}`; `ErrLoginRequired` → 409 `{"login": true}`.
- The `GET /api/config/mcp` list gains `"from_plugin": "<source>/<item>"` for entries an install owns — supplied by an optional `PluginOwner(name string) string` func on `WebConfig`, set by bootstrap to `(*marketplace.Service).OwnerOf` when enabled (nil otherwise).

- [ ] **Step 1: Write failing tests**

```go
func TestPreviewDisabledServerListsTools(t *testing.T) // fake session advertises 2 tools -> 2 previews; Tools() unchanged; status unchanged
func TestPreviewUnknownServer(t *testing.T)            // errors.Is(err, ErrRestartRequired)
func TestPreviewLoginRequired(t *testing.T)
func TestApprovalRouteWritesBoth(t *testing.T)         // enabled + require_approval in config after PUT
func TestApprovalRouteRequiresSession(t *testing.T)
```

- [ ] **Step 2–4:** run `go test ./internal/mcp/ ./internal/panel/ -run 'Preview|ApprovalRoute'` (FAIL), implement, run (PASS).
- [ ] **Step 5: Commit** `feat(mcp): preview a server's tools and enable it with approvals in one write`

---

### Task 14: SPA — Connectors "Review and enable"

**Files:**
- Create: `website/src/McpEnableDialog.tsx`, `website/src/McpEnableDialog.test.tsx`
- Modify: `website/src/McpCard.tsx`

**Interfaces:**
- Consumes: Task 13 routes.
- A disabled http entry shows **Review and enable** (a `from <plugin>` label when `from_plugin` is set). The dialog calls preview; on `{login:true}` it starts the existing login flow (`GET /auth/mcp/{server}`); on `{restart:true}` it shows "Restart to load this server" with the existing restart action; otherwise it lists tools with a checkbox each, **all ticked by default**, and Save calls the approval route.

- [ ] **Step 1: Write failing tests** (`bun test`): all tools ticked by default; unticking one sends `require_approval` without it; restart and login responses render their states.
- [ ] **Step 2: Run** `cd website && bun test McpEnableDialog` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `cd website && bun test && bun run build` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): review and enable an MCP server with per-tool approvals`

---

### Task 15: SPA — Plugins tab

**Files:**
- Create: `website/src/extensions/marketplace/marketplace.ts` (API client and types), `PluginsTab.tsx`, `DiscoverView.tsx`, `InstallReview.tsx`, `InstalledView.tsx`, `SourcesCard.tsx`, and `*.test.tsx` beside the views
- Modify: `website/src/CustomizePage.tsx` (third tab `plugins`, shown only when session `features` includes `"marketplace"`; `?tab=plugins` in the URL)

**Interfaces:**
- Consumes: Task 12 routes.
- `InstallReview` shows every file's full text (collapsible), skipped items with reasons, warnings, conflicts with a rename input (changing a rename re-requests the plan), the index bytes and the count of skills that would be name-only, the copy "Skills are shared by every account. Their instructions reach the model on any turn where they match.", and **Install** sending `{source, item, renames, sha, digest}`. A 409 digest mismatch reloads the plan with the message.
- `InstalledView` shows source, short SHA, installed-by and when, status, edited skills; **Check for updates**, **Update** (review with per-skill "replace my edits"), **Disable/Enable**, **Remove** (lists edited skills with "delete" ticks).
- `SourcesCard` edits sources through `PUT /api/config/marketplace`.

- [ ] **Step 1: Write failing tests:** tab hidden without the feature; Install posts the digest it was shown; rename change refetches plan; 409 digest mismatch shows "review again"; Remove sends only ticked edited skills.
- [ ] **Step 2: Run** `cd website && bun test extensions/marketplace` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `cd website && bun test && bun run build`, then `make build` (embeds `internal/panel/webui/dist`). Check both widths in a browser against `anthropics/skills` (`skills_repo`) and one public Claude plugin marketplace with `marketplace.enabled: true`. Expected: install, update check, disable, remove all work; a plugin with stdio MCP lists it as skipped.
- [ ] **Step 5: Commit** `feat(web): Plugins tab for marketplace discover, review, and installs`

---

### Task 16: Documentation and recorded decisions

**Files:**
- Modify: `AGENTS.md` (replace the *A plugin marketplace or hub* paragraph under *Declined capabilities* with a *Decided* entry per the spec's *Changes to recorded decisions*; leave *Runtime-loaded plugins* as is)
- Modify: `TODO.md` (Customize step 4 replaced by a pointer to the spec; step 3 marked done)
- Modify: `docs/superpowers/specs/2026-10-12-customize-design.md` (one line: plugins and directory skills superseded by the marketplace spec)
- Create: `docs/src/content/docs/configure/marketplace.md`; Modify: `docs/src/content/docs/use/skills.md` (directory skills, `file` on `skill_read`, the index budget), `docs/src/content/docs/configure/mcp-servers.md` (Review and enable)
- Test: `internal/bootstrap/docs_consistency_test.go` (existing; keep green)

- [ ] **Step 1: Write the docs changes.**
- [ ] **Step 2: Run** `go test ./internal/bootstrap/ -run Docs && cd docs && bun run build` — Expected: PASS.
- [ ] **Step 3: Run** `make fmt vet test race build` and `make smoke` (report an unavailable Docker daemon as a blocker, not a pass).
- [ ] **Step 4: Commit** `docs: marketplace configuration, directory skills, and the recorded decision`
