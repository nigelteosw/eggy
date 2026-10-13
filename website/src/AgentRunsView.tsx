import { useState, type ReactNode } from "react";
import type { AgentRun, AgentRunStep } from "./api";
import { FIELD, PRIMARY_BUTTON, SECONDARY_BUTTON } from "./components/ui/form";
import { Input } from "./components/ui/input";
import { canCancel, runHeadline, runTone, stageLabel, stateLabel } from "./lib/agent-runner";

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

// The runs as cards, three to a row where the window has the width. Each card
// leads with the run's issue, so the grid answers "what went wrong" without a click.
export function AgentRunsGrid({ runs, onOpen, onBack }: { runs: AgentRun[]; onOpen: (id: string) => void; onBack: () => void }) {
  return (
    <Section
      title="Runs"
      hint="Each card shows what a run is doing or why it stopped. Open one for the full story."
      action={
        <button type="button" onClick={onBack} className={SECONDARY_BUTTON}>
          Back to Agent Runner
        </button>
      }
    >
      {runs.length === 0 && <p className="text-sm text-neutral-700">No runs yet.</p>}
      <ul className="grid grid-cols-1 gap-3 md:grid-cols-3">
        {runs.map((run) => (
          <li key={run.id}>
            <button
              type="button"
              onClick={() => onOpen(run.id)}
              className="flex h-full w-full flex-col gap-2 rounded-2xl bg-neutral-100 p-4 text-left"
            >
              <span className="flex items-center justify-between gap-2">
                <span className="truncate text-sm font-medium text-foreground">{run.repository}</span>
                <Badge tone={runTone(run.state)}>{stateLabel(run.state)}</Badge>
              </span>
              <span className="text-[11px] text-neutral-700">{new Date(run.created_at).toLocaleString()}</span>
              <span className="line-clamp-3 text-sm text-neutral-700">{runHeadline(run)}</span>
            </button>
          </li>
        ))}
      </ul>
    </Section>
  );
}

// One run, in full: the reason first, then where it stopped, then whatever the
// run produced. Nothing here is a second source of truth; it is the record as stored.
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
  const stopped = run.state === "failed" || run.state === "cancelled" || run.state === "interrupted";
  return (
    <div className="flex flex-col gap-4" aria-label="Run">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <button type="button" onClick={onBack} className={SECONDARY_BUTTON}>
          All runs
        </button>
        <span className="text-xs text-neutral-700">{new Date(run.created_at).toLocaleString()}</span>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-base font-medium text-foreground">{run.repository}</span>
        <Badge tone={runTone(run.state)}>{stateLabel(run.state)}</Badge>
      </div>

      {stopped && (
        <div className="flex flex-col gap-1 rounded-2xl bg-neutral-100 p-4">
          <h4 className="text-sm font-medium text-foreground">Why it stopped</h4>
          <p className="text-sm text-neutral-700">{run.failure || "No reason was recorded for this run."}</p>
          {run.stage && <p className="text-xs text-neutral-700">Stopped at: {stageLabel(run.stage)}</p>}
        </div>
      )}

      {run.state === "waiting_for_user" && (
        <form
          className="flex flex-col gap-2 rounded-2xl bg-neutral-100 p-4"
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
        <p className="text-sm text-neutral-700">
          {run.draft ? "Draft pull request: " : "Pull request: "}
          <a href={run.pr_url} target="_blank" rel="noreferrer" className="underline">
            {run.pr_url}
          </a>
        </p>
      )}

      <div className="flex flex-col gap-2 rounded-2xl bg-neutral-100 p-4">
        <h4 className="text-sm font-medium text-foreground">What happened</h4>
        {steps.length === 0 && <p className="text-sm text-neutral-700">No steps recorded yet.</p>}
        {steps.length > 0 && (
          <ol className="flex flex-col gap-2">
            {steps.map((step) => (
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
        <div className="flex flex-col gap-1 rounded-2xl bg-neutral-100 p-4">
          <h4 className="text-sm font-medium text-foreground">What the run produced</h4>
          <pre className="max-h-96 overflow-auto whitespace-pre-wrap text-xs text-neutral-700">{run.output}</pre>
        </div>
      )}

      {canCancel(run.state) && (
        <div>
          <button type="button" disabled={busy} onClick={onCancel} className={SECONDARY_BUTTON}>
            Cancel run
          </button>
        </div>
      )}
    </div>
  );
}
