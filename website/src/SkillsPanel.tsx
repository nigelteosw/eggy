import { useCallback, useEffect, useMemo, useState } from "react";
import { SessionExpiredError, getSkill, listSkills, updateSkill, type SkillRow } from "./api";
import { cn, errorMessage } from "./lib/utils";
import { ErrorBanner } from "./components/ui/error-banner";
import { Switch } from "./components/ui/switch";
import { FileCodeIcon } from "./components/ui/icons";

// The owner's skills, grouped under "Yours" like the other lists in the rail.
// Clicking a row opens its detail page; the switch turns it on or off in place.
export function SkillsPanel({
  query,
  onOpen,
  onSessionExpired,
}: {
  query: string;
  onOpen: (name: string) => void;
  onSessionExpired: () => void;
}) {
  const [rows, setRows] = useState<SkillRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      setRows(await listSkills());
      setError(null);
    } catch (err) {
      if (err instanceof SessionExpiredError) return onSessionExpired();
      setError(errorMessage(err, "Could not load skills."));
    }
  }, [onSessionExpired]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return (rows ?? [])
      .filter((row) => needle === "" || row.name.includes(needle) || row.description.toLowerCase().includes(needle))
      .slice()
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [rows, query]);

  // Toggling reads the whole skill first, because a save replaces its description
  // and body; sending only the flag would blank them.
  async function toggle(row: SkillRow) {
    try {
      const skill = await getSkill(row.name);
      await updateSkill(row.name, { description: skill.description, body: skill.body, disabled: !row.disabled });
      await reload();
    } catch (err) {
      if (err instanceof SessionExpiredError) return onSessionExpired();
      setError(errorMessage(err, "Could not change this skill."));
    }
  }

  if (error) return <ErrorBanner>{error}</ErrorBanner>;
  if (rows === null) return <p className="text-sm text-neutral-700">Loading skills…</p>;
  if (rows.length === 0) {
    return <p className="text-sm text-neutral-700">No skills yet. Add one to teach Eggy a procedure it can follow.</p>;
  }
  if (visible.length === 0) return <p className="text-sm text-neutral-700">No skills match “{query.trim()}”.</p>;

  return (
    <section className="flex flex-col gap-2">
      <h2 className="flex items-center gap-2 text-[15px] font-medium">
        Yours
        <span className="rounded-full bg-neutral-100 px-2 py-0.5 text-[12px] text-neutral-700">{rows.length}</span>
      </h2>
      <ul className="flex flex-col divide-y divide-neutral-200 border-y border-neutral-200">
        {visible.map((row) => (
          <li key={row.name} className="flex items-center gap-4 py-3">
            <button
              type="button"
              onClick={() => onOpen(row.name)}
              className="flex min-w-0 flex-1 items-start gap-3 rounded-lg text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            >
              <span className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-neutral-100 text-neutral-700">
                <FileCodeIcon className="h-4 w-4" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex flex-wrap items-center gap-2">
                  <span className="truncate font-mono text-[13.5px] text-foreground">{row.name}</span>
                  {row.invalid ? (
                    <span className="rounded-md bg-destructive/10 px-1.5 py-0.5 text-[11px] text-destructive">Invalid</span>
                  ) : row.disabled ? (
                    <span className="rounded-md bg-neutral-100 px-1.5 py-0.5 text-[11px] text-neutral-700">Disabled</span>
                  ) : null}
                </span>
                <span className="line-clamp-2 text-[13px] leading-5 text-neutral-700">{row.invalid ? `Could not load: ${row.invalid}` : row.description}</span>
              </span>
            </button>
            {!row.invalid && (
              <Switch checked={!row.disabled} onCheckedChange={() => void toggle(row)} label={<span className="sr-only">Available to Eggy: {row.name}</span>} className={cn("shrink-0")} />
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
