import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { Switch } from "./components/ui/switch";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD_COMPACT, FIELD_LABEL, PRIMARY_BUTTON, SECONDARY_BUTTON } from "./components/ui/form";
import { SummaryRows } from "./components/ui/summary-rows";

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


  return (
    <div className="mb-2">
      <CardHeader title="Tracing" className="mb-3" description="Choose what the Traces dashboard records and how long it is retained." />

      {!row ? (
        <p className="rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">Tracing is off.</p>
      ) : (
        <SummaryRows headers={headers} row={row} />
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
                <Label htmlFor="tracing-keep" className={FIELD_LABEL}>
                  Turns kept
                </Label>
                <Input id="tracing-keep" inputMode="numeric" placeholder="500" value={keepTurns} onChange={(e) => setKeepTurns(e.target.value)} className={FIELD_COMPACT} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="tracing-retention" className={FIELD_LABEL}>
                  Kept for
                </Label>
                <Input id="tracing-retention" list="tracing-retention-presets" placeholder="168h" value={retention} onChange={(e) => setRetention(e.target.value)} className={FIELD_COMPACT} />
                <datalist id="tracing-retention-presets">
                  {RETENTION_PRESETS.map((preset) => <option key={preset} value={preset} />)}
                </datalist>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="tracing-max-body" className={FIELD_LABEL}>
                  Max body (bytes)
                </Label>
                <Input id="tracing-max-body" inputMode="numeric" placeholder="1048576" value={maxBodyBytes} onChange={(e) => setMaxBodyBytes(e.target.value)} className={FIELD_COMPACT} />
              </div>
            </div>
            <p className="mt-2.5 text-xs text-neutral-700">Blank fields use defaults. The first limit reached drops the oldest traces.</p>
          </details>
          <div className="flex flex-wrap gap-2 pt-1">
            <button
              type="submit"
              disabled={saving}
              className={PRIMARY_BUTTON}
            >
              {saving ? "Saving..." : "Save tracing"}
            </button>
            <button
              type="button"
              disabled={saving}
              onClick={handleRestoreDefaults}
              className={SECONDARY_BUTTON}
            >
              Restore defaults
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
