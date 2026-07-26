import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";

function fieldValue(fields: { label: string; value: string }[] | undefined, label: string): string {
  return fields?.find((field) => field.label === label)?.value ?? "";
}

export function CalendarCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("calendar", onSessionExpired);
  const [enabled, setEnabled] = useState("false");
  const [defaultCalendar, setDefaultCalendar] = useState("");
  const [timezone, setTimezone] = useState("");
  const [initialized, setInitialized] = useState(false);

  useEffect(() => {
    if (result && !initialized) {
      setEnabled(fieldValue(result.fields, "Enabled") || "false");
      setDefaultCalendar(fieldValue(result.fields, "Default calendar"));
      setTimezone(fieldValue(result.fields, "Timezone"));
      setInitialized(true);
    }
  }, [result, initialized]);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({ enabled, default_calendar: defaultCalendar, timezone });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Calendar</CardTitle>
        <CardDescription>Let Eggy read and schedule against your calendar.</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="rounded-md border border-border bg-muted/40 px-3 py-2.5 sm:col-span-2">
            <Switch checked={enabled === "true"} onCheckedChange={(next) => setEnabled(next ? "true" : "false")} label="Enabled" />
          </div>
          <Input placeholder="default_calendar" value={defaultCalendar} onChange={(e) => setDefaultCalendar(e.target.value)} />
          <Input placeholder="timezone (IANA)" value={timezone} onChange={(e) => setTimezone(e.target.value)} />
          <Button type="submit" disabled={saving} className="sm:col-span-2">
            {saving ? "Saving..." : "Save calendar settings"}
          </Button>
        </form>
        {error && (
          <p className="mt-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
