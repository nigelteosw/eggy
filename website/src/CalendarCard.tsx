import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { Input } from "./components/ui/input";

function fieldValue(fields: { label: string; value: string }[] | undefined, label: string): string {
  return fields?.find((field) => field.label === label)?.value ?? "";
}

// The default calendar is the whole configuration: setting it turns Calendar
// on, clearing it turns Calendar off. Event times follow agent.timezone, so
// there is no separate calendar timezone to set here.
export function CalendarCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("calendar", onSessionExpired);
  const [defaultCalendar, setDefaultCalendar] = useState("");
  const [initialized, setInitialized] = useState(false);

  useEffect(() => {
    if (result && !initialized) {
      setDefaultCalendar(fieldValue(result.fields, "Default calendar"));
      setInitialized(true);
    }
  }, [result, initialized]);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({ default_calendar: defaultCalendar });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Calendar</CardTitle>
        <CardDescription>
          Let Eggy read and schedule against your calendar. Clear the default calendar to turn Calendar off.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className="flex flex-col gap-3">
          <Input
            placeholder="default_calendar (e.g. primary)"
            value={defaultCalendar}
            onChange={(e) => setDefaultCalendar(e.target.value)}
          />
          <Button type="submit" disabled={saving}>
            {saving ? "Saving..." : "Save calendar settings"}
          </Button>
        </form>
        {defaultCalendar && (
          <p className="mt-4 text-sm text-muted-foreground">
            Connect the Google account at <code>/auth/google</code> after saving. Restart Eggy for a change here to take
            effect.
          </p>
        )}
        {error && (
          <p className="mt-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
