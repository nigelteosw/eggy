import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
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
    <Card>
      <CardHeader>
        <CardTitle>Tracing</CardTitle>
        <CardDescription>Choose what the Traces dashboard records and how long it is retained.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable headers={result?.table_headers} rows={result?.table_rows} empty="Tracing is off." />
        <details className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">Configure tracing</summary>
          <form onSubmit={handleSubmit} className="mt-4 flex flex-col gap-4">
            <Switch
              checked={enabled}
              onCheckedChange={setEnabled}
              label={enabled ? "Recording turns" : "Not recording turns"}
            />
            <details>
              <summary className="cursor-pointer text-sm text-muted-foreground">Advanced options</summary>
              <div
                className={`mt-3 grid grid-cols-1 gap-3 transition-opacity sm:grid-cols-3 ${enabled ? "" : "pointer-events-none opacity-50"}`}
              >
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="tracing-keep">Turns kept</Label>
                  <Input id="tracing-keep" inputMode="numeric" placeholder="500" value={keepTurns} onChange={(e) => setKeepTurns(e.target.value)} />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="tracing-retention">Kept for</Label>
                  <Input id="tracing-retention" list="tracing-retention-presets" placeholder="168h" value={retention} onChange={(e) => setRetention(e.target.value)} />
                  <datalist id="tracing-retention-presets">
                    {RETENTION_PRESETS.map((preset) => <option key={preset} value={preset} />)}
                  </datalist>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="tracing-max-body">Max body (bytes)</Label>
                  <Input id="tracing-max-body" inputMode="numeric" placeholder="1048576" value={maxBodyBytes} onChange={(e) => setMaxBodyBytes(e.target.value)} />
                </div>
              </div>
              <p className="mt-2 text-xs text-muted-foreground">
                Blank fields use defaults. The first limit reached drops the oldest traces.
              </p>
            </details>
            <div className="flex flex-wrap gap-2">
              <Button type="submit" disabled={saving}>
                {saving ? "Saving..." : "Save tracing"}
              </Button>
              <Button type="button" variant="ghost" disabled={saving} onClick={handleRestoreDefaults}>
                Restore defaults
              </Button>
            </div>
          </form>
        </details>
        {result?.detail && <p className="text-xs text-muted-foreground">{result.detail}</p>}
        {error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
