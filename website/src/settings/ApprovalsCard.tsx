import { useCallback, useEffect, useState } from "react";
import {
  CommandResult,
  SessionExpiredError,
  approveChatDecision,
  listApprovals,
} from "../lib/api";
import { errorMessage } from "../lib/utils";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";

// Column positions in the rows /api/approvals returns.
const ID = 0;
const ACTION = 1;
const SUMMARY = 2;
const STATE = 3;
const REQUESTED = 4;

/**
 * Approvals were countable and not inspectable: status reports "1 approval
 * waiting" and nothing could say what it was. Deciding one still goes through
 * the same route a chat tap uses, so this card is the missing view rather than
 * a second way to approve something.
 */
export function ApprovalsCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [deciding, setDeciding] = useState<string | null>(null);

  const load = useCallback(() => {
    listApprovals()
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

  async function decide(row: string[], approved: boolean) {
    setDeciding(row[ID]);
    setError(null);
    try {
      await approveChatDecision(row[ID], approved);
      // The decision is enqueued rather than applied inline, so reload
      // instead of assuming the row is gone.
      load();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Could not record decision"));
    } finally {
      setDeciding(null);
    }
  }

  const rows = result?.table_rows ?? [];

  return (
    <div className="flex flex-col gap-7">
      <div>
        <CardHeader
          title="Waiting on you"
          className="mb-3"
          description={
            <>
              Protected actions waiting on you, oldest first. An approval past its window shows as expired: it still
              counts as pending until it is decided, which is why it appears here rather than vanishing.
            </>
          }
        />
        {rows.length === 0 ? (
          <p className="rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">
            Nothing is waiting on you.
          </p>
        ) : (
          <div className="flex flex-col">
            {rows.map((row) => (
              <div
                key={row[ID]}
                className="grid grid-cols-1 gap-3 px-1 py-3.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]"
              >
                <span className="min-w-0">
                  <span className="flex flex-wrap items-baseline gap-2">
                    <span className="font-mono text-[14.5px] text-foreground [overflow-wrap:anywhere]">{row[ACTION]}</span>
                    <span className="whitespace-nowrap rounded-full px-2 py-0.5 text-[11px] tracking-wide text-neutral-700 shadow-[inset_0_0_0_1px_hsl(var(--neutral-300))]">
                      {row[STATE]}
                    </span>
                  </span>
                  <span className="mt-1 block break-words text-xs leading-relaxed text-neutral-700">{row[SUMMARY]}</span>
                </span>
                <span className="flex flex-wrap items-center gap-1.5">
                  {row[REQUESTED] && (
                    <span className="mr-1 whitespace-nowrap text-xs tabular-nums text-neutral-700">{row[REQUESTED]}</span>
                  )}
                  <button
                    type="button"
                    disabled={deciding === row[ID] || row[STATE] === "expired"}
                    onClick={() => decide(row, true)}
                    className="min-h-8 whitespace-nowrap rounded-lg px-2.5 text-[12.5px] text-foreground transition-colors hover:bg-neutral-100 disabled:pointer-events-none disabled:opacity-50"
                  >
                    Approve
                  </button>
                  <button
                    type="button"
                    disabled={deciding === row[ID]}
                    onClick={() => decide(row, false)}
                    className="min-h-8 whitespace-nowrap rounded-lg px-2.5 text-[12.5px] text-eg-red-ink transition-colors hover:bg-eg-red-tint disabled:pointer-events-none disabled:opacity-50"
                  >
                    Decline
                  </button>
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      {error && (
        <ErrorBanner>
          {error}
        </ErrorBanner>
      )}
    </div>
  );
}
