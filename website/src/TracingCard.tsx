import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { Switch } from "./components/ui/switch";

// The defaults this form falls back to. They are not restated here: a blank
// field means "the default" all the way through to config.applyDefaults, which
// is the one place that knows the numbers. Restoring defaults is therefore the
// same save every other edit makes, with the fields emptied -- not a second
// path that could disagree about what a default is.
const RETENTION_PRESETS = ["24h", "72h", "168h", "720h"];

export function TracingCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("tracing", onSessionExpired);
  const [enabled, setEnabled] = useState(true);
  const [keepTurns, setKeepTurns] = useState("");
  const [retention, setRetention] = useState("");
  const [maxBodyBytes, setMaxBodyBytes] = useState("");

  // The form starts from what is actually configured rather than from blanks,
  // so the owner edits their settings instead of retyping them. The row the
  // GET route returns is the same one shown in the table above.
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
    // Blank fields are the request for defaults, so this is the ordinary save
    // with nothing filled in.
    await save({ enabled: "true", keep_turns: "", retention: "", max_body_bytes: "" });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Tracing</CardTitle>
        <CardDescription>
          Records every turn as it runs: the prompt behind each model call, and the arguments and output of every tool
          call. Read them under Traces. Prompts are the largest and most sensitive thing Eggy stores — they carry your
          memory documents and recent conversation — so old traces are dropped on both a count and an age.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable headers={result?.table_headers} rows={result?.table_rows} empty="Tracing is off." />
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <Switch
            checked={enabled}
            onCheckedChange={setEnabled}
            label={enabled ? "Recording turns" : "Not recording turns"}
          />
          <div
            className={`grid grid-cols-1 gap-3 transition-opacity sm:grid-cols-3 ${enabled ? "" : "pointer-events-none opacity-50"}`}
          >
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="tracing-keep">Turns kept</Label>
              <Input
                id="tracing-keep"
                inputMode="numeric"
                placeholder="500"
                value={keepTurns}
                onChange={(e) => setKeepTurns(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="tracing-retention">Kept for</Label>
              <Input
                id="tracing-retention"
                list="tracing-retention-presets"
                placeholder="168h"
                value={retention}
                onChange={(e) => setRetention(e.target.value)}
              />
              <datalist id="tracing-retention-presets">
                {RETENTION_PRESETS.map((preset) => (
                  <option key={preset} value={preset} />
                ))}
              </datalist>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="tracing-max-body">Max body (bytes)</Label>
              <Input
                id="tracing-max-body"
                inputMode="numeric"
                placeholder="1048576"
                value={maxBodyBytes}
                onChange={(e) => setMaxBodyBytes(e.target.value)}
              />
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            Leave a field blank to use its default. Whichever limit is reached first drops the oldest traces.
          </p>
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={saving}>
              {saving ? "Saving..." : "Save tracing"}
            </Button>
            <Button type="button" variant="ghost" disabled={saving} onClick={handleRestoreDefaults}>
              Restore defaults
            </Button>
          </div>
        </form>
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
