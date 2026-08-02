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

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({ interval: tickInterval, instruction });
    setInstruction("");
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Heartbeat</CardTitle>
        <CardDescription>
          A periodic check-in that runs on its own and messages you on Telegram only when something is worth saying.
          Leave the interval blank to turn it off. 3h is a good starting point.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable headers={result?.table_headers} rows={result?.table_rows} empty="Heartbeat is off." />
        <form onSubmit={handleSubmit} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Input
            placeholder="interval (3h, 45m — blank turns it off)"
            value={tickInterval}
            onChange={(e) => setTickInterval(e.target.value)}
          />
          <Input
            placeholder="instruction (optional)"
            value={instruction}
            onChange={(e) => setInstruction(e.target.value)}
          />
          <Button type="submit" disabled={saving} className="sm:col-span-2">
            {saving ? "Saving..." : "Save heartbeat"}
          </Button>
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
