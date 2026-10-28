import { PageHeader } from "../components/ui/page-header";
import { useEffect, useState } from "react";
import { SessionExpiredError, getAgent, getWatchList, listSchedules, listThreads, type AgentSelection } from "../lib/api";
import { ApprovalsCard } from "../settings/ApprovalsCard";
import { INSTRUCTION, NEXT_RUN, NEXT_RUN_AT } from "../schedules/SchedulesCard";
import { relativeTime } from "../lib/time";
import { errorMessage } from "../lib/utils";

// The home page answers the three things a person wants when they open Eggy:
// what needs them, what Eggy is keeping an eye on, and what is coming up. Each
// is read from the state the panel already holds, so Home adds no mechanism of
// its own; it only brings those answers to the front.
//
// It also teaches a new owner what Eggy is for, through a short checklist that
// ticks itself off from real state and disappears once every item is done.

type Checklist = { chatted: boolean; watching: boolean; checkins: boolean };

// watchLines counts the items on the watch list. Blank lines and headings are
// structure, not items, so they are not counted.
export function watchLines(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "" && !line.startsWith("#"));
}

export function HomePage({
  onSessionExpired,
  onNavigate,
  onOpenSettings,
  onNewChat,
  onOpenSidebar,
}: {
  onOpenSidebar?: () => void;
  onSessionExpired: () => void;
  onNavigate: (view: "scheduled" | "chat") => void;
  onOpenSettings: () => void;
  onNewChat: () => void;
}) {
  const [watch, setWatch] = useState<string[] | null>(null);
  const [schedules, setSchedules] = useState<string[][] | null>(null);
  const [threadCount, setThreadCount] = useState<number | null>(null);
  const [agent, setAgent] = useState<AgentSelection | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const fail = (err: unknown) => {
      if (err instanceof SessionExpiredError) onSessionExpired();
      else setError(errorMessage(err, "Some of Home could not load"));
    };
    listThreads().then((threads) => setThreadCount(threads.length)).catch(fail);
    getWatchList()
      .then((result) => setWatch(watchLines(result.fields?.find((field) => field.label === "watch")?.value ?? "")))
      .catch(fail);
    listSchedules().then((result) => setSchedules(result.table_rows ?? [])).catch(fail);
    getAgent().then(setAgent).catch(fail);
  }, [onSessionExpired]);

  const checklist: Checklist = {
    chatted: (threadCount ?? 0) > 0,
    watching: (watch?.length ?? 0) > 0,
    checkins: agent?.heartbeat === true,
  };
  const checklistDone = checklist.chatted && checklist.watching && checklist.checkins;
  const loaded = watch !== null && schedules !== null && threadCount !== null && agent !== null;

  // The status line is the page in one sentence: it is what someone reads
  // before they decide whether anything here needs them.
  const upcoming = schedules?.slice(0, 3) ?? [];
  const status = loaded
    ? upcoming.length > 0
      ? `Next up: ${upcoming[0][INSTRUCTION]} ${upcoming[0][NEXT_RUN] ? `(${upcoming[0][NEXT_RUN]})` : ""}`.trim()
      : "Nothing is scheduled. Ask Eggy in chat to set something up."
    : "Loading…";

  return (
    <main className="app-canvas scrollbar-slim h-full min-h-0 min-w-0 overflow-y-auto">
      <div className="mx-auto flex max-w-[860px] flex-col gap-8 px-5 pb-11 pt-10 sm:px-7">
        <PageHeader title="Home" onOpenSidebar={onOpenSidebar} description={status} />

        {error && <p className="rounded-xl bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>}

        {!checklistDone && loaded && (
          <section aria-label="Getting started" className="flex flex-col gap-2 rounded-2xl bg-neutral-100 p-5">
            <h2 className="text-sm font-semibold">Getting started</h2>
            <ul className="flex flex-col gap-1">
              <ChecklistRow done={checklist.chatted} label="Ask Eggy something" action="Start a chat" onAction={onNewChat} />
              <ChecklistRow done={checklist.watching} label="Add something to keep an eye on" action="Open settings" onAction={onOpenSettings} />
              <ChecklistRow done={checklist.checkins} label="Turn on check-ins, so Eggy can tell you when something needs you" action="Open settings" onAction={onOpenSettings} />
            </ul>
          </section>
        )}

        <section aria-label="Needs you">
          <ApprovalsCard onSessionExpired={onSessionExpired} />
        </section>

        <section aria-labelledby="home-watching" className="flex flex-col gap-3">
          <div className="flex items-baseline justify-between gap-3">
            <h2 id="home-watching" className="text-sm font-semibold">Watching</h2>
            <button type="button" onClick={onOpenSettings} className="text-xs font-medium text-neutral-700 underline-offset-2 hover:underline">
              Edit
            </button>
          </div>
          {watch === null ? (
            <p className="text-sm text-neutral-700">Loading…</p>
          ) : watch.length === 0 ? (
            <p className="rounded-2xl bg-neutral-100 px-4 py-5 text-sm text-neutral-700">
              Nothing yet. Tell Eggy what to keep an eye on, and it will check on it between your conversations.
            </p>
          ) : (
            <ul className="flex flex-col">
              {watch.slice(0, 5).map((line, index) => (
                <li key={index} className="py-2.5 text-sm shadow-[inset_0_1px_0_hsl(var(--neutral-200))]">
                  {line.replace(/^[-*]\s*/, "")}
                </li>
              ))}
              {watch.length > 5 && <li className="pt-2.5 text-xs text-neutral-700">and {watch.length - 5} more</li>}
            </ul>
          )}
        </section>

        <section aria-labelledby="home-coming-up" className="flex flex-col gap-3">
          <div className="flex items-baseline justify-between gap-3">
            <h2 id="home-coming-up" className="text-sm font-semibold">Coming up</h2>
            <button type="button" onClick={() => onNavigate("scheduled")} className="text-xs font-medium text-neutral-700 underline-offset-2 hover:underline">
              All scheduled
            </button>
          </div>
          {schedules === null ? (
            <p className="text-sm text-neutral-700">Loading…</p>
          ) : upcoming.length === 0 ? (
            <p className="rounded-2xl bg-neutral-100 px-4 py-5 text-sm text-neutral-700">Nothing is scheduled.</p>
          ) : (
            <ul className="flex flex-col">
              {upcoming.map((row, index) => (
                <li key={index} className="flex items-baseline justify-between gap-4 py-2.5 text-sm shadow-[inset_0_1px_0_hsl(var(--neutral-200))]">
                  <span className="min-w-0 break-words">{row[INSTRUCTION]}</span>
                  <span title={row[NEXT_RUN]} className="shrink-0 text-xs tabular-nums text-neutral-700">
                    {row[NEXT_RUN_AT] ? relativeTime(row[NEXT_RUN_AT], Date.now()) : row[NEXT_RUN]}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </main>
  );
}

function ChecklistRow({ done, label, action, onAction }: { done: boolean; label: string; action: string; onAction: () => void }) {
  return (
    <li className="flex min-h-11 items-center gap-3 text-sm">
      <span
        aria-hidden="true"
        className={done ? "flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-primary text-[11px] text-primary-foreground" : "h-5 w-5 shrink-0 rounded-full border border-neutral-400"}
      >
        {done ? "✓" : ""}
      </span>
      <span className={done ? "min-w-0 flex-1 text-neutral-700 line-through" : "min-w-0 flex-1"}>{label}</span>
      {!done && (
        <button type="button" onClick={onAction} className="shrink-0 text-xs font-medium underline-offset-2 hover:underline">
          {action}
        </button>
      )}
    </li>
  );
}
