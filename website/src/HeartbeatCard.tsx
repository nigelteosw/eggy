import { useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
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

  return (
    <Card>
      <CardHeader>
        <CardTitle>Heartbeat</CardTitle>
        <CardDescription>A periodic check-in that messages you only when something needs attention.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable headers={result?.table_headers} rows={result?.table_rows} empty="Heartbeat is off." />
        <details className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">Configure heartbeat</summary>
          <form onSubmit={handleSubmit} className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Input placeholder="interval (3h, 45m — blank turns it off)" value={tickInterval} onChange={(e) => setTickInterval(e.target.value)} />
            <details className="sm:col-span-2">
              <summary className="cursor-pointer text-sm text-muted-foreground">Advanced options</summary>
              <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Input placeholder="instruction (optional)" value={instruction} onChange={(e) => setInstruction(e.target.value)} className="sm:col-span-2" />
                <Input placeholder="active from (08:00 — any hour)" value={activeStart} onChange={(e) => setActiveStart(e.target.value)} />
                <Input placeholder="active until (22:00 — any hour)" value={activeEnd} onChange={(e) => setActiveEnd(e.target.value)} />
              </div>
            </details>
            <Button type="submit" disabled={saving} className="sm:col-span-2">
              {saving ? "Saving..." : "Save heartbeat"}
            </Button>
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
