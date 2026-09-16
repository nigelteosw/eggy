import { useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Input } from "./components/ui/input";

export function HeartbeatCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("heartbeat", onSessionExpired);
  const [tickInterval, setTickInterval] = useState("");
  const [instruction, setInstruction] = useState("");
  const [activeStart, setActiveStart] = useState("");
  const [activeEnd, setActiveEnd] = useState("");

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({ interval: tickInterval, instruction, active_start: activeStart, active_end: activeEnd });
    setInstruction("");
  }

  const headers = result?.table_headers ?? [];
  const row = result?.table_rows?.[0];
  const fieldClass =
    "h-[42px] w-full rounded-xl border border-neutral-200 bg-background px-3.5 text-[13px] text-foreground caret-accent-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30";

  return (
    <div className="mb-2">
      <div className="mx-0.5 mb-3">
        <h3 className="text-[15px] font-semibold tracking-tight">Heartbeat</h3>
        <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">
          A periodic check-in that messages you only when something needs attention.
        </p>
      </div>

      {!row ? (
        <p className="rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">Heartbeat is off.</p>
      ) : (
        <div className="mb-3 flex flex-col">
          {headers.map((label, i) => (
            <div
              key={label}
              className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-1 py-3.5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]"
            >
              <span className="min-w-0 text-sm">{label}</span>
              <span className="text-right text-[13.5px] tabular-nums text-neutral-700">{row[i]}</span>
            </div>
          ))}
        </div>
      )}

      <details className="rounded-2xl bg-neutral-100 p-4">
        <summary className="cursor-pointer text-[12.5px] font-medium">Configure heartbeat</summary>
        <form onSubmit={handleSubmit} className="mt-3 flex flex-col gap-3">
          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            <Input
              placeholder="interval (3h, 45m — blank turns it off)"
              value={tickInterval}
              onChange={(e) => setTickInterval(e.target.value)}
              className={fieldClass}
            />
          </div>
          <details>
            <summary className="cursor-pointer text-[12.5px] text-neutral-700">Advanced options</summary>
            <div className="mt-2.5 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
              <Input
                placeholder="instruction (optional)"
                value={instruction}
                onChange={(e) => setInstruction(e.target.value)}
                className={fieldClass + " sm:col-span-2"}
              />
              <Input
                placeholder="active from (08:00 — any hour)"
                value={activeStart}
                onChange={(e) => setActiveStart(e.target.value)}
                className={fieldClass}
              />
              <Input
                placeholder="active until (22:00 — any hour)"
                value={activeEnd}
                onChange={(e) => setActiveEnd(e.target.value)}
                className={fieldClass}
              />
            </div>
          </details>
          <div className="flex flex-wrap gap-2 pt-1">
            <button
              type="submit"
              disabled={saving}
              className="min-h-10 whitespace-nowrap rounded-xl bg-primary px-4 text-[13.5px] font-semibold text-primary-foreground transition-opacity hover:opacity-90 disabled:pointer-events-none disabled:opacity-50"
            >
              {saving ? "Saving..." : "Save heartbeat"}
            </button>
          </div>
        </form>
      </details>

      {result?.detail && <p className="mt-2 text-xs text-neutral-700">{result.detail}</p>}
      {error && (
        <p className="mt-3 rounded-xl bg-eg-red-tint px-3 py-2 text-sm text-eg-red-ink" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
