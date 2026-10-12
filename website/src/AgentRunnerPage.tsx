import { useCallback, useEffect, useState } from "react";
import {
  SessionExpiredError,
  addAgentRepository,
  answerAgentRun,
  cancelAgentRun,
  getAgentStatus,
  listAgentRepos,
  listAgentRuns,
  setAgentRepoEnabled,
  removeAgentRepository,
  setRepositoryChecks,
  startAgentRun,
  type AgentRepository,
  type AgentRun,
  type AgentStatus,
} from "./api";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { CloseIcon } from "./components/ui/icons";
import { FIELD, FIELD_LABEL, PRIMARY_BUTTON, SECONDARY_BUTTON } from "./components/ui/form";
import { Input } from "./components/ui/input";
import { Select } from "./components/ui/select";
import {
  AGENT_START_COPY,
  canCancel,
  parseChecks,
  sandboxNotice,
  stateLabel,
  MAX_CHECKS,
} from "./lib/agent-runner";

// AgentRunStartForm is the only way to start a run from the panel. The copy
// above the button is the spec's wording, so the owner reads what a run does
// before it is started.
export function AgentRunStartForm({
  repositories,
  repository,
  task,
  busy,
  error,
  onRepositoryChange,
  onTaskChange,
  onStart,
}: {
  repositories: AgentRepository[];
  repository: string;
  task: string;
  busy: boolean;
  error: string | null;
  onRepositoryChange: (name: string) => void;
  onTaskChange: (task: string) => void;
  onStart: () => void;
}) {
  const enabled = repositories.filter((repo) => repo.enabled);
  return (
    <form
      className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4"
      onSubmit={(event) => {
        event.preventDefault();
        onStart();
      }}
    >
      <label className="flex max-w-md flex-col gap-1.5 text-[11.5px] text-neutral-700">
        Repository
        <Select value={repository} onChange={(event) => onRepositoryChange(event.target.value)} aria-label="Repository">
          {enabled.length === 0 && <option value="">No repository enabled</option>}
          {enabled.map((repo) => (
            <option key={repo.name} value={repo.name}>
              {repo.name}
            </option>
          ))}
        </Select>
      </label>
      <label className="flex flex-col gap-1.5 text-[11.5px] text-neutral-700">
        What should change
        <textarea
          className={`${FIELD} min-h-24 rounded-xl px-3 py-2 text-sm`}
          value={task}
          onChange={(event) => onTaskChange(event.target.value)}
          aria-label="Task"
        />
      </label>
      <p className="text-sm text-neutral-700">{AGENT_START_COPY}</p>
      <div>
        <button type="submit" disabled={busy || !repository || task.trim() === ""} className={PRIMARY_BUTTON}>
          {busy ? "Starting..." : "Start run"}
        </button>
      </div>
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </form>
  );
}

function RunDetail({
  run,
  busy,
  onCancel,
  onAnswer,
}: {
  run: AgentRun;
  busy: boolean;
  onCancel: () => void;
  onAnswer: (text: string) => void;
}) {
  const [answer, setAnswer] = useState("");
  return (
    <section className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4" aria-label="Run">
      <p className="text-sm font-medium text-foreground">
        {run.repository} · {stateLabel(run.state)}
      </p>
      {run.question && run.state === "waiting_for_user" && (
        <form
          className="flex flex-col gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            onAnswer(answer);
            setAnswer("");
          }}
        >
          <p className="text-sm text-neutral-700">The agent asks: {run.question}</p>
          <Input className={FIELD} value={answer} onChange={(event) => setAnswer(event.target.value)} aria-label="Your answer" />
          <div>
            <button type="submit" disabled={busy || answer.trim() === ""} className={PRIMARY_BUTTON}>
              Send answer
            </button>
          </div>
        </form>
      )}
      {run.pr_url && (
        <p className="text-sm text-neutral-700">
          {run.draft ? "Draft pull request: " : "Pull request: "}
          <a href={run.pr_url} target="_blank" rel="noreferrer" className="underline">
            {run.pr_url}
          </a>
        </p>
      )}
      {run.failure && <p className="text-sm text-neutral-700">{run.failure}</p>}
      {run.output && <pre className="max-h-64 overflow-auto whitespace-pre-wrap text-xs text-neutral-700">{run.output}</pre>}
      {canCancel(run.state) && (
        <div>
          <button type="button" disabled={busy} onClick={onCancel} className={SECONDARY_BUTTON}>
            Cancel run
          </button>
        </div>
      )}
    </section>
  );
}

// AddRepositoryForm allows one more repository. Only a GitHub https URL is
// accepted; the server refuses anything else with the reason.
export function AddRepositoryForm({
  name,
  cloneURL,
  baseBranch,
  busy,
  error,
  onChange,
  onAdd,
}: {
  name: string;
  cloneURL: string;
  baseBranch: string;
  busy: boolean;
  error: string | null;
  onChange: (field: "name" | "cloneURL" | "baseBranch", value: string) => void;
  onAdd: () => void;
}) {
  return (
    <form
      className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4"
      onSubmit={(event) => {
        event.preventDefault();
        onAdd();
      }}
    >
      <p className={FIELD_LABEL}>Add a repository</p>
      <Input className={FIELD} value={name} onChange={(e) => onChange("name", e.target.value)} aria-label="Repository name" placeholder="name, e.g. widgets" />
      <Input className={FIELD} value={cloneURL} onChange={(e) => onChange("cloneURL", e.target.value)} aria-label="Clone URL" placeholder="https://github.com/owner/repo.git" />
      <Input className={FIELD} value={baseBranch} onChange={(e) => onChange("baseBranch", e.target.value)} aria-label="Base branch" placeholder="main" />
      <p className="text-xs text-neutral-700">
        Runs start after a restart. Then save your GitHub token again so Eggy checks access to the new repository.
      </p>
      <div>
        <button type="submit" disabled={busy || name.trim() === "" || cloneURL.trim() === ""} className={PRIMARY_BUTTON}>
          Add repository
        </button>
      </div>
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </form>
  );
}

function RepositoryRow({
  repo,
  onToggle,
  onSaveChecks,
  onRemove,
}: {
  repo: AgentRepository;
  onToggle: (on: boolean) => void;
  onSaveChecks: (text: string) => void;
  onRemove: () => void;
}) {
  // Prefilled from the saved checks: saving an editor that started empty would
  // silently clear them.
  const [checks, setChecks] = useState(() => (repo.checks ?? []).map((argv) => argv.join(" ")).join("\n"));
  const parsed = parseChecks(checks);
  return (
    <div className="flex flex-col gap-2 rounded-2xl bg-neutral-100 p-4">
      <label className="flex items-center gap-2 text-sm text-foreground">
        <input type="checkbox" checked={repo.enabled} onChange={(event) => onToggle(event.target.checked)} />
        {repo.name} {repo.push_access ? "" : "(no push access)"}
      </label>
      <label className="flex flex-col gap-1.5 text-[11.5px] text-neutral-700">
        Checks, one command per line (the only commands a run may run)
        <textarea className={`${FIELD} min-h-20 rounded-xl px-3 py-2 font-mono text-xs`} value={checks} onChange={(event) => setChecks(event.target.value)} aria-label={`Checks for ${repo.name}`} />
      </label>
      {parsed.length > MAX_CHECKS && <p className="text-xs text-neutral-700">At most {MAX_CHECKS} checks.</p>}
      <p className="text-xs text-neutral-700">Checks take effect after a restart.</p>
      <div className="flex gap-2">
        <button type="button" disabled={parsed.length > MAX_CHECKS} onClick={() => onSaveChecks(checks)} className={SECONDARY_BUTTON}>
          Save checks
        </button>
        <button type="button" onClick={onRemove} className={SECONDARY_BUTTON}>
          Remove repository
        </button>
      </div>
    </div>
  );
}

export function AgentRunnerPage({ onClose, onSessionExpired }: { onClose: () => void; onSessionExpired: () => void }) {
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [repos, setRepos] = useState<AgentRepository[]>([]);
  const [runs, setRuns] = useState<AgentRun[]>([]);
  const [selected, setSelected] = useState<AgentRun | null>(null);
  const [repository, setRepository] = useState("");
  const [task, setTask] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [newRepo, setNewRepo] = useState({ name: "", cloneURL: "", baseBranch: "" });
  const [addError, setAddError] = useState<string | null>(null);

  const fail = useCallback(
    (err: unknown) => {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Something went wrong");
    },
    [onSessionExpired],
  );

  const refresh = useCallback(() => {
    Promise.all([listAgentRuns(), listAgentRepos()])
      .then(([nextRuns, nextRepos]) => {
        setRuns(nextRuns);
        setRepos(nextRepos);
        setRepository((current) => current || nextRepos.find((repo) => repo.enabled)?.name || "");
      })
      .catch(fail);
  }, [fail]);

  useEffect(() => {
    getAgentStatus().then(setStatus).catch(fail);
    refresh();
    const timer = window.setInterval(refresh, 15000);
    return () => window.clearInterval(timer);
  }, [fail, refresh]);

  async function act(action: () => Promise<AgentRun>) {
    setBusy(true);
    setError(null);
    try {
      const run = await action();
      setSelected(run);
      refresh();
    } catch (err) {
      fail(err);
    } finally {
      setBusy(false);
    }
  }

  const notice = status ? sandboxNotice(status) : null;

  return (
    <div className="flex flex-col gap-5 p-6">
      <div className="flex items-start justify-between gap-3">
        <CardHeader title="Agent Runner" description="Eggy changes a repository you allow, runs its checks, and opens a pull request for a person to review." />
        <button type="button" onClick={onClose} aria-label="Close" className={SECONDARY_BUTTON}>
          <CloseIcon className="h-4 w-4" />
        </button>
      </div>
      {notice && <p className="rounded-2xl bg-neutral-100 px-3.5 py-2.5 text-sm text-neutral-700" role="status">{notice}</p>}
      <AgentRunStartForm
        repositories={repos}
        repository={repository}
        task={task}
        busy={busy}
        error={error}
        onRepositoryChange={setRepository}
        onTaskChange={setTask}
        onStart={() => act(() => startAgentRun({ repository, task: task.trim() }))}
      />
      <section className="flex flex-col gap-3">
        <h3 className={FIELD_LABEL}>Repositories</h3>
        <AddRepositoryForm
          name={newRepo.name}
          cloneURL={newRepo.cloneURL}
          baseBranch={newRepo.baseBranch}
          busy={busy}
          error={addError}
          onChange={(field, value) => setNewRepo({ ...newRepo, [field]: value })}
          onAdd={() =>
            addAgentRepository({ name: newRepo.name.trim(), clone_url: newRepo.cloneURL.trim(), base_branch: newRepo.baseBranch.trim() || "main" })
              .then(() => {
                setNewRepo({ name: "", cloneURL: "", baseBranch: "" });
                setAddError(null);
                refresh();
              })
              .catch((err) => setAddError(err instanceof Error ? err.message : "Could not add the repository"))
          }
        />
        {repos.map((repo) => (
          <RepositoryRow
            key={repo.name}
            repo={repo}
            onToggle={(on) => setAgentRepoEnabled(repo.name, on).then(setRepos).catch(fail)}
            onSaveChecks={(text) => setRepositoryChecks(repo.name, parseChecks(text)).catch(fail)}
            onRemove={() => removeAgentRepository(repo.name).then(refresh).catch(fail)}
          />
        ))}
      </section>
      <section className="flex flex-col gap-2">
        <h3 className={FIELD_LABEL}>Runs</h3>
        {runs.length === 0 && <p className="text-sm text-neutral-700">No runs yet.</p>}
        {runs.map((run) => (
          <button key={run.id} type="button" onClick={() => setSelected(run)} className="flex justify-between rounded-xl bg-neutral-100 px-3 py-2 text-left text-sm">
            <span>{run.repository}</span>
            <span className="text-neutral-700">{stateLabel(run.state)}</span>
          </button>
        ))}
      </section>
      {selected && (
        <RunDetail
          run={selected}
          busy={busy}
          onCancel={() => act(() => cancelAgentRun(selected.id))}
          onAnswer={(text) => act(() => answerAgentRun(selected.id, text))}
        />
      )}
    </div>
  );
}
