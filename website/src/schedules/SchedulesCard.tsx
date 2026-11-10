import { relativeTime } from "../lib/time";
import { useCallback, useEffect, useState } from "react";
import { useConfirm } from "../components/ui/confirm";
import { CommandResult, SessionExpiredError, cancelSchedule, listSchedules } from "../lib/api";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { errorMessage } from "../lib/utils";
import { EmptyNote } from "../components/ui/voice";

// Column positions in the rows /api/schedules returns.
export const ID = 0;
const KIND = 1;
export const INSTRUCTION = 2;
const EXPRESSION = 3;
export const NEXT_RUN = 4;
// The exact next run, in RFC 3339, so the row can say "in 2 hours" and keep the
// moment on hover.
export const NEXT_RUN_AT = 5;
// When, in words -- "Every weekday at 9:00" -- the sentence Telegram uses.
export const WHEN = 6;

/**
 * Schedules are created by asking Eggy, not by filling in a form: "every
 * weekday at nine, check the deploy" is a better interface than a cron
 * field. What a conversation is bad at is seeing all of them at once and
 * removing the one that was a mistake, so this card is a list and a cancel
 * button rather than full CRUD.
 */
export function SchedulesCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [confirm, confirmDialog] = useConfirm();
  const [error, setError] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState<string | null>(null);

  const load = useCallback(() => {
    listSchedules()
      .then(setResult)
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(errorMessage(err, "Failed to load"));
      });
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleCancel(row: string[]) {
    if (!(await confirm({ title: "Cancel this schedule?", body: row[INSTRUCTION], confirmLabel: "Cancel schedule", destructive: true }))) return;
    setCancelling(row[ID]);
    setError(null);
    try {
      await cancelSchedule(row[ID]);
      load();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Could not cancel schedule"));
    } finally {
      setCancelling(null);
    }
  }

  const rows = result?.table_rows ?? [];

  return (
    <div className="mb-2">
      {confirmDialog}
      <CardHeader
        title="Schedules"
        className="mb-3"
        description={
          <>
            Everything Eggy will do on a timer, soonest first. Ask in chat to create one; cancel it here.
          </>
        }
      />
      {rows.length === 0 ? (
        <EmptyNote className="text-center">Nothing is scheduled.</EmptyNote>
      ) : (
        <div className="flex flex-col">
          {rows.map((row) => (
            <div
              key={row[ID]}
              className="grid grid-cols-1 gap-3 px-1 py-3.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]"
            >
              <span className="min-w-0">
                <span className="flex flex-wrap items-baseline gap-2">
                  <span className="break-words text-body text-foreground">{row[INSTRUCTION]}</span>
                  <span className="whitespace-nowrap rounded-full px-2 py-0.5 text-caption tracking-wide text-neutral-700 shadow-[inset_0_0_0_1px_hsl(var(--neutral-300))]">
                    {row[KIND]}
                  </span>
                </span>
                <span title={row[EXPRESSION]} className="mt-1 block break-words text-caption leading-relaxed text-neutral-700">
                  {row[WHEN] || row[EXPRESSION]}
                </span>
              </span>
              <span className="flex flex-wrap items-center gap-1.5">
                {row[NEXT_RUN] && (
                  <span title={row[NEXT_RUN]} className="mr-1 whitespace-nowrap text-caption tabular-nums text-neutral-700">
                    {row[NEXT_RUN_AT] ? relativeTime(row[NEXT_RUN_AT], Date.now()) : row[NEXT_RUN]}
                  </span>
                )}
                <button
                  type="button"
                  disabled={cancelling === row[ID]}
                  onClick={() => handleCancel(row)}
                  className="min-h-8 whitespace-nowrap rounded-control px-2.5 text-small text-tomato transition-colors hover:bg-tomato-soft disabled:pointer-events-none disabled:opacity-50"
                >
                  {cancelling === row[ID] ? "Cancelling…" : "Cancel"}
                </button>
              </span>
            </div>
          ))}
        </div>
      )}
      {error && (
        <ErrorBanner className="mt-3">
          {error}
        </ErrorBanner>
      )}
    </div>
  );
}
