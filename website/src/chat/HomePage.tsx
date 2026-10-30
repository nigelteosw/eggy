import { PageHeader } from "../components/ui/page-header";
import { useCallback, useEffect, useState } from "react";
import {
  SessionExpiredError,
  cancelSchedule,
  getAgent,
  getWatchList,
  listSchedules,
  listThreads,
  saveWatchList,
  type AgentSelection,
  type CommandResult,
} from "../lib/api";
import { ApprovalsCard } from "../settings/ApprovalsCard";
import { ID, INSTRUCTION, NEXT_RUN, NEXT_RUN_AT } from "../schedules/SchedulesCard";
import { relativeTime } from "../lib/time";
import { errorMessage } from "../lib/utils";
import { useConfirm } from "../components/ui/confirm";
import { TrashIcon } from "../components/ui/icons";
import { FIELD_COMPACT } from "../components/ui/form";
import { rewriteWatch, watchItems, type WatchItem } from "./watch-edit";

// The home page answers the three things a person wants when they open Eggy:
// what needs them, what Eggy is keeping an eye on, and what is coming up. Each
// is read from the state the panel already holds, so Home adds no mechanism of
// its own; it only brings those answers to the front.
//
// It also teaches a new owner what Eggy is for, through a short checklist that
// ticks itself off from real state and disappears once every item is done.

type Checklist = { chatted: boolean; watching: boolean; checkins: boolean };

const watchText = (result: CommandResult) => result.fields?.find((field) => field.label === "watch")?.value ?? "";

const HEADER_BUTTON = "text-xs font-medium text-neutral-700 underline-offset-2 hover:underline disabled:pointer-events-none disabled:opacity-50";
const TRASH_BUTTON =
  "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-neutral-700 transition-colors hover:bg-eg-red-tint hover:text-eg-red-ink disabled:pointer-events-none disabled:opacity-50";

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
  // The watch list is kept as the document it is, so an edit made here can be
  // written back without disturbing the lines Home does not show.
  const [watchDoc, setWatchDoc] = useState<string | null>(null);
  const [watchDraft, setWatchDraft] = useState<WatchItem[] | null>(null);
  const [savingWatch, setSavingWatch] = useState(false);
  const [schedules, setSchedules] = useState<string[][] | null>(null);
  const [editingSchedules, setEditingSchedules] = useState(false);
  const [cancelling, setCancelling] = useState<string | null>(null);
  const [threadCount, setThreadCount] = useState<number | null>(null);
  const [agent, setAgent] = useState<AgentSelection | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirm, confirmDialog] = useConfirm();

  const fail = useCallback(
    (fallback: string) => (err: unknown) => {
      if (err instanceof SessionExpiredError) onSessionExpired();
      else setError(errorMessage(err, fallback));
    },
    [onSessionExpired],
  );
  const loadSchedules = useCallback(
    () => listSchedules().then((result) => setSchedules(result.table_rows ?? [])).catch(fail("Some of Home could not load")),
    [fail],
  );

  useEffect(() => {
    const failed = fail("Some of Home could not load");
    listThreads().then((threads) => setThreadCount(threads.length)).catch(failed);
    getWatchList().then((result) => setWatchDoc(watchText(result))).catch(failed);
    loadSchedules();
    getAgent().then(setAgent).catch(failed);
  }, [fail, loadSchedules]);

  const watch = watchDoc === null ? null : watchItems(watchDoc);

  function editWatch(withNewItem: boolean) {
    setError(null);
    const items = watch ?? [];
    setWatchDraft(withNewItem || items.length === 0 ? [...items, { line: null, text: "" }] : items);
  }

  function changeWatchItem(index: number, text: string) {
    setWatchDraft((draft) => draft?.map((item, at) => (at === index ? { ...item, text } : item)) ?? null);
  }

  // Saving rewrites the document it was edited from. Eggy writes to the same
  // file between turns, so the save first checks nothing changed underneath it
  // rather than overwriting a check-in's notes with a stale copy.
  async function saveWatch() {
    if (watchDraft === null || watchDoc === null) return;
    setSavingWatch(true);
    setError(null);
    try {
      const current = watchText(await getWatchList());
      if (current !== watchDoc) {
        setWatchDoc(current);
        setWatchDraft(null);
        setError("Eggy updated the watch list while you were editing. It has been reloaded; make your change again.");
        return;
      }
      const next = rewriteWatch(watchDoc, watchDraft);
      await saveWatchList(next);
      setWatchDoc(next);
      setWatchDraft(null);
    } catch (err) {
      fail("Could not save the watch list")(err);
    } finally {
      setSavingWatch(false);
    }
  }

  async function removeSchedule(row: string[]) {
    if (!(await confirm({ title: "Cancel this schedule?", body: row[INSTRUCTION], confirmLabel: "Cancel schedule", destructive: true }))) return;
    setCancelling(row[ID]);
    setError(null);
    try {
      await cancelSchedule(row[ID]);
      await loadSchedules();
    } catch (err) {
      fail("Could not cancel schedule")(err);
    } finally {
      setCancelling(null);
    }
  }

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
              <ChecklistRow done={checklist.watching} label="Add something to keep an eye on" action="Add one" onAction={() => editWatch(true)} />
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
            {watchDraft === null ? (
              <button type="button" onClick={() => editWatch(false)} disabled={watch === null} className={HEADER_BUTTON}>
                Edit
              </button>
            ) : (
              <span className="flex items-baseline gap-4">
                <button type="button" onClick={() => setWatchDraft(null)} disabled={savingWatch} className={HEADER_BUTTON}>
                  Cancel
                </button>
                <button type="button" onClick={saveWatch} disabled={savingWatch} className={`${HEADER_BUTTON} text-foreground`}>
                  {savingWatch ? "Saving…" : "Save"}
                </button>
              </span>
            )}
          </div>
          {watchDraft !== null ? (
            // Editing shows every item, not the first five: an item past the
            // fold is as likely to be the one that is done.
            <ul className="flex flex-col">
              {watchDraft.map((item, index) => (
                <li key={item.line ?? `new-${index}`} className="flex items-center gap-2 py-1.5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]">
                  <input
                    aria-label={`Watch item ${index + 1}`}
                    value={item.text}
                    autoFocus={item.line === null && index === watchDraft.length - 1}
                    placeholder="Something for Eggy to keep an eye on"
                    onChange={(event) => changeWatchItem(index, event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") void saveWatch();
                      if (event.key === "Escape") setWatchDraft(null);
                    }}
                    disabled={savingWatch}
                    className={FIELD_COMPACT}
                  />
                  <button
                    type="button"
                    aria-label="Delete this item"
                    title="Delete"
                    disabled={savingWatch}
                    onClick={() => setWatchDraft((draft) => draft?.filter((_, at) => at !== index) ?? null)}
                    className={TRASH_BUTTON}
                  >
                    <TrashIcon />
                  </button>
                </li>
              ))}
              <li className="pt-2">
                <button
                  type="button"
                  disabled={savingWatch}
                  onClick={() => setWatchDraft((draft) => [...(draft ?? []), { line: null, text: "" }])}
                  className={HEADER_BUTTON}
                >
                  Add an item
                </button>
              </li>
            </ul>
          ) : watch === null ? (
            <p className="text-sm text-neutral-700">Loading…</p>
          ) : watch.length === 0 ? (
            <p className="rounded-2xl bg-neutral-100 px-4 py-5 text-sm text-neutral-700">
              Nothing yet. Tell Eggy what to keep an eye on, and it will check on it between your conversations.
            </p>
          ) : (
            <ul className="flex flex-col">
              {watch.slice(0, 5).map((item) => (
                <li key={item.line} className="py-2.5 text-sm shadow-[inset_0_1px_0_hsl(var(--neutral-200))]">
                  {item.text}
                </li>
              ))}
              {watch.length > 5 && <li className="pt-2.5 text-xs text-neutral-700">and {watch.length - 5} more</li>}
            </ul>
          )}
        </section>

        <section aria-labelledby="home-coming-up" className="flex flex-col gap-3">
          {confirmDialog}
          <div className="flex items-baseline justify-between gap-3">
            <h2 id="home-coming-up" className="text-sm font-semibold">Coming up</h2>
            <span className="flex items-baseline gap-4">
              {upcoming.length > 0 && (
                <button type="button" onClick={() => setEditingSchedules((editing) => !editing)} className={HEADER_BUTTON}>
                  {editingSchedules ? "Done" : "Edit"}
                </button>
              )}
              <button type="button" onClick={() => onNavigate("scheduled")} className={HEADER_BUTTON}>
                All scheduled
              </button>
            </span>
          </div>
          {schedules === null ? (
            <p className="text-sm text-neutral-700">Loading…</p>
          ) : upcoming.length === 0 ? (
            <p className="rounded-2xl bg-neutral-100 px-4 py-5 text-sm text-neutral-700">Nothing is scheduled.</p>
          ) : (
            <ul className="flex flex-col">
              {upcoming.map((row) => (
                <li key={row[ID]} className="flex items-center justify-between gap-4 py-2.5 text-sm shadow-[inset_0_1px_0_hsl(var(--neutral-200))]">
                  <span className="min-w-0 break-words">{row[INSTRUCTION]}</span>
                  <span className="flex shrink-0 items-center gap-2">
                    <span title={row[NEXT_RUN]} className="text-xs tabular-nums text-neutral-700">
                      {row[NEXT_RUN_AT] ? relativeTime(row[NEXT_RUN_AT], Date.now()) : row[NEXT_RUN]}
                    </span>
                    {editingSchedules && (
                      <button
                        type="button"
                        aria-label="Cancel this schedule"
                        title="Cancel schedule"
                        disabled={cancelling === row[ID]}
                        onClick={() => removeSchedule(row)}
                        className={TRASH_BUTTON}
                      >
                        <TrashIcon />
                      </button>
                    )}
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
