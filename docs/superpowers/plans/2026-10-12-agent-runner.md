# Agent Runner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Eggy extension that lets an account start a coding run on an
allowed repository. The run edits a sandboxed checkout with Eggy's loop and
opens a pull request from the account's own GitHub PAT.

**Architecture:** One package, `internal/extensions/agentrunner/`, holds the
service, a single background worker, the coding tools, the sandbox and the
publisher. The worker runs `agent.Loop` with its own `agent.StaticTools`
source. Commands run as uid `coder` under Landlock through a hidden
`eggyd sandbox-exec` entry point. Runs are stored in SQLite (`agent_runs`),
and each PAT is sealed in `auth_records`. Panel, Telegram and bootstrap are
thin callers.

**Tech Stack:** Go 1.26, standard library, `golang.org/x/sys/unix` (already
in `go.mod`, becomes a direct dependency), SQLite store, React/TypeScript
panel (`website/`), Debian bookworm-slim image.

**Spec:** `docs/superpowers/specs/2026-10-11-agent-runner-design.md`. Read
it before Task 1. Every requirement in the spec is binding, including the
ones this plan does not repeat.

## Global Constraints

- Read `AGENTS.md` first. Its boundary rules apply to every task.
- Production code for the extension lives only in
  `internal/extensions/agentrunner/`. Outside it, touch only the files each
  task names.
- `internal/panel` must not import `internal/extensions/agentrunner`. It
  declares its own interface, as `internal/panel/finance.go` does.
- `internal/config` must not import the extension.
- Disabled means absent: with `agent_runner.enabled: false`, there is no tool,
  route, worker, goroutine, `features` entry or prompt bytes.
- Every store method reads the account from
  `ports.PrincipalFromContext(ctx)` and fails closed with `ports.ErrNoPrincipal`.
  No argument, body field or tool input may name the account.
- The PAT never appears in argv, a Git URL, Git config, a log line, a trace,
  the workspace, the sandbox environment, or any HTTP response body.
- Constants: one active run across the deployment; one queued run per account;
  30 minutes of active wall time per run; a 2,000,000-token budget per run;
  5 minutes per `bash` or `run_check` command; 64 KiB of output returned per
  command; 2 GiB per run directory; 10 GiB across all retained run directories;
  2 MiB of persisted redacted output; 24-hour mandate; 7-day retention for
  failed, interrupted and cancelled runs.
- Branch name: `eggy/<run-id>`. Run directory: `/data/agent-runs/<run-id>/`
  containing `ws/`, `home/` and `tmp/`. `<run-id>` is 16 random bytes as hex.
- Start copy, verbatim: "This run will push a feature branch and open a pull
  request on your behalf. Eggy will not approve or merge it. Your repository's
  CI will run on the branch."
- Extensions card copy, verbatim: "Runs commands as a sandboxed user that can
  reach the internet but not Eggy's data."
- Eggy never calls a GitHub endpoint that approves, merges, enables
  auto-merge, deletes a ref, creates a tag, or force-pushes. The GitHub client
  has no method for any of them.
- Test-first for every task. Before each commit run the focused package test,
  then `make fmt vet test race build`. Commit to `main` directly.
- Commit messages end with
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. If a different
  model implements a task, use that model's name.

## Review Focus

1. **A repository removed from `repositories:` while one of its runs is
   queued or waiting** must make that run fail with "repository no longer
   allowed", not crash or publish. Pinned in Task 6.
2. **A PAT whose access to the repository was revoked between start and
   publish** must fail the push with a clear message and keep the workspace.
   Pinned in Task 7.
3. **A task whose run made commits inside `ws/` with the agent's own `git`**
   must still publish only the file diff against the recorded base SHA, never
   those commits or their metadata. Pinned in Task 7.
4. **A model that never stops calling tools** must end at the token or
   wall-time cap as `failed`, never hang the worker. Pinned in Task 6.
5. **Two accounts enabling the same repository and running back to back**
   must get separate run directories and branches, and neither can read the
   other's run. Pinned in Task 2 (store) and Task 5 (filesystem).

---

### Task 1: Config section and repository checks

**Files:**
- Create: `internal/config/agent_runner.go`, `internal/config/agent_runner_test.go`
- Modify: `internal/config/config.go` (add `AgentRunner AgentRunnerConfig` field
  beside `Finance` at line 64; add `Checks [][]string` to `RepositoryConfig`
  at line 405), `internal/config/config_validate.go` (call
  `c.validateAgentRunner()` beside `c.validateFinance()` at line 143)

**Interfaces:**
- Produces:
  - `type AgentRunnerConfig struct { Enabled bool \`yaml:"enabled"\`; Model string \`yaml:"model,omitempty"\`; Skills map[string]bool \`yaml:"skills,omitempty"\` }`
  - `RepositoryConfig.Checks [][]string \`yaml:"checks,omitempty"\``
  - `var AgentRunnerSkills = []string{"brainstorming", "writing-plans", "verification-before-completion"}`
  - `func SetAgentRunner(path string, enabled bool, model string, skills map[string]bool) error`
  - `func SetRepositoryChecks(path, repository string, checks [][]string) error`

- [ ] **Step 1: Write failing tests** in `agent_runner_test.go`:
  - `TestAgentRunnerDisabledByDefault`: loading the example config gives `AgentRunner.Enabled == false`.
  - `TestAgentRunnerEnabledRequiresKnownModel`: `enabled: true` with
    `model: nope` fails validation, and the error contains
    `agent_runner.model "nope" is not a configured model alias`. With an
    alias from `models:`, validation passes.
  - `TestAgentRunnerRejectsUnknownSkill`: `skills: {foo: true}` fails, and the
    error contains `agent_runner.skills: unknown skill "foo"`.
  - `TestRepositoryChecksValidation`: an empty argv (`[]`) fails, an argv with
    an empty first element fails, and more than 8 checks fails.
    `[["go","test","./..."]]` passes.
  - `TestSetAgentRunnerRoundTrip` and `TestSetRepositoryChecksRoundTrip`:
    write to a temp config with the setter, reload, and compare equal. An
    unknown repository name returns an error containing `unknown repository`.
- [ ] **Step 2:** Run `go test ./internal/config/ -run 'AgentRunner|RepositoryChecks'`. Expected: FAIL (undefined).
- [ ] **Step 3:** Implement. Follow `internal/config/finance.go`: the
  struct's comment states it is startup configuration, and the setters use
  `mutate(path, func(cfg *Config) error {...})`. Validation runs only when
  `Enabled` is true, except checks validation, which always runs. Find the
  model aliases where existing code validates `/model` choices (search for
  `validateModels` or similar in `config_validate.go`).
- [ ] **Step 4:** Run the focused tests (PASS), then `make fmt vet test race build`.
- [ ] **Step 5:** Commit `feat(config): agent_runner section and repository checks`.

### Task 2: Ports and the `agent_runs` store

**Files:**
- Create: `internal/ports/agentrunner.go`, `internal/storage/sqlite/agentrunner.go`, `internal/storage/sqlite/agentrunner_test.go`
- Modify: wherever `financeSchema` is applied on open (search
  `financeSchema` in `internal/storage/sqlite/store.go`); add
  `agentRunnerSchema` beside it.

**Interfaces:**
- Produces in `internal/ports/agentrunner.go`:
  ```go
  type AgentRunState string // "queued","running","waiting_for_user","publishing","completed","failed","cancelled","interrupted"
  type PublishStage string  // "","committed","push_intended","pushed","pr_intended","pr_opened","delivered"
  type AgentRun struct {
      ID, AccountID, Repository, Brief, BriefDigest string
      BaseBranch, BaseSHA, Branch, HeadSHA string
      State AgentRunState; Revision int64; Stage PublishStage
      PATGeneration int64
      MandateExpires time.Time
      Question, Answer string
      UsedTokens int64; UsedActive time.Duration
      Output string            // redacted, ≤ 2 MiB, truncated from the front
      PRURL, Failure string
      OriginChannel, OriginDestination string
      Delivered bool
      CreatedAt, UpdatedAt time.Time
  }
  var ErrAgentRunNotFound = errors.New("agent run not found")
  var ErrAgentRunConflict = errors.New("agent run changed concurrently")
  type AgentRunStore interface {
      CreateAgentRun(ctx context.Context, run AgentRun) error
      GetAgentRun(ctx context.Context, id string) (AgentRun, error)
      ListAgentRuns(ctx context.Context, limit int) ([]AgentRun, error)
      // UpdateAgentRun writes run iff the stored Revision equals run.Revision, then increments it.
      UpdateAgentRun(ctx context.Context, run AgentRun) (AgentRun, error)
      SetRepositoryEnabled(ctx context.Context, repository string, enabled bool) error
      EnabledRepositories(ctx context.Context) ([]string, error)
      // Unscoped worker reads: they take no principal and are only called by the worker.
      NextQueuedAgentRun(ctx context.Context) (AgentRun, bool, error)
      AgentRunsInStates(ctx context.Context, states ...AgentRunState) ([]AgentRun, error)
  }
  ```
- Produces: `(*sqlite.Store)` implements `ports.AgentRunStore`.

- [ ] **Step 1: Write failing tests:**
  - `TestAgentRunCreateGetScopedToAccount`: account A creates a run. A can
    `Get` it. B's `Get` returns `ErrAgentRunNotFound`. A context with no
    principal returns `ports.ErrNoPrincipal`.
  - `TestAgentRunUpdateIsConditional`: two updates from the same revision.
    The second returns `ErrAgentRunConflict`.
  - `TestRepositoryEnablementPerAccount`: A enables `eggy`. B's
    `EnabledRepositories` is empty.
  - `TestNextQueuedAgentRunOldestFirst`.
  - `TestAgentRunSchemaIsAdditive`: opening an existing database twice
    succeeds, and `MachineStateVersion` is unchanged.
- [ ] **Step 2:** Run `go test ./internal/storage/sqlite/ -run AgentRun`. Expected: FAIL.
- [ ] **Step 3:** Implement. Create two tables `IF NOT EXISTS`:
  - `agent_runs`: one column per field; times as RFC 3339 text; durations as
    integer nanoseconds. Index on `(account_id, created_at)` and `(state, created_at)`.
  - `agent_runner_repos`: `(account_id, repository)` primary key.

  Put the schema-comment rationale beside the constant, as `finance.go` does.
- [ ] **Step 4:** Focused tests PASS; then `make fmt vet test race build`.
- [ ] **Step 5:** Commit `feat(storage): agent_runs store`.

### Task 3: Account PAT credential and GitHub client

**Files:**
- Create: `internal/extensions/agentrunner/credential.go`, `credential_test.go`, `github.go`, `github_test.go`

**Interfaces:**
- Consumes: `grants.Records`, `grants.NewSealer(label, key)`, `ports.PrincipalFromContext`.
- Produces:
  ```go
  type Credentials struct{ /* records grants.Records; sealer *grants.Sealer; gh *GitHub */ }
  func NewCredentials(records grants.Records, encryptionKey string, gh *GitHub) (*Credentials, error)
  type PATStatus struct { Connected bool; Login string; Generation int64; Repositories map[string]bool /* repo name -> push access */ }
  func (c *Credentials) Set(ctx context.Context, token string, repos []config.RepositoryConfig) (PATStatus, error)
  func (c *Credentials) Status(ctx context.Context) (PATStatus, error)
  func (c *Credentials) Disconnect(ctx context.Context) error
  func (c *Credentials) token(ctx context.Context) (string, int64, error) // unexported; only github.go/publish.go call it

  type GitHub struct{ /* baseURL string; client *http.Client */ }
  func NewGitHub(client *http.Client) *GitHub // baseURL is an unexported package var "https://api.github.com", overridden only in tests
  type Identity struct { Login, NoReplyEmail string; ID int64 }
  func (g *GitHub) Identity(ctx context.Context, token string) (Identity, error)
  func (g *GitHub) CanPush(ctx context.Context, token, owner, repo string) (bool, error)
  func (g *GitHub) BranchProtected(ctx context.Context, token, owner, repo, branch string) (bool, error)
  func (g *GitHub) CreatePR(ctx context.Context, token, owner, repo string, pr PullRequest) (string, error)
  func (g *GitHub) FindPRByMarker(ctx context.Context, token, owner, repo, head, marker string) (string, bool, error)
  type PullRequest struct { Title, Body, Head, Base string; Draft bool }
  func ParseCloneURL(cloneURL string) (owner, repo string, err error) // github.com https URLs only
  ```
- The sealed record has section `"agent_runner_pat"` and key `<accountID>`.
  Its associated data is the literal string `agent_runner|github|<accountID>|<generation>`.
  `Set` and `Disconnect` each increase the generation by one.
  `NoReplyEmail` is `<id>+<login>@users.noreply.github.com`.

- [ ] **Step 1: Write failing tests** against an `httptest.Server`:
  - `TestCredentialsSetVerifiesIdentityAndAccess`: the fake serves `/user`
    and `/repos/o/r`, with `permissions.push`. `Status` returns the login,
    `Repositories["r"]==true`, and generation 1.
  - `TestCredentialsNeverReturnToken`: marshal `PATStatus` to JSON. It does
    not contain the token string.
  - `TestCredentialsScopedToAccount`: A sets a token. B's `Status` reports
    `Connected==false`.
  - `TestCredentialsReplaceAdvancesGeneration`: Set, then Set again, gives
    generation 2. A record sealed under generation 1's associated data fails
    to open.
  - `TestGitHubHasNoMergeOrApprove`: use reflection over `*GitHub` methods to
    assert that no method name contains `Merge`, `Approve`, `Review`, `Tag`,
    `Delete` or `Force`.
  - `TestFindPRByMarker` and `TestBranchProtectedFailsClosed`: a 500 response
    from the protection endpoint returns an error, not `false`.
  - `TestParseCloneURL`: accepts `https://github.com/o/r.git` and
    `https://github.com/o/r`. Rejects ssh URLs, other hosts, and URLs with
    embedded credentials.
- [ ] **Step 2:** Run `go test ./internal/extensions/agentrunner/ -run 'Credential|GitHub|ParseClone'`. Expected: FAIL.
- [ ] **Step 3:** Implement with `net/http` and `encoding/json` only. Send
  `Authorization: Bearer <token>` and
  `Accept: application/vnd.github+json`. Bound every response read with
  `io.LimitReader` at 1 MiB.
- [ ] **Step 4:** PASS; then `make fmt vet test race build`.
- [ ] **Step 5:** Commit `feat(agentrunner): sealed account PAT and GitHub client`.

### Task 4: Sandbox

**Files:**
- Create: `internal/extensions/agentrunner/sandbox.go` (shared types),
  `sandbox_linux.go`, `sandbox_other.go` (`//go:build !linux`: `Probe`
  returns `ErrSandboxUnsupported`), `sandbox_linux_test.go`
- Modify: `cmd/eggyd/main.go` (at the top of `main()`, before
  `flag.Parse`: if `len(os.Args) > 1 && os.Args[1] == "sandbox-exec"`, call
  `os.Exit(agentrunner.SandboxExecMain(os.Args[2:]))`), `Dockerfile`, and a
  boot-time `/data` permission step (Step 3 says where)

**Interfaces:**
- Produces:
  ```go
  var ErrSandboxUnsupported = errors.New("sandbox unsupported on this platform")
  type SandboxSpec struct {
      RunDir   string        // /data/agent-runs/<id>
      Argv     []string      // e.g. ["bash","-c",cmd] or a checks entry
      Timeout  time.Duration // 5m
      OutputCap int          // 64 KiB
      DenyPort int           // Eggy's listen port, for the probe's assertion only
  }
  type SandboxResult struct { ExitCode int; Output string; TimedOut, Truncated bool }
  type Sandbox struct{ /* coderUID, coderGID uint32; self string (os.Executable) */ }
  func NewSandbox() (*Sandbox, error)                     // looks up user "coder"
  func (s *Sandbox) Run(ctx context.Context, spec SandboxSpec) (SandboxResult, error)
  func (s *Sandbox) Probe(ctx context.Context, dataDir string, eggyPort int) error // nil = verified
  func (s *Sandbox) PrepareRunDir(runDir string) error   // mkdir ws/ home/ tmp/ 0700, chown coder
  func SandboxExecMain(args []string) int                // runs inside the child
  ```
- How it works:
  - `Run` starts `<self> sandbox-exec <runDir> -- <argv...>` with
    `SysProcAttr{Credential:{Uid,Gid,NoSetGroups:false}, Setpgid:true}` and
    working directory `<runDir>/ws`.
  - The environment is built from scratch: `PATH=/usr/local/go/bin:/usr/local/node/bin:/usr/bin:/bin`,
    `HOME=<runDir>/home`, `TMPDIR=<runDir>/tmp`, `LANG=C.UTF-8`,
    `GOMODCACHE=<runDir>/home/go/pkg/mod`, `GOCACHE=<runDir>/home/.cache/go-build`,
    `npm_config_cache=<runDir>/home/.npm`, `GIT_CONFIG_GLOBAL=<runDir>/home/.gitconfig`,
    `GIT_CONFIG_NOSYSTEM=1`.
  - On timeout or context cancel, kill the process group with `SIGKILL`.
  - `SandboxExecMain`, in order:
    1. `unix.Prctl(PR_SET_NO_NEW_PRIVS,1,0,0,0)`.
    2. `setrlimit`: `RLIMIT_NPROC=512`, `RLIMIT_FSIZE=1 GiB`, `RLIMIT_AS=8 GiB`.
    3. Create the Landlock ruleset with ABI 6 using raw
       `unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, ...)`. Handled
       accesses: all filesystem rights, `NET_BIND_TCP` and `NET_CONNECT_TCP`,
       and scoped `ABSTRACT_UNIX_SOCKET|SIGNAL`.
    4. Add rules. Read and execute: `/usr`, `/bin`, `/lib`, `/lib64`, `/etc`,
       `/usr/local/go`, `/usr/local/node`, `/dev/null`, `/dev/urandom`,
       `/proc/self`. Full access: `<runDir>`. Connect: ports 443 and 80.
    5. `landlock_restrict_self`, then `unix.Exec(argv...)`.

    If any step fails, write `sandbox-exec: <step>: <err>` to stderr and
    return 125. Never exec unrestricted.
  - `Probe` creates a temporary run dir under `<dataDir>/agent-runs/.probe-<rand>`
    and asserts all of the following, in this order:
    - `true` exits 0;
    - `cat <dataDir>/eggy.db` exits non-zero;
    - `touch <dataDir>/x` exits non-zero;
    - `bash -c 'exec 3<>/dev/tcp/127.0.0.1/<eggyPort>'` exits non-zero;
    - `kill -0 <eggyd pid>` exits non-zero.

    It removes the probe dir and returns the first failure as
    `fmt.Errorf("sandbox probe: %s", name)`.

- [ ] **Step 1: Write failing tests** in `sandbox_linux_test.go`. Each test
  starts with `if os.Getuid()!=0 { t.Skip("needs root and user coder") }`; on
  macOS the file doesn't build, so it is skipped automatically.
  - `TestSandboxProbePasses`.
  - `TestSandboxCannotReadOutsideRunDir`.
  - `TestSandboxCannotConnectToNonWebPort`.
  - `TestSandboxEnvHasNoParentSecrets`: set `EGGY_TEST_SECRET=s3cr3t` in the
    parent, run `env`, and assert the output does not contain `s3cr3t`.
  - `TestSandboxTimeoutKillsGroup`: `bash -c 'sleep 100 & sleep 100'` with a
    1s timeout returns `TimedOut` and leaves no `sleep` process owned by
    `coder`.
  - `TestSandboxExecFailsClosed`: a cross-platform test that calls
    `SandboxExecMain` with an invalid run dir and expects 125.
- [ ] **Step 2:** On macOS, run `go test ./internal/extensions/agentrunner/ -run Sandbox`.
  Expected: `TestSandboxExecFailsClosed` FAILS; the Linux tests don't build.
  Also run in Docker as root:
  `docker run --rm -v "$PWD":/src -w /src golang:1.26-bookworm sh -c 'useradd -r -M coder && go test ./internal/extensions/agentrunner/ -run Sandbox -v'`.
  Expected: FAIL (undefined).
- [ ] **Step 3:** Implement. Constants not exported by `x/sys/unix` (for
  example the ABI 6 scope flags) are declared locally with a comment citing
  `include/uapi/linux/landlock.h`. `/data` permissions:
  - `Sandbox.LockDataDir(dataDir string) error` sets `dataDir` to `0711`,
    every other entry directly under it to root-owned `0700`/`0600`, and
    `agent-runs` to `0711`, root-owned. Run directories inside it are owned by
    `coder`, `0700`.
  - Bootstrap calls `LockDataDir` (Task 8) only when the extension is enabled
    and only when `os.Getuid()==0`.
  - Dockerfile, final stage:
    - add `RUN useradd --system --no-create-home --shell /usr/sbin/nologin coder`;
    - install Node 22 LTS from `https://nodejs.org/dist/v22.<x>/node-v22.<x>-linux-x64.tar.xz`
      into `/usr/local/node`. Use an `ARG NODE_VERSION` and an
      `ARG NODE_SHA256`, and run `echo "$NODE_SHA256  node.tar.xz" | sha256sum -c -`
      before extracting. Take the hash from that release's `SHASUMS256.txt`;
    - add `xz-utils` to the apt line;
    - append `/usr/local/node/bin` to `PATH`.
- [ ] **Step 4:** Docker test run PASSES; `make fmt vet test race build` passes on macOS; `docker build .` succeeds.
- [ ] **Step 5:** Commit `feat(agentrunner): coder-user Landlock sandbox and sandbox-exec entry`.

### Task 5: Workspace, file tools, `bash`, `run_check`

**Files:**
- Create: `internal/extensions/agentrunner/workspace.go`, `workspace_test.go`, `codetools.go`, `codetools_test.go`

**Interfaces:**
- Consumes: `Sandbox.Run`, `Sandbox.PrepareRunDir`, `Credentials.token`, `ParseCloneURL`.
- Produces:
  ```go
  type Workspaces struct{ /* root string = <data>/agent-runs; sandbox commandRunner */ }
  type commandRunner interface { Run(ctx context.Context, spec SandboxSpec) (SandboxResult, error) } // fakeable
  func NewWorkspaces(root string, sandbox commandRunner) *Workspaces
  func (w *Workspaces) Dir(runID string) string                  // validates runID is 32 hex chars, else panics
  func (w *Workspaces) Clone(ctx context.Context, runID, cloneURL, baseBranch, token string) (baseSHA string, err error)
  func (w *Workspaces) Remove(runID string) error
  func (w *Workspaces) Sweep(now time.Time, keep func(runID string) (retainUntil time.Time, ok bool)) error
  func (w *Workspaces) Usage() (total int64, err error)
  func resolveInside(ws, rel string) (string, error)             // rejects absolute, "..", and symlinks resolving outside
  func CodingTools(w *Workspaces, run *ActiveRun, withBash bool) []ports.Tool  // list, read, search, edit, [bash], run_check
  type ActiveRun struct { ID string; Checks [][]string; CheckResults []CheckResult }
  type CheckResult struct { Argv []string; ExitCode int; TimedOut bool }
  ```
- Clone runs as root (it is a trusted operation), outside the sandbox:
  1. `git clone --no-tags --single-branch --branch <base> <cloneURL> <dir>/ws`,
     with `GIT_ASKPASS` pointing at a `0700` script in a fresh `os.MkdirTemp`
     that echoes `$EGGY_GIT_TOKEN`, and the token passed only in that git
     process's env as `EGGY_GIT_TOKEN`. Also set `GIT_TERMINAL_PROMPT=0` and
     `GIT_CONFIG_NOSYSTEM=1`.
  2. Remove the temp dir.
  3. Record `git rev-parse HEAD`.
  4. Run `git -C ws checkout -b eggy/<id>`.
  5. Set `core.hooksPath=/dev/null` in `ws/.git/config`.
  6. Chown the whole run dir to `coder` recursively.
- Tool schemas are JSON with `additionalProperties:false`:
  - `list{path}`
  - `read{path, offset?, limit?}` (returns at most 2,000 lines)
  - `search{pattern, path?}` (Go `regexp`, at most 200 matches)
  - `edit{path, old, new}` (exact unique replace; `old==""` creates the
    file and fails if it exists)
  - `bash{command}`
  - `run_check{index}`

  `bash` and `run_check` go through `commandRunner`. `run_check` appends a
  `CheckResult`; `bash` never does.

- [ ] **Step 1: Write failing tests** using a fake `commandRunner` and a temp dir:
  - `TestResolveInsideRejectsEscapes`: rejects `../x`, `/etc/passwd`, and a
    symlink `ws/l -> /etc`. Accepts `a/b.go`.
  - `TestEditExactUnique`: zero matches is an error, two matches is an error,
    and `old==""` on an existing file is an error.
  - `TestRunCheckRecordsAndBashDoesNot`.
  - `TestRunCheckRejectsOutOfRangeIndex`.
  - `TestDirRejectsNonHexRunID`.
  - `TestTwoRunsSeparateDirs` (Review Focus 5).
  - `TestCloneNeverPutsTokenInArgvOrConfig`: use a fake git on `PATH` that
    records its argv and env. Assert the token appears only in env as
    `EGGY_GIT_TOKEN`. Assert `ws/.git/config` doesn't contain it.
  - `TestSweepHonorsRetention`.
- [ ] **Step 2:** Run `go test ./internal/extensions/agentrunner/ -run 'Resolve|Edit|RunCheck|Dir|TwoRuns|Clone|Sweep'`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS; `make fmt vet test race build`.
- [ ] **Step 5:** Commit `feat(agentrunner): run workspaces and coding tools`.

### Task 6: Service, worker, coding turn, `agent_runner` tool, skills

**Files:**
- Create: `internal/extensions/agentrunner/service.go`, `service_test.go`, `worker.go`, `worker_test.go`, `tool.go`, `tool_test.go`, `prompt.go`, `skills/` (three `.md` files, with `LICENSE` copied from Superpowers v6.4.2)

**Interfaces:**
- Consumes: Tasks 2–5; `agent.NewSelectedLoop`, `agent.StaticTools`, `agent.ModelTarget`, `agent.ContextPolicy`, `agent.RunOptions`.
- Produces:
  ```go
  type Options struct {
      Store ports.AgentRunStore; Credentials *Credentials; Workspaces *Workspaces; GitHub *GitHub
      Repositories func() []config.RepositoryConfig // live view; Review Focus 1
      Models map[string]agent.ModelTarget; Model string; Skills map[string]bool
      Notify func(ctx context.Context, run ports.AgentRun, text string) error // delivers to OriginChannel/OriginDestination
      SandboxOK bool; SandboxReason string // from Sandbox.Probe in bootstrap
      Now func() time.Time; NewID func() string
  }
  type Service struct{ /* ... */ }
  func New(o Options) (*Service, error)
  type StartRequest struct { Repository, Task, Channel, Destination string }
  func (s *Service) Start(ctx context.Context, req StartRequest) (ports.AgentRun, error)   // trusted ingress only
  func (s *Service) Get(ctx context.Context, id string) (ports.AgentRun, error)
  func (s *Service) List(ctx context.Context) ([]ports.AgentRun, error)
  func (s *Service) Cancel(ctx context.Context, id string) (ports.AgentRun, error)
  func (s *Service) Answer(ctx context.Context, id, text string) (ports.AgentRun, error)
  func (s *Service) Repositories(ctx context.Context) ([]RepoStatus, error)
  func (s *Service) SetRepositoryEnabled(ctx context.Context, name string, enabled bool) error
  type RepoStatus struct { Name string; Enabled bool; PushAccess bool }
  func (s *Service) Run(ctx context.Context) error   // the worker loop; returns when ctx ends
  func NewTool(s *Service) ports.Tool                 // name "agent_runner"; actions status,list,cancel,answer
  func (s *Service) SandboxStatus() (ok bool, reason string)
  ```
- Rules the tests pin:
  - `Start` refuses each of the following with a specific message: a
    repository not in `Repositories()`; a repository not enabled for this
    account; no PAT; no push access; an account that already has a queued run.
  - `Start` resolves the base SHA lazily at clone time. It stores the brief,
    `BriefDigest` = sha256 hex of the brief, `PATGeneration`,
    `MandateExpires = now+24h`, and `Branch = "eggy/"+id`.
  - The worker claims one queued run at a time (one active run across the
    deployment). It clones on first run, builds the coding loop with
    `agent.StaticTools(CodingTools(...))`, and runs it with
    `AllowedTools: nil` (all six).
  - The system messages come from `prompt.go`: the coding instructions plus
    the enabled skills, embedded with `//go:embed skills/*.md`. The model is
    `Options.Model`, never the account's `/model`.
  - Budget: every model call adds `RunResult.Usage` total tokens to
    `UsedTokens`, and active wall time accrues only while the run is in
    `running`. At 2,000,000 tokens or 30 minutes the run becomes `failed`
    with `Failure` set to `budget exhausted` and the workspace is kept.
  - Questions: the coding tool set gets a seventh tool, `ask_owner{question}`.
    Calling it ends the loop, stores `Question`, sets `waiting_for_user`,
    frees the slot and calls `Notify`. `Answer` stores the text and requeues
    the run. On the next claim, the brief, the question and answer, and a
    workspace `git status`/`git diff --stat` summary are the input. A waiting
    run past `MandateExpires` becomes `cancelled`.
  - `Cancel` cancels the live context if the run is running, and sets
    `cancelled` otherwise.
  - On worker start, every `running` run becomes `interrupted` and is never
    relaunched automatically. Publication reconciliation is Task 7.
  - With `SandboxOK == false`, the coding tool set has no `bash`
    (`TestNoBashWithoutSandbox`).
  - The tool's `status` and `list` are `ports.ReadOnlyTool()`, `cancel` and
    `answer` are mutations, and it has **no `start` action**.
  - A `Get`, `Cancel` or `Answer` for another account's run returns
    `ErrAgentRunNotFound`.

- [ ] **Step 1: Write failing tests** with an in-memory fake store, a fake
  `commandRunner`, a fake `ports.Model` that replays scripted tool calls, and
  a fake `Notify`:
  - `TestStartRefusals` (table-driven, one row per refusal above).
  - `TestStartRecordsMandate`.
  - `TestWorkerRunsCodingTurnWithRunnerModelNotOwnerModel`: put a different
    `/model` on the context and assert the fake for `Options.Model` received
    the call.
  - `TestCodingTurnSeesOnlyCodingTools`: the definitions handed to the fake
    model are exactly `list, read, search, edit, bash, run_check, ask_owner`.
  - `TestDisabledSkillNotInPrompt`.
  - `TestBudgetTokensFailsRun` and `TestBudgetWallTimeFailsRun` (Review Focus 4: the fake model loops forever).
  - `TestAskOwnerReleasesSlotAndAnswerRequeues`.
  - `TestWaitingRunExpiresWithMandate`.
  - `TestRepositoryRemovedWhileQueuedFails` (Review Focus 1).
  - `TestStartupMarksRunningInterrupted`.
  - `TestToolHasNoStartAction`: the schema's action enum equals `["status","list","cancel","answer"]`.
  - `TestForeignRunNotFound`.
- [ ] **Step 2:** Run `go test ./internal/extensions/agentrunner/ -run 'Start|Worker|CodingTurn|Skill|Budget|AskOwner|Waiting|RepositoryRemoved|Startup|Tool|Foreign'`. Expected: FAIL.
- [ ] **Step 3:** Implement.
  - Skills: copy `brainstorming`, `writing-plans` and
    `verification-before-completion` from Superpowers 6.4.2
    (`~/.claude/plugins/cache/superpowers-marketplace/superpowers/6.4.2/skills/<name>/SKILL.md`).
    Edit them by hand so they never mention subagents, worktrees or
    TodoWrite, and note the source revision and licence at the top of each.
  - The worker selects on the `ctx` and a `wake chan struct{}` that `Start`
    and `Answer` signal (buffered, size 1). It also checks every 30 seconds
    for mandate expiry. That timer exists only while the extension is
    enabled, because the worker does.
- [ ] **Step 4:** PASS; `make fmt vet test race build`.
- [ ] **Step 5:** Commit `feat(agentrunner): run service, worker and coding turn`.

### Task 7: Publisher and reconciliation

**Files:**
- Create: `internal/extensions/agentrunner/publish.go`, `publish_test.go`

**Interfaces:**
- Consumes: `Workspaces`, `Credentials.token`, `GitHub`, `ports.AgentRunStore`, `ActiveRun.CheckResults`.
- Produces:
  ```go
  func (s *Service) publish(ctx context.Context, run ports.AgentRun, checks []CheckResult) (ports.AgentRun, error)
  func (s *Service) reconcile(ctx context.Context) error // called by Run before claiming work
  ```
- Steps:
  1. The run is set to `publishing`.
  2. The change set is `git -C ws diff --binary <BaseSHA>` plus untracked
     files (`git ls-files --others --exclude-standard`), read as root with
     the workspace's Git config ignored (`GIT_CONFIG_GLOBAL=/dev/null`,
     `GIT_CONFIG_NOSYSTEM=1`, `-c core.hooksPath=/dev/null`). Reject the
     whole change set if any path is under `.github/`, is a symlink or a
     special file, escapes the repository, or matches the account's PAT or
     any value from `config.Secrets.Values()`.
  3. Apply the change set to a fresh clone in `<runDir>/publish/` at
     `BaseSHA` with `git apply --binary`, so commits the agent made itself in
     `ws/` are ignored (Review Focus 3). No changes at all: the run ends
     `completed` with "no change" and no PR.
  4. Commit as `Identity.Login <Identity.NoReplyEmail>`. Message: the first
     line of the brief (at most 72 characters), a blank line, then
     `Eggy-Run: <id>`.
  5. Each stage is persisted before its effect: `committed`, then
     `push_intended`, then the push, then `pushed`, then `pr_intended`, then
     the PR, then `pr_opened`.
  6. Before the push, re-check: the mandate hasn't expired; `PATGeneration`
     matches the current generation; the branch is `eggy/<id>`; it isn't the
     base or a protected branch; and `BranchProtected` (an error fails
     closed). Push with the same `GIT_ASKPASS` method as clone, using
     `git push origin HEAD:refs/heads/eggy/<id>`, without `--force`.
  7. The PR is a draft if any check failed, timed out, or none ran. Body
     sections: Summary, Changes, Checks run (argv and exit code for each
     `CheckResult` only), Limitations, and `<!-- eggy-run:<id> -->`.
  8. `Notify` gets the PR URL, then the run is set to `delivered`.
- `reconcile`, for runs in `publishing`:
  - `push_intended`: compare the remote branch SHA with the local publish
    HEAD. If they match, advance to `pushed`. If the branch is missing,
    push again. If it holds other content, fail with "branch has unexpected
    content".
  - `pr_intended`: if `FindPRByMarker` finds the PR, record it. Otherwise
    create it.
  - `pr_opened` but not delivered: retry `Notify`.

- [ ] **Step 1: Write failing tests** with a local bare repository as the
  remote (`git init --bare` in `t.TempDir()`, `file://` URL) and an
  `httptest` GitHub:
  - `TestPublishOpensPRWithCheckEvidence`.
  - `TestPublishDraftWhenChecksFailOrMissing`.
  - `TestPublishNoChangeNoPR`.
  - `TestPublishRejectsGithubDir`.
  - `TestPublishRejectsSymlink`.
  - `TestPublishRejectsSecretInDiff`.
  - `TestPublishIgnoresAgentCommits` (Review Focus 3).
  - `TestPublishRefusesStaleGeneration`.
  - `TestPublishRefusesExpiredMandate`.
  - `TestPublishFailsClosedWhenProtectionUnknown`.
  - `TestPublishRevokedAccessFails` (Review Focus 2).
  - `TestReconcileAfterCrashAtEachStage` (table: crash after
    `push_intended`, after the push, after `pr_intended`, after the PR) —
    there is never a duplicate PR, and the URL is always recorded.
  - `TestNotifyRetryDoesNotRepublish`.
- [ ] **Step 2:** Run `go test ./internal/extensions/agentrunner/ -run 'Publish|Reconcile|NotifyRetry'`. Expected: FAIL.
- [ ] **Step 3:** Implement. Hook `publish` into the worker after the coding
  loop returns normally, and call `reconcile` at the top of `Run`.
- [ ] **Step 4:** PASS; `make fmt vet test race build`.
- [ ] **Step 5:** Commit `feat(agentrunner): validated publisher with crash reconciliation`.

### Task 8: Bootstrap wiring and `/code`

**Files:**
- Create: `internal/bootstrap/agentrunner.go`, `internal/bootstrap/agentrunner_test.go`, `internal/commands/code.go`, `internal/commands/code_test.go`
- Modify:
  - `internal/bootstrap/app.go`: construct after finance (around line 350),
    start the worker goroutine in the app's run lifecycle beside the other
    background loops, and set `webConfig.AgentRunner` the same nil-safe way
    as `webConfig.Finance` (line 521).
  - `internal/commands/commands.go`: add `AgentRunner CodeRunner` to
    `Options`, and `case "code": return s.codeCommand(ctx, args)` in
    `Execute`.
  - `internal/commands/help.go`: help text.
  - `AGENTS.md`.

**Interfaces:**
- Consumes: `agentrunner.New`, `NewTool`, `NewSandbox`, `Probe`, `LockDataDir`, `NewCredentials`, `NewGitHub`, `NewWorkspaces`.
- Produces:
  - `func newAgentRunner(cfg config.Config, secrets config.Secrets, store ports.AgentRunStore, records grants.Records, targets map[string]agent.ModelTarget, layout home.Layout, notify func(context.Context, ports.AgentRun, string) error) (*agentrunner.Service, []ports.Tool, error)`.
    It returns `nil, nil, nil` when the extension is disabled. When the
    sandbox probe fails, it logs a warning and passes `SandboxOK: false`
    and the probe error as `SandboxReason`.
  - `CodeRunner` interface in `internal/commands`:
    `Start`, `List`, `Cancel`, `Answer`, `Repositories` and
    `SetRepositoryEnabled`, with the same signatures as `Service`. Like the
    panel, `commands` declares its own interface and doesn't import the
    extension.
- `/code` grammar:
  - `/code repos`
  - `/code repos enable <name>`
  - `/code repos disable <name>`
  - `/code run <name> <task...>`
  - `/code status`
  - `/code cancel <id>`
  - `/code answer <id> <text...>`

  Anything else returns the usage text. `/code run` passes `Channel` and
  `Destination` from `destination.FromContext(ctx)`. It is accepted only when
  the destination is a verified private Telegram chat or the web panel;
  Discord gets "Agent Runner can't be started from Discord." With
  `AgentRunner == nil`, `/code` returns "Agent Runner is not enabled."
- The `agent_runner` tool is registered with `gate(...)`, like finance. Do
  not add it to `turns.ReadOnlyTools()`; a bootstrap test pins that it is
  absent there.

- [ ] **Step 1: Write failing tests:**
  - `TestAgentRunnerDisabledRegistersNothing`: no `agent_runner` tool, no
    coding tool names anywhere in the registry, and a nil service.
  - `TestAgentRunnerToolNotInUnpromptedTurns`.
  - `TestCodingToolsNeverInOwnerCatalog`: enabled, and the owner loop's
    `ToolNames` contains none of `list, read, search, edit, bash, run_check, ask_owner`.
  - `TestCodeCommandGrammar` (table).
  - `TestCodeRunRefusedFromDiscord`.
  - `TestCodeDisabledMessage`.
- [ ] **Step 2:** Run `go test ./internal/bootstrap/ ./internal/commands/ -run 'AgentRunner|Coding|Code'`. Expected: FAIL.
- [ ] **Step 3:** Implement. Revise `AGENTS.md` in this commit:
  - Replace the safety invariant "Eggy has no repository commit, push,
    pull-request, or merge capability…" with one that names the Agent Runner
    as the single exception. It may push only `eggy/<run-id>` and open one
    PR, authorized by a mandate from a trusted ingress; it never approves,
    merges, tags, deletes refs or force-pushes.
  - Add a short paragraph under the `/mode` bullet saying the mandate, not
    the mode, governs coding-turn tools, and why.
  - Record the coder-user plus Landlock sandbox and its fail-closed probe
    under "Invariants that cost a defect to learn" as a rule: `bash` is
    registered only after a passing probe.
- [ ] **Step 4:** PASS; `make fmt vet test race build`.
- [ ] **Step 5:** Commit `feat(agentrunner): bootstrap wiring and /code commands`.

### Task 9: Panel API

**Files:**
- Create: `internal/panel/agentrunner.go`, `internal/panel/agentrunner_test.go`
- Modify:
  - `internal/panel/web.go`: add `"agent_runner"` to `features` beside
    finance (line 278), mount routes only when `webConfig.AgentRunner != nil`,
    and add `"agent_runner"` to the config sections list (line 282).
  - `internal/panel/config_routes.go`: add `case "agent_runner"` in GET
    (around line 194) and POST (around line 338), calling
    `config.SetAgentRunner`.
  - The `webConfig` struct definition: add `AgentRunner AgentRunnerService`.

**Interfaces:**
- Produces: an `AgentRunnerService` interface in the panel, with the
  `Service` methods it calls plus `SetPAT`, `PATStatus`, `DisconnectPAT` and
  `SandboxStatus`. Bootstrap passes an adapter struct when it needs to
  combine `Service` and `Credentials`.
- Routes (all behind `guard`):
  - `GET /api/agent-runner/runs`
  - `POST /api/agent-runner/runs` (`{repository, task}`)
  - `GET /api/agent-runner/runs/{id}`
  - `POST /api/agent-runner/runs/{id}/cancel`
  - `POST /api/agent-runner/runs/{id}/answer` (`{text}`)
  - `GET /api/agent-runner/repos`
  - `POST /api/agent-runner/repos/{name}` (`{enabled}`)
  - `GET /api/agent-runner/pat`
  - `PUT /api/agent-runner/pat` (`{token}`)
  - `DELETE /api/agent-runner/pat`
  - `POST /api/config/repositories/{name}/checks` (`{checks}`; calls
    `config.SetRepositoryChecks`)

  Bodies are decoded with unknown fields refused, as `decodeFinanceBody`
  does.
- [ ] **Step 1: Write failing tests:**
  - `TestAgentRunnerRoutesAbsentWhenNil`: 404, and `features` has no `agent_runner`.
  - `TestModeEndpointNeverListsAgentRunner`.
  - `TestPATNeverInResponse`: every route's response body is checked for the token.
  - `TestForeignRunIs404`.
  - `TestStartBodyRejectsUnknownFields`: a body carrying `account_id` gets a 400.
  - `TestAgentRunnerConfigSectionRoundTrip`.
- [ ] **Step 2:** Run `go test ./internal/panel/ -run AgentRunner`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS; `make fmt vet test race build`.
- [ ] **Step 5:** Commit `feat(panel): agent runner API`.

### Task 10: Web UI

**Files:**
- Create: `website/src/AgentRunnerCard.tsx`, `website/src/AgentRunnerPage.tsx`, `website/src/lib/agent-runner.ts`, and tests beside them in the style of the existing finance tests
- Modify: `website/src/extensions.tsx` (add an entry with
  `feature: "agent_runner"` and label `Agent Runner`), `website/src/ConfigPage.tsx`
  (render `AgentRunnerCard` beside `FinanceCard`), `website/src/PersonalSettingsCard.tsx`
  (a GitHub PAT field: password input, Save, Disconnect, and status showing
  the login and per-repository access; never prefilled)

**Interfaces:**
- Consumes: the Task 9 routes.
- The card copies `FinanceSettingsView`'s split between a presentational view
  and a data hook. It shows the enabled switch, a model select (aliases from
  the existing models section), a toggle per skill from
  `AgentRunnerSkills`, per-repository checks editing, and the Extensions
  card copy from Global Constraints. When `SandboxStatus` is not ok, it shows
  `Sandbox unavailable: <reason>. Runs can edit and run checks but have no bash.`
- The page has:
  - a Start form (repository select limited to enabled repositories, a task
    textarea, the start copy from Global Constraints shown above the button);
  - a repository list with enable toggles;
  - a run list;
  - a run detail view showing state, question with an answer box, check
    results, PR link and a cancel button.

  Use the existing patterns for loading, error and session-expired states.

- [ ] **Step 1: Write failing tests** with the existing website test runner (see `website/package.json`):
  - The card's view renders "restart needed" exactly when the saved and
    running states differ, as the finance test does.
  - The page shows the start copy verbatim.
  - The PAT field never renders a saved value.
- [ ] **Step 2:** Run `cd website && bun test`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `bun test` and `bun run build` PASS; `make fmt vet test race build`.
- [ ] **Step 5:** Commit `feat(web): Agent Runner card, page and PAT setting`.

### Task 11: Railway verification and end-to-end run

**Files:**
- Modify: `TODO.md` (mark done, or list what remains)

- [ ] **Step 1:** If Docker is available, run `make smoke`. Otherwise report
  that Docker is unavailable; don't count it as a pass.
- [ ] **Step 2:** Deploy. In the Railway container (`railway ssh`), check that
  `id coder` exists and that `ls -ld /data /data/agent-runs` shows `0711`,
  root-owned. Confirm the Agent Runner card shows no "Sandbox unavailable"
  line.
- [ ] **Step 3:** End to end, with the owner:
  1. Set the model to an OpenRouter alias and set `checks` on one test
     repository.
  2. Connect a PAT.
  3. `/code repos enable <name>` from Telegram.
  4. `/code run <name> add a line to README`.
  5. Observe a PR on `eggy/<id>` delivered to Telegram, with no approval and
     no merge.
  6. Repeat from the web Start form.
  7. Answer a question run.
  8. Cancel a run.
- [ ] **Step 4:** Commit `docs: agent runner verified on Railway`.
