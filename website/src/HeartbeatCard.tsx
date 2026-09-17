import { useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Input } from "./components/ui/input";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD_COMPACT, PRIMARY_BUTTON } from "./components/ui/form";
import { SummaryRows } from "./components/ui/summary-rows";

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

  return (
    <div className="mb-2">
      <CardHeader title="Heartbeat" className="mb-3" description="A periodic check-in that messages you only when something needs attention." />

      {!row ? (
        <p className="rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">Heartbeat is off.</p>
      ) : (
        <SummaryRows headers={headers} row={row} />
      )}

      <details className="rounded-2xl bg-neutral-100 p-4">
        <summary className="cursor-pointer text-[12.5px] font-medium">Configure heartbeat</summary>
        <form onSubmit={handleSubmit} className="mt-3 flex flex-col gap-3">
          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            <Input
              placeholder="interval (3h, 45m — blank turns it off)"
              value={tickInterval}
              onChange={(e) => setTickInterval(e.target.value)}
              className={FIELD_COMPACT}
            />
          </div>
          <details>
            <summary className="cursor-pointer text-[12.5px] text-neutral-700">Advanced options</summary>
            <div className="mt-2.5 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
              <Input
                placeholder="instruction (optional)"
                value={instruction}
                onChange={(e) => setInstruction(e.target.value)}
                className={FIELD_COMPACT + " sm:col-span-2"}
              />
              <Input
                placeholder="active from (08:00 — any hour)"
                value={activeStart}
                onChange={(e) => setActiveStart(e.target.value)}
                className={FIELD_COMPACT}
              />
              <Input
                placeholder="active until (22:00 — any hour)"
                value={activeEnd}
                onChange={(e) => setActiveEnd(e.target.value)}
                className={FIELD_COMPACT}
              />
            </div>
          </details>
          <div className="flex flex-wrap gap-2 pt-1">
            <button
              type="submit"
              disabled={saving}
              className={PRIMARY_BUTTON}
            >
              {saving ? "Saving..." : "Save heartbeat"}
            </button>
          </div>
        </form>
      </details>

      {result?.detail && <p className="mt-2 text-xs text-neutral-700">{result.detail}</p>}
      {error && (
        <ErrorBanner className="mt-3">
          {error}
        </ErrorBanner>
      )}
    </div>
  );
}
