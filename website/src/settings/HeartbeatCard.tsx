import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { FIELD_COMPACT, FIELD_LABEL, PRIMARY_BUTTON } from "../components/ui/form";
import { Label } from "../components/ui/label";
import { SaveBar } from "../components/ui/save-bar";
import { Select } from "../components/ui/select";
import { SharedLabel } from "../components/ui/shared-label";
import { Switch } from "../components/ui/switch";
import { CADENCES, cadenceLabel, durationOf, minutesOf, windowOf } from "../lib/checkins";

type Form = { minutes: number; windowed: boolean; start: string; end: string; instruction: string };

// HeartbeatCard is the deployment's half of check-ins: how often Eggy looks,
// and between which hours. It is shared, so it says so. The form starts from
// what is saved, so changing the cadence never clears the hours beside it.
export function HeartbeatCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("heartbeat", onSessionExpired);
  const row = result?.table_rows?.[0];
  const [saved, setSaved] = useState<Form | null>(null);
  const [form, setForm] = useState<Form | null>(null);

  useEffect(() => {
    if (!row) return;
    const minutes = minutesOf(row[0]) ?? 0;
    const window = windowOf(row[2]);
    const next: Form = { minutes, windowed: window !== null, start: window?.start ?? "08:00", end: window?.end ?? "22:00", instruction: "" };
    setSaved(next);
    setForm(next);
    // The row is the server's answer; it changes only on a load.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [row?.join("\u0000")]);

  const dirty = form !== null && saved !== null && JSON.stringify(form) !== JSON.stringify(saved);

  async function submit() {
    if (!form) return;
    await save({
      interval: durationOf(form.minutes),
      instruction: form.instruction,
      active_start: form.windowed ? form.start : "",
      active_end: form.windowed ? form.end : "",
    });
  }

  const set = (patch: Partial<Form>) => setForm((current) => (current ? { ...current, ...patch } : current));
  // A cadence set in config.yaml that the picker does not offer is kept as a choice.
  const choices = form && !CADENCES.some((cadence) => cadence.minutes === form.minutes) ? [...CADENCES, { minutes: form.minutes, label: cadenceLabel(form.minutes) }] : CADENCES;

  return (
    <div className="mb-2 flex flex-col gap-3">
      <CardHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            How often I check <SharedLabel />
          </span>
        }
        description="The cadence and hours apply to everyone who has check-ins on."
      />
      {form && (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
          className="flex flex-col gap-4 rounded-card bg-neutral-100 p-4"
        >
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="checkin-cadence" className={FIELD_LABEL}>
              How often
            </Label>
            <Select id="checkin-cadence" value={form.minutes} onChange={(event) => set({ minutes: Number(event.target.value) })} className="bg-background text-field sm:text-small">
              {choices.map((cadence) => (
                <option key={cadence.minutes} value={cadence.minutes}>
                  {cadence.label}
                </option>
              ))}
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Switch label="Only between certain hours" checked={form.windowed} onCheckedChange={(windowed) => set({ windowed })} />
            {form.windowed && (
              <div className="grid grid-cols-2 gap-2.5 sm:max-w-sm">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="checkin-from" className={FIELD_LABEL}>
                    From
                  </Label>
                  <input id="checkin-from" type="time" value={form.start} onChange={(event) => set({ start: event.target.value })} className={FIELD_COMPACT} />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="checkin-until" className={FIELD_LABEL}>
                    Until
                  </Label>
                  <input id="checkin-until" type="time" value={form.end} onChange={(event) => set({ end: event.target.value })} className={FIELD_COMPACT} />
                </div>
              </div>
            )}
          </div>
          <details>
            <summary className="cursor-pointer text-small text-neutral-700">What I look for</summary>
            <div className="mt-2.5 flex flex-col gap-1.5">
              <Label htmlFor="checkin-instruction" className={FIELD_LABEL}>
                Instruction <span className="text-neutral-500">· heartbeat.instruction</span>
              </Label>
              <textarea
                id="checkin-instruction"
                rows={3}
                value={form.instruction}
                placeholder={row?.[1] === "(built-in default)" ? "The built-in instruction" : row?.[1]}
                onChange={(event) => set({ instruction: event.target.value })}
                className={FIELD_COMPACT + " h-auto py-2.5"}
              />
              <p className="text-caption text-neutral-700">Leave it blank to keep the current one.</p>
            </div>
          </details>
          <div className="hidden md:block">
            <button type="submit" disabled={saving || !dirty} className={PRIMARY_BUTTON}>
              {saving ? "Saving..." : "Save"}
            </button>
          </div>
          <SaveBar dirty={dirty} saving={saving} label="Save check-ins" onSave={submit} />
        </form>
      )}
      {result?.detail && <p className="text-caption text-neutral-700">{result.detail}</p>}
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </div>
  );
}
