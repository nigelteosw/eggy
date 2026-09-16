import { useCallback, useEffect, useState } from "react";
import {
  CommandResult,
  SessionExpiredError,
  approveChatDecision,
  getApprovalMode,
  listApprovals,
  setApprovalMode,
} from "./api";
import { cn } from "./lib/utils";

// Column positions in the rows /api/approvals returns.
const ID = 0;
const ACTION = 1;
const SUMMARY = 2;
const STATE = 3;
const REQUESTED = 4;

// The mode routes report the state in a field rather than only in prose, so
// the picker reflects the server's answer instead of what the click assumed.
function readApprovalMode(result: CommandResult): string {
  return result.fields?.find((field) => field.label === "approval_mode")?.value ?? "";
}

const MODES = [
  { value: "strict", label: "Strict — ask before every tool call", note: "Every tool call asks first, reading included." },
  { value: "normal", label: "Normal — ask before anything that writes", note: "Reading runs freely; anything that writes asks first." },
  { value: "auto", label: "Auto — never ask", note: "Nothing asks — tool calls that change things run unapproved." },
];

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
  const [mode, setMode] = useState("");
  const [modeMessage, setModeMessage] = useState<string | null>(null);
  const [switching, setSwitching] = useState(false);

  const load = useCallback(() => {
    listApprovals()
      .then(setResult)
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to load");
      });
    getApprovalMode()
      .then((result) => {
        setMode(readApprovalMode(result));
        setModeMessage(result.title ?? null);
      })
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
        }
        // A mode that cannot be read is left blank rather than defaulting to
        // one. Showing "normal" would tell the owner writes are being reviewed
        // when nothing here knows that.
      });
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  async function chooseMode(next: string) {
    setSwitching(true);
    setError(null);
    try {
      const result = await setApprovalMode(next);
      setMode(readApprovalMode(result));
      setModeMessage(result.title ?? null);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Could not change the approval mode");
    } finally {
      setSwitching(false);
    }
  }

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
      setError(err instanceof Error ? err.message : "Could not record decision");
    } finally {
      setDeciding(null);
    }
  }

  const rows = result?.table_rows ?? [];

  return (
    <div className="flex flex-col gap-7">
      <div>
        <div className="mx-0.5 mb-3">
          <h3 className="text-[15px] font-semibold tracking-tight">Approval mode</h3>
          <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">
            The standing answer to "may I?". It applies to every surface at once — this panel, Telegram, and
            unprompted turns.
          </p>
        </div>
        <div role="radiogroup" aria-label="Approval mode" className="flex flex-col">
          {MODES.map((option) => {
            const selected = mode === option.value;
            return (
              <button
                key={option.value}
                type="button"
                role="radio"
                aria-checked={selected}
                disabled={switching || mode === ""}
                onClick={() => chooseMode(option.value)}
                className="flex w-full items-center gap-3 px-1 py-3.5 text-left shadow-[inset_0_1px_0_hsl(var(--neutral-200))] disabled:pointer-events-none disabled:opacity-50"
              >
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-medium">{option.label}</span>
                  <span className="mt-0.5 block text-xs leading-relaxed text-neutral-700">{option.note}</span>
                </span>
                <svg
                  width="17"
                  height="17"
                  viewBox="0 0 20 20"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  className={cn("shrink-0 text-accent-600", selected ? "opacity-100" : "opacity-0")}
                >
                  <path d="M4.5 10.5 8 14l7.5-8" />
                </svg>
              </button>
            );
          })}
        </div>
        <p className="mt-2 px-1 text-xs text-neutral-700">{modeMessage ?? "Reading the current mode…"}</p>
      </div>

      <div>
        <div className="mx-0.5 mb-3">
          <h3 className="text-[15px] font-semibold tracking-tight">Waiting on you</h3>
          <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">
            Protected actions waiting on you, oldest first. An approval past its window shows as expired: it still
            counts as pending until it is decided, which is why it appears here rather than vanishing.
          </p>
        </div>
        {rows.length === 0 ? (
          <p className="rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">
            Nothing is waiting on you.
          </p>
        ) : (
          <div className="flex flex-col">
            {rows.map((row) => (
              <div
                key={row[ID]}
                className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-5 px-1 py-3.5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]"
              >
                <span className="min-w-0">
                  <span className="flex flex-wrap items-baseline gap-2">
                    <span className="font-mono text-[14.5px] text-foreground">{row[ACTION]}</span>
                    <span className="whitespace-nowrap rounded-full px-2 py-0.5 text-[11px] tracking-wide text-neutral-700 shadow-[inset_0_0_0_1px_hsl(var(--neutral-300))]">
                      {row[STATE]}
                    </span>
                  </span>
                  <span className="mt-1 block text-xs leading-relaxed text-neutral-700">{row[SUMMARY]}</span>
                </span>
                <span className="flex items-center gap-1.5">
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
                    Reject
                  </button>
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      {error && (
        <p className="rounded-xl bg-eg-red-tint px-3 py-2 text-sm text-eg-red-ink" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
