import { useCallback, useEffect, useState } from "react";
import { cn } from "./lib/utils";
import {
  SessionExpiredError,
  addAgentRepository,
  answerAgentRun,
  deleteAgentRun,
  cancelAgentRun,
  getAgentStatus,
  listAgentRepos,
  listAgentRunSteps,
  listAgentRuns,
  removeAgentRepository,
  setAgentRepoEnabled,
  setRepositoryChecks,
  startAgentRun,
  type AgentRepository,
  type AgentRun,
  type AgentRunStep,
  type AgentStatus,
} from "./api";
import { CardHeader } from "./components/ui/card-header";
import { CloseIcon } from "./components/ui/icons";
import { FIELD, PRIMARY_BUTTON, SECONDARY_BUTTON } from "./components/ui/form";
import { Input } from "./components/ui/input";
import { Select } from "./components/ui/select";
import { Switch } from "./components/ui/switch";
import {
  AGENT_START_COPY,
  MAX_CHECKS,
  parseChecks,
  sandboxNotice,
  waitingRuns,
  type RunFilter,
} from "./lib/agent-runner";
import { AgentRunDetailView, AgentRunRow, AgentRunsList, Badge, Section } from "./AgentRunsView";

// serverText joins a result's title and detail, so the page says what the
// server did, including that Eggy is restarting, in the server's own words.
function serverText(result: unknown): string {
  const { title, detail } = (result ?? {}) as {
    title?: string;
    detail?: string;
  };
  return [title, detail].filter(Boolean).join(" ") || "Done.";
}

type Message = { tone: "success" | "error" | "info"; text: string };

const TONE_CLASS: Record<Message["tone"], string> = {
  success: "bg-neutral-100 text-foreground",
  info: "bg-neutral-100 text-neutral-700",
  error: "bg-red-950/40 text-red-200",
};

// One message area for the whole page, so a result is always where the owner is
// already looking, not under whichever form produced it.
function Notice({
  message,
  onDismiss,
}: {
  message: Message;
  onDismiss?: () => void;
}) {
  return (
    <div
      role={message.tone === "error" ? "alert" : "status"}
      className={`flex items-start justify-between gap-3 rounded-2xl px-4 py-3 text-sm ${TONE_CLASS[message.tone]}`}
    >
      <span>{message.text}</span>
      {onDismiss && (
        <button
          type="button"
          onClick={onDismiss}
          aria-label="Dismiss"
          className="shrink-0 opacity-70 hover:opacity-100"
        >
          <CloseIcon className="h-4 w-4" />
        </button>
      )}
    </div>
  );
}

// AgentRunStartForm is the only way to start a run from the panel. The copy
// above the button is the spec's wording, so the owner reads what a run does
// before it is started. With no repository enabled it says what to do instead
// of offering an empty list.
export function AgentRunStartForm({
  repositories,
  repository,
  task,
  busy,
  onRepositoryChange,
  onTaskChange,
  onStart,
}: {
  repositories: AgentRepository[];
  repository: string;
  task: string;
  busy: boolean;
  onRepositoryChange: (name: string) => void;
  onTaskChange: (task: string) => void;
  onStart: () => void;
}) {
  const startable = repositories.filter(
    (repo) => repo.enabled && !repo.removed && !repo.pending,
  );
  if (startable.length === 0) {
    return (
      <p className="rounded-2xl bg-neutral-100 px-4 py-3 text-sm text-neutral-700">
        No repository is ready. Add one under Repositories, restart Eggy if it says so, then
        turn it on for your account.
      </p>
    );
  }
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
        <Select
          value={repository}
          onChange={(event) => onRepositoryChange(event.target.value)}
          aria-label="Repository"
        >
          {startable.map((repo) => (
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
          placeholder="Describe the change in a sentence or two"
        />
      </label>
      <p className="text-xs text-neutral-700">{AGENT_START_COPY}</p>
      <div>
        <button
          type="submit"
          disabled={busy || !repository || task.trim() === ""}
          className={PRIMARY_BUTTON}
        >
          {busy ? "Starting..." : "Start run"}
        </button>
      </div>
    </form>
  );
}

// AddRepositoryForm allows one more repository. Only a GitHub https URL is
// accepted; the server refuses anything else and the page shows its reason.
export function AddRepositoryForm({
  name,
  cloneURL,
  baseBranch,
  busy,
  onChange,
  onAdd,
  onCancel,
}: {
  name: string;
  cloneURL: string;
  baseBranch: string;
  busy: boolean;
  onChange: (field: "name" | "cloneURL" | "baseBranch", value: string) => void;
  onAdd: () => void;
  onCancel: () => void;
}) {
  return (
    <form
      className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4"
      onSubmit={(event) => {
        event.preventDefault();
        onAdd();
      }}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1.5 text-[11.5px] text-neutral-700">
          Name
          <Input
            className={FIELD}
            value={name}
            onChange={(e) => onChange("name", e.target.value)}
            aria-label="Repository name"
            placeholder="widgets"
          />
        </label>
        <label className="flex flex-col gap-1.5 text-[11.5px] text-neutral-700">
          Base branch
          <Input
            className={FIELD}
            value={baseBranch}
            onChange={(e) => onChange("baseBranch", e.target.value)}
            aria-label="Base branch"
            placeholder="main"
          />
        </label>
      </div>
      <label className="flex flex-col gap-1.5 text-[11.5px] text-neutral-700">
        GitHub URL
        <Input
          className={FIELD}
          value={cloneURL}
          onChange={(e) => onChange("cloneURL", e.target.value)}
          aria-label="Clone URL"
          placeholder="https://github.com/owner/repo.git"
        />
      </label>
      <p className="text-xs text-neutral-700">
        Eggy restarts to apply it, then checks your saved GitHub token against
        the repository on its own.
      </p>
      <div className="flex gap-2">
        <button
          type="submit"
          disabled={busy || name.trim() === "" || cloneURL.trim() === ""}
          className={PRIMARY_BUTTON}
        >
          Add repository
        </button>
        <button type="button" onClick={onCancel} className={SECONDARY_BUTTON}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function RepositoryCard({
  repo,
  busy,
  onToggle,
  onSaveChecks,
  onRemove,
}: {
  repo: AgentRepository;
  busy: boolean;
  onToggle: (on: boolean) => void;
  onSaveChecks: (checks: string[][]) => void;
  onRemove: () => void;
}) {
  // Prefilled from the saved checks: saving an editor that started empty would
  // silently clear them.
  const [checks, setChecks] = useState(() =>
    (repo.checks ?? []).map((argv) => argv.join(" ")).join("\n"),
  );
  const [confirming, setConfirming] = useState(false);
  const parsed = parseChecks(checks);
  const waiting = repo.pending || repo.removed;
  return (
    <div className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium text-foreground">{repo.name}</span>
        {repo.enabled && !waiting && <Badge tone="good">On</Badge>}
        {repo.pending && <Badge tone="warn">Starts after restart</Badge>}
        {repo.removed && <Badge tone="warn">Removed after restart</Badge>}
        {!waiting && !repo.push_access && (
          <Badge tone="warn">No push access</Badge>
        )}
        {!waiting && repo.needs_restart && (
          <Badge tone="warn">Checks change after restart</Badge>
        )}
      </div>
      <Switch
        checked={repo.enabled}
        disabled={waiting || busy}
        onCheckedChange={onToggle}
        label="Allow runs on this repository for my account"
      />
      <details className="group rounded-xl bg-neutral-200/30 px-3 py-2">
        <summary className="cursor-pointer text-sm text-neutral-700">
          Checks ({parsed.length}) · the only commands a run may run
        </summary>
        <div className="mt-3 flex flex-col gap-2">
          <textarea
            className={`${FIELD} min-h-20 rounded-xl px-3 py-2 font-mono text-xs`}
            value={checks}
            onChange={(event) => setChecks(event.target.value)}
            aria-label={`Checks for ${repo.name}`}
            placeholder={"go vet ./...\ngo test ./..."}
          />
          <p className="text-xs text-neutral-700">
            One command per line. Changes apply after Eggy restarts.
          </p>
          {parsed.length > MAX_CHECKS && (
            <p className="text-xs text-red-300">At most {MAX_CHECKS} checks.</p>
          )}
          <div>
            <button
              type="button"
              disabled={busy || parsed.length > MAX_CHECKS || repo.removed}
              onClick={() => onSaveChecks(parsed)}
              className={SECONDARY_BUTTON}
            >
              Save checks
            </button>
          </div>
        </div>
      </details>
      <div className="flex items-center gap-2">
        {confirming ? (
          <>
            <span className="text-sm text-neutral-700">
              Remove {repo.name}?
            </span>
            <button
              type="button"
              disabled={busy}
              onClick={onRemove}
              className={SECONDARY_BUTTON}
            >
              Yes, remove
            </button>
            <button
              type="button"
              onClick={() => setConfirming(false)}
              className={SECONDARY_BUTTON}
            >
              Keep it
            </button>
          </>
        ) : (
          !repo.removed && (
            <button
              type="button"
              onClick={() => setConfirming(true)}
              className="text-xs text-neutral-700 underline"
            >
              Remove repository
            </button>
          )
        )}
      </div>
    </div>
  );
}

type Tab = "start" | "runs";

// The page's two tabs. Setting up and starting a run is one; looking back at
// what runs did is the other. The tab shows in the page, not in the address,
// because the extension window has no address of its own.
const TABS: { id: Tab; label: string }[] = [
  { id: "start", label: "Agent Runner" },
  { id: "runs", label: "Previous runs" },
];

// How many of the newest runs the start tab previews before "See all".
const RECENT_RUNS = 3;

export function AgentRunnerPage({
  onClose,
  onSessionExpired,
}: {
  onClose: () => void;
  onSessionExpired: () => void;
}) {
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [repos, setRepos] = useState<AgentRepository[] | null>(null);
  const [runs, setRuns] = useState<AgentRun[]>([]);
  const [tab, setTab] = useState<Tab>("start");
  // The selected run is held by id so the 15-second refresh keeps it current.
  // The filter lives here too, so a run opened and closed keeps its filter.
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [filter, setFilter] = useState<RunFilter>("all");
  const selected = runs.find((run) => run.id === selectedId) ?? null;
  const [steps, setSteps] = useState<AgentRunStep[]>([]);
  const [repository, setRepository] = useState("");
  const [task, setTask] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<Message | null>(null);
  const [adding, setAdding] = useState(false);
  const [newRepo, setNewRepo] = useState({
    name: "",
    cloneURL: "",
    baseBranch: "",
  });

  const fail = useCallback(
    (err: unknown) => {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setMessage({
        tone: "error",
        text: err instanceof Error ? err.message : "Something went wrong.",
      });
    },
    [onSessionExpired],
  );

  const refresh = useCallback(() => {
    Promise.all([listAgentRuns(), listAgentRepos()])
      .then(([nextRuns, nextRepos]) => {
        setRuns(nextRuns);
        setRepos(nextRepos);
        setRepository((current) =>
          nextRepos.some((repo) => repo.name === current && repo.enabled)
            ? current
            : (nextRepos.find((repo) => repo.enabled && !repo.removed)?.name ??
              ""),
        );
      })
      .catch(fail);
  }, [fail]);

  useEffect(() => {
    getAgentStatus().then(setStatus).catch(fail);
    refresh();
    const timer = window.setInterval(refresh, 15000);
    return () => window.clearInterval(timer);
  }, [fail, refresh]);

  // A different run starts with an empty timeline, so the last run's steps are
  // never shown under this one.
  useEffect(() => {
    setSteps([]);
  }, [selectedId]);

  // The steps reload with the runs, so an open run's timeline keeps up with it.
  // They are kept in place while they reload: clearing them on every refresh
  // blanked the timeline every 15 seconds.
  useEffect(() => {
    if (tab !== "runs" || !selectedId) {
      return;
    }
    let current = true;
    listAgentRunSteps(selectedId)
      .then((next) => {
        if (current) setSteps(next);
      })
      .catch(fail);
    return () => {
      current = false;
    };
  }, [tab, selectedId, runs, fail]);

  // perform runs one action, reports its result in the message area, and
  // refreshes, so every change shows up the same way.
  async function perform(
    action: () => Promise<unknown>,
    success: string | ((result: unknown) => string),
  ) {
    setBusy(true);
    setMessage(null);
    try {
      const result = await action();
      setMessage({
        tone: "success",
        text: typeof success === "function" ? success(result) : success,
      });
      refresh();
      return result;
    } catch (err) {
      fail(err);
      return undefined;
    } finally {
      setBusy(false);
    }
  }

  function openRun(id: string) {
    setSelectedId(id);
    setTab("runs");
  }

  async function startRun() {
    const run = (await perform(
      () => startAgentRun({ repository, task: task.trim() }),
      "Run queued. It will report here when it finishes.",
    )) as AgentRun | undefined;
    if (run) {
      setTask("");
      openRun(run.id);
    }
  }

  async function addRepository() {
    const name = newRepo.name.trim();
    const done = await perform(
      () =>
        addAgentRepository({
          name,
          clone_url: newRepo.cloneURL.trim(),
          base_branch: newRepo.baseBranch.trim() || "main",
        }),
      serverText,
    );
    if (done !== undefined) {
      setNewRepo({ name: "", cloneURL: "", baseBranch: "" });
      setAdding(false);
    }
  }

  const notice = status ? sandboxNotice(status) : null;
  const needsRestart = (repos ?? []).some((repo) => repo.needs_restart);
  const waiting = waitingRuns(runs);

  return (
    // The scroll container is this page's own: the extension window gives a
    // fixed height, and without overflow here the content is clipped, not scrolled.
    <div className="app-canvas scrollbar-slim h-full min-h-0 overflow-y-auto">
      <div className="mx-auto flex w-full max-w-4xl flex-col gap-6 p-6">
        <div className="flex items-start justify-between gap-3">
          <CardHeader
            title="Agent Runner"
            description="Eggy changes a repository you allow, runs its checks, and opens a pull request for a person to review."
          />
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            className={SECONDARY_BUTTON}
          >
            <CloseIcon className="h-4 w-4" />
          </button>
        </div>

        {waiting.length > 0 && (
          <div
            role="status"
            className="flex flex-wrap items-center justify-between gap-3 rounded-2xl bg-amber-950/30 px-4 py-3 text-sm"
          >
            <span>
              {waiting.length === 1
                ? `${waiting[0].repository} is waiting for your answer.`
                : `${waiting.length} runs are waiting for your answer.`}
            </span>
            <button type="button" onClick={() => openRun(waiting[0].id)} className={SECONDARY_BUTTON}>
              Answer
            </button>
          </div>
        )}

        {message && (
          <Notice message={message} onDismiss={() => setMessage(null)} />
        )}
        {needsRestart && (
          <Notice
            message={{
              tone: "info",
              text: "Some changes apply after Eggy restarts. Repositories marked below are waiting for it.",
            }}
          />
        )}
        {notice && <Notice message={{ tone: "info", text: notice }} />}

        <div className="flex flex-wrap items-center gap-3">
          <div role="tablist" aria-label="Agent Runner" className="flex gap-1 rounded-xl bg-neutral-100 p-1">
            {TABS.map((item) => (
              <button
                key={item.id}
                type="button"
                role="tab"
                id={`agent-runner-tab-${item.id}`}
                aria-selected={tab === item.id}
                aria-controls="agent-runner-panel"
                onClick={() => setTab(item.id)}
                className={cn(
                  "min-h-9 rounded-lg px-3.5 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                  tab === item.id ? "bg-background font-medium text-foreground shadow-sm" : "text-neutral-700 hover:text-foreground",
                )}
              >
                {item.label}
              </button>
            ))}
          </div>
        </div>

        <div id="agent-runner-panel" role="tabpanel" aria-labelledby={`agent-runner-tab-${tab}`} className="flex flex-col gap-6">
          {tab === "start" && (
            <>
              <Section
                title="Start a run"
                hint="Describe a change. Eggy works in the background and opens a pull request."
              >
                <AgentRunStartForm
                  repositories={repos ?? []}
                  repository={repository}
                  task={task}
                  busy={busy}
                  onRepositoryChange={setRepository}
                  onTaskChange={setTask}
                  onStart={startRun}
                />
              </Section>

              <Section
                title="Recent runs"
                hint="The newest runs. Every run keeps its own page with the reason it stopped."
                action={
                  runs.length > 0 && (
                    <button type="button" onClick={() => setTab("runs")} className={SECONDARY_BUTTON}>
                      All runs ({runs.length})
                    </button>
                  )
                }
              >
                {runs.length === 0 ? (
                  <p className="text-sm text-neutral-700">No runs yet.</p>
                ) : (
                  <ul className="flex flex-col gap-2">
                    {runs.slice(0, RECENT_RUNS).map((run) => (
                      <AgentRunRow key={run.id} run={run} onOpen={openRun} />
                    ))}
                  </ul>
                )}
              </Section>

              <Section
                title="Repositories"
                hint="The repositories Eggy may change. Each one needs your GitHub token to have push access."
                action={
                  !adding && (
                    <button type="button" onClick={() => setAdding(true)} className={SECONDARY_BUTTON}>
                      Add repository
                    </button>
                  )
                }
              >
                {adding && (
                  <AddRepositoryForm
                    name={newRepo.name}
                    cloneURL={newRepo.cloneURL}
                    baseBranch={newRepo.baseBranch}
                    busy={busy}
                    onChange={(field, value) => setNewRepo({ ...newRepo, [field]: value })}
                    onAdd={addRepository}
                    onCancel={() => setAdding(false)}
                  />
                )}
                {repos !== null && repos.length === 0 && !adding && (
                  <p className="rounded-2xl bg-neutral-100 px-4 py-3 text-sm text-neutral-700">
                    No repositories yet. Add one to let Eggy work on it.
                  </p>
                )}
                {(repos ?? []).map((repo) => (
                  <RepositoryCard
                    key={repo.name}
                    repo={repo}
                    busy={busy}
                    onToggle={(on) =>
                      void perform(
                        () => setAgentRepoEnabled(repo.name, on),
                        on
                          ? `${repo.name} is on for your account.`
                          : `${repo.name} is off for your account.`,
                      )
                    }
                    onSaveChecks={(checks) =>
                      void perform(
                        () => setRepositoryChecks(repo.name, checks),
                        "Checks saved. They apply after Eggy restarts.",
                      )
                    }
                    onRemove={() =>
                      void perform(() => removeAgentRepository(repo.name), serverText)
                    }
                  />
                ))}
              </Section>
            </>
          )}

          {tab === "runs" && !selectedId && (
            <Section title="Previous runs" hint="Filter by what you need to know. Open a run for its full story.">
              <AgentRunsList runs={runs} filter={filter} onFilterChange={setFilter} onOpen={openRun} />
            </Section>
          )}

          {tab === "runs" && selectedId && !selected && (
            <Section title="Run" hint="This run is no longer listed.">
              <button type="button" onClick={() => setSelectedId(null)} className={SECONDARY_BUTTON}>
                All runs
              </button>
            </Section>
          )}

          {tab === "runs" && selected && (
            <AgentRunDetailView
              run={selected}
              steps={steps}
              busy={busy}
              onBack={() => setSelectedId(null)}
              onCancel={() =>
                void perform(
                  () => cancelAgentRun(selected.id),
                  "Cancelling the run.",
                )
              }
              onAnswer={(text) =>
                void perform(
                  () => answerAgentRun(selected.id, text),
                  "Answer sent.",
                )
              }
              onDelete={async () => {
                const done = await perform(
                  () => deleteAgentRun(selected.id),
                  "Run deleted.",
                );
                if (done !== undefined) setSelectedId(null);
              }}
            />
          )}
        </div>
      </div>
    </div>
  );
}
