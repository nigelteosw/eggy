import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { Switch } from "./components/ui/switch";

const RETENTION_PRESETS = ["24h", "72h", "168h", "720h"];

export function TracingCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("tracing", onSessionExpired);
  const [enabled, setEnabled] = useState(true);
  const [keepTurns, setKeepTurns] = useState("");
  const [retention, setRetention] = useState("");
  const [maxBodyBytes, setMaxBodyBytes] = useState("");

  const headers = result?.table_headers ?? [];
  const row = result?.table_rows?.[0];
  useEffect(() => {
    if (!row) return;
    setEnabled(row[0] !== "off");
    setKeepTurns(row[1] ?? "");
    setRetention(row[2] ?? "");
    setMaxBodyBytes(row[3] ?? "");
  }, [result]);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({
      enabled: enabled ? "true" : "false",
      keep_turns: keepTurns,
      retention,
      max_body_bytes: maxBodyBytes,
    });
  }

  async function handleRestoreDefaults() {
    setEnabled(true);
    setKeepTurns("");
    setRetention("");
    setMaxBodyBytes("");
    await save({ enabled: "true", keep_turns: "", retention: "", max_body_bytes: "" });
  }

  const fieldClass =
    "h-[42px] w-full rounded-xl border border-neutral-200 bg-background px-3.5 text-[13px] text-foreground caret-accent-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30";

  return (
    <div className="mb-2">
      <div className="mx-0.5 mb-3">
        <h3 className="text-[15px] font-semibold tracking-tight">Tracing</h3>
        <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">
          Choose what the Traces dashboard records and how long it is retained.
        </p>
      </div>

      {!row ? (
        <p className="rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">Tracing is off.</p>
      ) : (
        <div className="mb-3 flex flex-col">
          {headers.map((label, i) => (
            <div
              key={label}
              className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-1 py-3.5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]"
            >
              <span className="min-w-0 text-sm">{label}</span>
              <span className="min-w-0 text-right text-[13.5px] tabular-nums text-neutral-700 [overflow-wrap:anywhere]">{row[i]}</span>
            </div>
          ))}
        </div>
      )}

      <details className="rounded-2xl bg-neutral-100 p-4">
        <summary className="cursor-pointer text-[12.5px] font-medium">Configure tracing</summary>
        <form onSubmit={handleSubmit} className="mt-3 flex flex-col gap-4">
          <Switch checked={enabled} onCheckedChange={setEnabled} label={enabled ? "Recording turns" : "Not recording turns"} />
          <details>
            <summary className="cursor-pointer text-[12.5px] text-neutral-700">Advanced options</summary>
            <div
              className={`mt-2.5 grid grid-cols-1 gap-2.5 transition-opacity sm:grid-cols-3 ${enabled ? "" : "pointer-events-none opacity-50"}`}
            >
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="tracing-keep" className="text-[11.5px] font-normal text-neutral-700">
                  Turns kept
                </Label>
                <Input id="tracing-keep" inputMode="numeric" placeholder="500" value={keepTurns} onChange={(e) => setKeepTurns(e.target.value)} className={fieldClass} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="tracing-retention" className="text-[11.5px] font-normal text-neutral-700">
                  Kept for
                </Label>
                <Input id="tracing-retention" list="tracing-retention-presets" placeholder="168h" value={retention} onChange={(e) => setRetention(e.target.value)} className={fieldClass} />
                <datalist id="tracing-retention-presets">
                  {RETENTION_PRESETS.map((preset) => <option key={preset} value={preset} />)}
                </datalist>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="tracing-max-body" className="text-[11.5px] font-normal text-neutral-700">
                  Max body (bytes)
                </Label>
                <Input id="tracing-max-body" inputMode="numeric" placeholder="1048576" value={maxBodyBytes} onChange={(e) => setMaxBodyBytes(e.target.value)} className={fieldClass} />
              </div>
            </div>
            <p className="mt-2.5 text-xs text-neutral-700">Blank fields use defaults. The first limit reached drops the oldest traces.</p>
          </details>
          <div className="flex flex-wrap gap-2 pt-1">
            <button
              type="submit"
              disabled={saving}
              className="min-h-10 whitespace-nowrap rounded-xl bg-primary px-4 text-[13.5px] font-semibold text-primary-foreground transition-opacity hover:opacity-90 disabled:pointer-events-none disabled:opacity-50"
            >
              {saving ? "Saving..." : "Save tracing"}
            </button>
            <button
              type="button"
              disabled={saving}
              onClick={handleRestoreDefaults}
              className="min-h-10 whitespace-nowrap rounded-xl px-4 text-[13.5px] font-medium text-neutral-700 transition-colors hover:bg-neutral-200 disabled:pointer-events-none disabled:opacity-50"
            >
              Restore defaults
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
