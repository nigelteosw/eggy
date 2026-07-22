import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";

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
    <section className="rounded-lg bg-white p-6 shadow">
      <h2 className="mb-4 text-lg font-semibold text-slate-900">Calendar</h2>
      <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-3">
        <label className="col-span-2 flex items-center gap-2 text-sm text-slate-700">
          <input type="checkbox" checked={enabled === "true"} onChange={(e) => setEnabled(e.target.checked ? "true" : "false")} />
          Enabled
        </label>
        <input
          placeholder="default_calendar"
          value={defaultCalendar}
          onChange={(e) => setDefaultCalendar(e.target.value)}
          className="rounded border border-slate-300 px-3 py-2"
        />
        <input placeholder="timezone (IANA)" value={timezone} onChange={(e) => setTimezone(e.target.value)} className="rounded border border-slate-300 px-3 py-2" />
        <button type="submit" disabled={saving} className="col-span-2 rounded bg-slate-900 px-4 py-2 text-white disabled:opacity-50">
          {saving ? "Saving..." : "Save calendar settings"}
        </button>
      </form>
      {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
    </section>
  );
}
