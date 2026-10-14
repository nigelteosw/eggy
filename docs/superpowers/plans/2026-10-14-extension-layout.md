# Extension layout cleanup

Goal: make extension code (finance, agent runner) visibly separate from core
Eggy code. Pure moves/renames — no behaviour change, no new packages, no
exported internals. Go package boundaries stay exactly as AGENTS.md requires.

Commit straight to `main`, one commit per part. Use `git mv` for every move.

## Part 1 — Go: `ext_` filename prefix

Rename only; package names and contents are untouched.

```sh
cd /Users/nigel/Projects/Eggy/eggy
git mv internal/bootstrap/agentrunner.go               internal/bootstrap/ext_agentrunner.go
git mv internal/bootstrap/agentrunner_test.go          internal/bootstrap/ext_agentrunner_test.go
git mv internal/bootstrap/finance.go                   internal/bootstrap/ext_finance.go
git mv internal/bootstrap/finance_test.go              internal/bootstrap/ext_finance_test.go
git mv internal/bootstrap/finance_integration_test.go  internal/bootstrap/ext_finance_integration_test.go
git mv internal/config/agent_runner.go                 internal/config/ext_agentrunner.go
git mv internal/config/agent_runner_test.go            internal/config/ext_agentrunner_test.go
git mv internal/config/finance.go                      internal/config/ext_finance.go
git mv internal/config/finance_test.go                 internal/config/ext_finance_test.go
git mv internal/panel/agentrunner.go                   internal/panel/ext_agentrunner.go
git mv internal/panel/agentrunner_test.go              internal/panel/ext_agentrunner_test.go
git mv internal/panel/finance.go                       internal/panel/ext_finance.go
git mv internal/panel/finance_test.go                  internal/panel/ext_finance_test.go
git mv internal/panel/finance_search_test.go           internal/panel/ext_finance_search_test.go
git mv internal/ports/agentrunner.go                   internal/ports/ext_agentrunner.go
git mv internal/ports/finance.go                       internal/ports/ext_finance.go
git mv internal/storage/sqlite/agentrunner.go          internal/storage/sqlite/ext_agentrunner.go
git mv internal/storage/sqlite/agentrunner_test.go     internal/storage/sqlite/ext_agentrunner_test.go
git mv internal/storage/sqlite/finance.go              internal/storage/sqlite/ext_finance.go
git mv internal/storage/sqlite/finance_test.go         internal/storage/sqlite/ext_finance_test.go
git mv internal/storage/sqlite/finance_kinds_test.go   internal/storage/sqlite/ext_finance_kinds_test.go
git mv internal/storage/sqlite/finance_search_test.go  internal/storage/sqlite/ext_finance_search_test.go
git mv internal/commands/code.go                       internal/commands/ext_agentrunner.go
git mv internal/commands/code_test.go                  internal/commands/ext_agentrunner_test.go
```

Then `grep -rn 'agent_runner\.go\|agentrunner\.go\|finance\.go\|code\.go' --include='*.go' --include='*.md' internal AGENTS.md README.md TODO.md docs/src`
and fix any live reference (ignore `docs/superpowers/` and `.superpowers/`,
which are historical).

Verify: `make fmt vet test race build`. Commit:
`refactor: prefix extension files with ext_ in shared packages`.

## Part 2 — Web panel: `website/src/extensions/`

Target layout (`GitHubTokenField.tsx` stays in `src/` — core
`PersonalSettingsCard` uses it):

```
src/extensions/index.tsx                 (was src/extensions.tsx)
src/extensions/finance/FinancePage.tsx, FinanceCard.tsx, FinanceToolbar.tsx,
                       FinanceCategories.tsx, FinanceOverview.tsx,
                       finance.ts, finance-list.ts   (from src/lib/)
src/extensions/agent-runner/AgentRunnerPage.tsx, AgentRunnerCard.tsx,
                       AgentRunsView.tsx, agent-runner.ts (from src/lib/)
tests/extensions/finance/finance*.test.ts
tests/extensions/agent-runner/agent-runner*.test.ts, agent-runs*.test.ts
```

```sh
cd /Users/nigel/Projects/Eggy/eggy/website
mkdir -p src/extensions/finance src/extensions/agent-runner tests/extensions/finance tests/extensions/agent-runner
git mv src/extensions.tsx src/extensions/index.tsx
git mv src/Finance{Page,Card,Toolbar,Categories,Overview}.tsx src/extensions/finance/
git mv src/lib/finance.ts src/lib/finance-list.ts src/extensions/finance/
git mv src/AgentRunnerPage.tsx src/AgentRunnerCard.tsx src/AgentRunsView.tsx src/extensions/agent-runner/
git mv src/lib/agent-runner.ts src/extensions/agent-runner/
git mv tests/finance*.test.ts tests/extensions/finance/
git mv tests/agent-runner*.test.ts tests/agent-runs*.test.ts tests/extensions/agent-runner/
```

Import fixes (check multi-line `} from "..."` imports too):

- Moved components in `src/extensions/<x>/`: `"./components/` → `"../../components/`,
  `"./api"` → `"../../api"`, `"./useConfigSection"` → `"../../useConfigSection"`,
  `"./lib/utils"` → `"../../lib/utils"`, `"./lib/finance"` → `"./finance"`,
  `"./lib/finance-list"` → `"./finance-list"`, `"./lib/agent-runner"` → `"./agent-runner"`.
  Sibling imports (`"./FinanceCategories"`, `"./AgentRunsView"`) stay.
- Moved lib files (`finance.ts`, `finance-list.ts`, `agent-runner.ts`): `"../api"` → `"../../api"`.
- `src/extensions/index.tsx`: `"./FinancePage"` → `"./finance/FinancePage"`,
  `"./AgentRunnerPage"` → `"./agent-runner/AgentRunnerPage"`,
  `"./components/ui/icons"` → `"../components/ui/icons"`.
- `src/ConfigPage.tsx`: `"./FinanceCard"` → `"./extensions/finance/FinanceCard"`,
  `"./AgentRunnerCard"` → `"./extensions/agent-runner/AgentRunnerCard"`.
- `App.tsx`, `ThreadSidebar.tsx`, `tests/navigation.test.ts` import
  `"./extensions"` / `"../src/extensions"` — resolves to the directory index; leave.
- Moved tests: `"../src/` → `"../../../src/`, then point at the new paths
  (`src/lib/finance` → `src/extensions/finance/finance`, `src/FinancePage` →
  `src/extensions/finance/FinancePage`, etc.). `../src/GitHubTokenField` and
  `../src/extensions` become `../../../src/GitHubTokenField` / `../../../src/extensions`.
- `grep -rn 'readFileSync\|"src/' tests` — fix any test that reads a moved file by path.
- `grep -rn 'Finance\|AgentRun' tailwind.config.js` — if `content` globs list
  files explicitly, make sure `src/**` still covers them.

Verify: `bunx tsc --noEmit && bun test && bun run build` (all from `website/`),
then `make test` at the repo root. Commit:
`refactor(web): move extension UI under src/extensions/`.

## Part 3 — Record the convention

Add to AGENTS.md, in the feature-extension bullet under Boundaries, one
sentence: an extension's code in a shared package (`ports`, `config`,
`storage/sqlite`, `panel`, `bootstrap`, `commands`) lives in files named
`ext_<name>.go`, and its web panel code under `website/src/extensions/<name>/`.
Commit: `docs: record the extension file layout`.

## Out of scope

Moving Go stores or panel handlers into `internal/extensions/` — AGENTS.md
forbids the panel importing extensions and exporting internals to satisfy
directories.
