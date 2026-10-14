import { useState, type ReactNode } from "react";
import type { AgentRun, AgentRunStep } from "./api";
import { FIELD, PRIMARY_BUTTON, SECONDARY_BUTTON } from "./components/ui/form";
import { Input } from "./components/ui/input";
import {
  RUN_FILTERS,
  canCancel,
  briefTitle,
  filterRuns,
  formatDuration,
  formatTokens,
  isActive,
  isModelStep,
  isStopped,
  isWaiting,
  publishProgress,
  runHeadline,
  runTone,
  stageLabel,
  stateLabel,
  timeAgo,
  type RunFilter,
} from "./lib/agent-runner";

export function Badge({ children, tone = "plain" }: { children: ReactNode; tone?: "plain" | "warn" | "good" }) {
  const toneClass =
    tone === "warn"
      ? "bg-amber-950/50 text-amber-200"
      : tone === "good"
        ? "bg-emerald-950/50 text-emerald-200"
        : "bg-neutral-200/60 text-neutral-700";
  return <span className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${toneClass}`}>{children}</span>;
}

export function Section({ title, hint, action, children }: { title: string; hint?: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-end justify-between gap-3">
        <div className="flex flex-col gap-0.5">
          <h3 className="text-sm font-medium text-foreground">{title}</h3>
          {hint && <p className="text-xs text-neutral-700">{hint}</p>}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

// One run as a row: what it is, what it did or why it stopped, and when. The
// row is the whole target, so a run is one tap away on a phone.
export function AgentRunRow({ run, onOpen }: { run: AgentRun; onOpen: (id: string) => void }) {
  return (
    <li>
      <button
        type="button"
        onClick={() => onOpen(run.id)}
        className="flex w-full flex-col gap-2 rounded-2xl bg-neutral-100 px-4 py-3 text-left transition-colors hover:bg-neutral-200/60 sm:flex-row sm:items-center sm:gap-4"
      >
        <span className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="flex flex-wrap items-center gap-2">
            <span className="truncate text-sm font-medium text-foreground">{run.repository}</span>
            <Badge tone={runTone(run.state)}>{stateLabel(run.state)}</Badge>
          </span>
          {briefTitle(run.brief) && <span className="truncate text-sm text-foreground">{briefTitle(run.brief)}</span>}
          <span className="line-clamp-2 text-sm text-neutral-700">{runHeadline(run)}</span>
        </span>
        <span className="flex shrink-0 flex-wrap gap-x-3 gap-y-0.5 text-[11.5px] text-neutral-700 sm:flex-col sm:items-end">
          <span>{timeAgo(run.created_at)}</span>
          {run.used_tokens > 0 && <span>{formatTokens(run.used_tokens)} tokens</span>}
        </span>
      </button>
    </li>
  );
}

// The runs, filtered. The filter is the owner's own question: what needs me,
// what is moving, what finished, what stopped. Counts show before opening a filter.
export function AgentRunsList({
  runs,
  filter,
  onFilterChange,
  onOpen,
}: {
  runs: AgentRun[];
  filter: RunFilter;
  onFilterChange: (filter: RunFilter) => void;
  onOpen: (id: string) => void;
}) {
  const shown = filterRuns(runs, filter);
  return (
    <div className="flex flex-col gap-3">
      <div role="group" aria-label="Filter runs" className="flex flex-wrap gap-1.5">
        {RUN_FILTERS.map((item) => {
          const count = filterRuns(runs, item.id).length;
          const selected = filter === item.id;
          return (
            <button
              key={item.id}
              type="button"
              aria-pressed={selected}
              onClick={() => onFilterChange(item.id)}
              className={`flex min-h-8 items-center gap-1.5 rounded-full px-3 text-xs transition-colors ${
                selected ? "bg-foreground text-background" : "bg-neutral-100 text-neutral-700 hover:text-foreground"
              }`}
            >
              {item.label}
              <span className={selected ? "opacity-80" : "text-neutral-500"}>{count}</span>
            </button>
          );
        })}
      </div>
      {shown.length === 0 ? (
        <p className="rounded-2xl bg-neutral-100 px-4 py-3 text-sm text-neutral-700">{emptyRunsCopy(runs.length, filter)}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {shown.map((run) => (
            <AgentRunRow key={run.id} run={run} onOpen={onOpen} />
          ))}
        </ul>
      )}
    </div>
  );
}

function emptyRunsCopy(total: number, filter: RunFilter): string {
  if (total === 0) return "No runs yet. Start one from the Agent Runner tab.";
  if (filter === "needs_you") return "Nothing is waiting for you.";
  if (filter === "active") return "Nothing is in progress.";
  if (filter === "stopped") return "No run has stopped.";
  if (filter === "completed") return "No run has completed yet.";
  return "No runs yet.";
}

// One run, in full. What the owner must act on comes first, then where the run
// stands, then what it did. Nothing here is a second source of truth; it is the
// record as stored.
export function AgentRunDetailView({
  run,
  steps = [],
  busy,
  onBack,
  onCancel,
  onAnswer,
}: {
  run: AgentRun;
  steps?: AgentRunStep[];
  busy: boolean;
  onBack: () => void;
  onCancel: () => void;
  onAnswer: (text: string) => void;
}) {
  const [answer, setAnswer] = useState("");
  const [showModel, setShowModel] = useState(false);
  const modelCalls = steps.filter((step) => isModelStep(step.kind)).length;
  const visibleSteps = showModel ? steps : steps.filter((step) => !isModelStep(step.kind));
  const showProgress = Boolean(run.stage) || run.state === "completed" || isActive(run.state);
  return (
    <div className="flex flex-col gap-5" aria-label="Run">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <button type="button" onClick={onBack} className={SECONDARY_BUTTON}>
          All runs
        </button>
        {canCancel(run.state) && (
          <button type="button" disabled={busy} onClick={onCancel} className={SECONDARY_BUTTON}>
            Cancel run
          </button>
        )}
      </div>

      <header className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="text-base font-medium text-foreground">{run.repository}</h3>
          <Badge tone={runTone(run.state)}>{stateLabel(run.state)}</Badge>
        </div>
        {run.brief && (
          <div className="flex flex-col gap-1 rounded-2xl bg-neutral-100 p-4">
            <h4 className="text-xs font-medium text-neutral-700">Task</h4>
            <p className="whitespace-pre-wrap break-words text-sm text-foreground">{run.brief}</p>
          </div>
        )}
        <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-4">
          <Fact label="Started" value={timeAgo(run.created_at)} title={new Date(run.created_at).toLocaleString()} />
          <Fact label="Working time" value={run.active_seconds > 0 ? formatDuration(run.active_seconds) : "—"} />
          <Fact label="Tokens" value={run.used_tokens > 0 ? formatTokens(run.used_tokens) : "—"} />
          <Fact label="Base branch" value={run.base_branch || "—"} />
        </dl>
        {run.branch && (
          <p className="text-xs text-neutral-700">
            Working branch <code className="rounded bg-neutral-200/60 px-1 py-0.5">{run.branch}</code>
          </p>
        )}
      </header>

      {isWaiting(run.state) && (
        <form
          className="flex flex-col gap-2 rounded-2xl bg-amber-950/30 p-4"
          onSubmit={(event) => {
            event.preventDefault();
            onAnswer(answer);
            setAnswer("");
          }}
        >
          <h4 className="text-sm font-medium text-foreground">The agent is asking you</h4>
          <p className="text-sm text-neutral-700">{run.question}</p>
          <Input className={FIELD} value={answer} onChange={(event) => setAnswer(event.target.value)} aria-label="Your answer" />
          <div>
            <button type="submit" disabled={busy || answer.trim() === ""} className={PRIMARY_BUTTON}>
              Send answer
            </button>
          </div>
        </form>
      )}

      {run.pr_url && (
        <div className="flex flex-col gap-1 rounded-2xl bg-neutral-100 p-4">
          <div className="flex flex-wrap items-center gap-2">
            <h4 className="text-sm font-medium text-foreground">{run.draft ? "Draft pull request" : "Pull request"}</h4>
            {run.draft && <Badge>Draft</Badge>}
          </div>
          {run.pr_title && <p className="text-sm text-foreground">{run.pr_title}</p>}
          <a href={run.pr_url} target="_blank" rel="noreferrer" className="break-all text-sm underline">
            {run.pr_url}
          </a>
        </div>
      )}

      {isStopped(run.state) && (
        <div className="flex flex-col gap-1 rounded-2xl bg-neutral-100 p-4">
          <h4 className="text-sm font-medium text-foreground">Why it stopped</h4>
          <p className="text-sm text-neutral-700">{run.failure || "No reason was recorded for this run."}</p>
          {run.stage && <p className="text-xs text-neutral-700">Stopped at: {stageLabel(run.stage)}</p>}
        </div>
      )}

      {showProgress && (
        <div className="flex flex-col gap-2 rounded-2xl bg-neutral-100 p-4">
          <h4 className="text-sm font-medium text-foreground">Progress</h4>
          <ol className="flex flex-wrap gap-x-5 gap-y-1.5">
            {publishProgress(run.stage ?? "").map((checkpoint) => (
              <li key={checkpoint.label} className={`flex items-center gap-1.5 text-sm ${checkpoint.done ? "text-foreground" : "text-neutral-500"}`}>
                <span aria-hidden="true">{checkpoint.done ? "✓" : "○"}</span>
                {checkpoint.label}
              </li>
            ))}
          </ol>
        </div>
      )}

      <div className="flex flex-col gap-2 rounded-2xl bg-neutral-100 p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-sm font-medium text-foreground">What happened</h4>
          {modelCalls > 0 && (
            <button type="button" onClick={() => setShowModel((on) => !on)} className="text-xs text-neutral-700 underline">
              {showModel ? "Hide model calls" : `Show ${modelCalls} model call${modelCalls === 1 ? "" : "s"}`}
            </button>
          )}
        </div>
        {steps.length === 0 && <p className="text-sm text-neutral-700">No steps recorded yet.</p>}
        {steps.length > 0 && visibleSteps.length === 0 && (
          <p className="text-sm text-neutral-700">Only model calls so far. Show them to see each one.</p>
        )}
        {visibleSteps.length > 0 && (
          <ol className="flex flex-col gap-2">
            {visibleSteps.map((step) => (
              <li key={step.seq} className="flex flex-col gap-0.5 border-l border-neutral-300 pl-3">
                <span className="text-[11px] text-neutral-700">
                  {new Date(step.at).toLocaleTimeString()} · {step.kind}
                </span>
                <span className="whitespace-pre-wrap break-words text-sm text-neutral-700">{step.text}</span>
              </li>
            ))}
          </ol>
        )}
      </div>

      {run.output && (
        <details className="rounded-2xl bg-neutral-100 p-4">
          <summary className="cursor-pointer text-sm font-medium text-foreground">What the run produced</summary>
          <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap text-xs text-neutral-700">{run.output}</pre>
        </details>
      )}
    </div>
  );
}

function Fact({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-[11.5px] text-neutral-700">{label}</dt>
      <dd className="text-sm text-foreground" title={title}>
        {value}
      </dd>
    </div>
  );
}
