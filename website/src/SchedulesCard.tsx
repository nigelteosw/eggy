import { useCallback, useEffect, useState } from "react";
import { CommandResult, SessionExpiredError, cancelSchedule, listSchedules } from "./api";

// Column positions in the rows /api/schedules returns.
const ID = 0;
const KIND = 1;
const INSTRUCTION = 2;
const EXPRESSION = 3;
const NEXT_RUN = 4;

/**
 * Schedules are created by asking Eggy, not by filling in a form: "every
 * weekday at nine, check the deploy" is a better interface than a cron
 * field. What a conversation is bad at is seeing all of them at once and
 * removing the one that was a mistake, so this card is a list and a cancel
 * button rather than full CRUD.
 */
export function SchedulesCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [result, setResult] = useState<CommandResult | null>(null);
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
        setError(err instanceof Error ? err.message : "Failed to load");
      });
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleCancel(row: string[]) {
    if (!window.confirm(`Cancel this schedule?\n\n${row[INSTRUCTION]}`)) return;
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
      setError(err instanceof Error ? err.message : "Could not cancel schedule");
    } finally {
      setCancelling(null);
    }
  }

  const rows = result?.table_rows ?? [];

  return (
    <div className="mb-2">
      <div className="mx-0.5 mb-3">
        <h3 className="text-[15px] font-semibold tracking-tight">Schedules</h3>
        <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">
          Everything Eggy will do on a timer, soonest first. Ask it in chat to create one; cancel it here. The heartbeat
          is a separate mechanism and does not appear in this list.
        </p>
      </div>
      {rows.length === 0 ? (
        <p className="rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">Nothing is scheduled.</p>
      ) : (
        <div className="flex flex-col">
          {rows.map((row) => (
            <div
              key={row[ID]}
              className="grid grid-cols-1 gap-3 px-1 py-3.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]"
            >
              <span className="min-w-0">
                <span className="flex flex-wrap items-baseline gap-2">
                  <span className="break-words text-[14.5px] text-foreground">{row[INSTRUCTION]}</span>
                  <span className="whitespace-nowrap rounded-full px-2 py-0.5 text-[11px] tracking-wide text-neutral-700 shadow-[inset_0_0_0_1px_hsl(var(--neutral-300))]">
                    {row[KIND]}
                  </span>
                </span>
                <span className="mt-1 block break-words text-xs leading-relaxed text-neutral-700">{row[EXPRESSION]}</span>
              </span>
              <span className="flex flex-wrap items-center gap-1.5">
                {row[NEXT_RUN] && (
                  <span className="mr-1 whitespace-nowrap text-xs tabular-nums text-neutral-700">{row[NEXT_RUN]}</span>
                )}
                <button
                  type="button"
                  disabled={cancelling === row[ID]}
                  onClick={() => handleCancel(row)}
                  className="min-h-8 whitespace-nowrap rounded-lg px-2.5 text-[12.5px] text-eg-red-ink transition-colors hover:bg-eg-red-tint disabled:pointer-events-none disabled:opacity-50"
                >
                  {cancelling === row[ID] ? "Cancelling…" : "Cancel"}
                </button>
              </span>
            </div>
          ))}
        </div>
      )}
      {error && (
        <p className="mt-3 rounded-xl bg-eg-red-tint px-3 py-2 text-sm text-eg-red-ink" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
