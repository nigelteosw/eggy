import { useCallback, useEffect, useMemo, useState } from "react";
import {
  SessionExpiredError,
  createSkill,
  deleteSkill,
  getSkill,
  listSkills,
  updateSkill,
  type SkillDetail,
  type SkillRow,
} from "./api";
import { cn, errorMessage } from "./lib/utils";
import { Button } from "./components/ui/button";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";
import { FIELD, FIELD_LABEL, PRIMARY_BUTTON, SECONDARY_BUTTON } from "./components/ui/form";
import { SearchIcon } from "./components/ui/icons";

// The editor's state. `name` is fixed once a skill exists: renaming is a create
// plus a delete, so references to the old name are never silently orphaned.
type Editing = { mode: "create" } | { mode: "edit"; name: string };

type Draft = { name: string; description: string; body: string; disabled: boolean };

const EMPTY_DRAFT: Draft = { name: "", description: "", body: "", disabled: false };

export function SkillsPanel({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [rows, setRows] = useState<SkillRow[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [editing, setEditing] = useState<Editing | null>(null);
  const [draft, setDraft] = useState<Draft>(EMPTY_DRAFT);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  const handleError = useCallback(
    (err: unknown, fallback: string, set: (message: string) => void) => {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      set(errorMessage(err, fallback));
    },
    [onSessionExpired],
  );

  const reload = useCallback(async () => {
    try {
      setRows(await listSkills());
      setLoadError(null);
    } catch (err) {
      handleError(err, "Could not load skills.", setLoadError);
    }
  }, [handleError]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // Sorted by name, with the query applied to name and description. Invalid
  // files are shown too, so the owner can see and delete them.
  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return (rows ?? [])
      .filter((row) => needle === "" || row.name.includes(needle) || row.description.toLowerCase().includes(needle))
      .slice()
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [rows, query]);

  function startCreate() {
    setEditing({ mode: "create" });
    setDraft(EMPTY_DRAFT);
    setSaveError(null);
    setConfirmingDelete(false);
    setNotice(null);
  }

  async function startEdit(name: string) {
    setNotice(null);
    setSaveError(null);
    setConfirmingDelete(false);
    try {
      const skill: SkillDetail = await getSkill(name);
      setDraft({ name: skill.name, description: skill.description, body: skill.body, disabled: skill.disabled });
      setEditing({ mode: "edit", name });
    } catch (err) {
      handleError(err, "Could not open skill.", (message) => setNotice(message));
    }
  }

  // Toggling reads the whole skill first, because a save replaces the file's
  // description and body; sending only the flag would blank them.
  async function toggle(row: SkillRow) {
    setNotice(null);
    try {
      const skill = await getSkill(row.name);
      await updateSkill(row.name, { description: skill.description, body: skill.body, disabled: !row.disabled });
      await reload();
    } catch (err) {
      handleError(err, "Could not change this skill.", (message) => setNotice(message));
    }
  }

  async function save() {
    if (!editing) return;
    setSaving(true);
    setSaveError(null);
    try {
      if (editing.mode === "create") {
        await createSkill(draft.name.trim(), { description: draft.description, body: draft.body, disabled: draft.disabled });
      } else {
        await updateSkill(editing.name, { description: draft.description, body: draft.body, disabled: draft.disabled });
      }
      setEditing(null);
      await reload();
    } catch (err) {
      // The draft stays in place on failure, so a rejected save loses nothing.
      handleError(err, "Could not save the skill.", setSaveError);
    } finally {
      setSaving(false);
    }
  }

  async function remove(name: string) {
    setSaving(true);
    setSaveError(null);
    try {
      await deleteSkill(name);
      setEditing(null);
      setConfirmingDelete(false);
      await reload();
    } catch (err) {
      handleError(err, "Could not delete the skill.", setSaveError);
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="flex flex-col gap-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <CardHeader
          title="Skills"
          description="Each skill is a set of instructions Eggy reads when a task matches its description. Disabling one stops new turns from seeing it. Skills are shared by everyone on this Eggy, so a change here applies to all of them."
        />
        <div className="flex items-center gap-2">
          <label className={cn("flex h-11 items-center gap-2 rounded-xl px-3 text-neutral-700", FIELD)}>
            <SearchIcon className="h-4 w-4 shrink-0" />
            <input
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search skills"
              aria-label="Search skills"
              className="min-w-0 flex-1 bg-transparent text-sm text-foreground placeholder:text-neutral-500 focus-visible:outline-none sm:w-56"
            />
          </label>
          <Button type="button" onClick={startCreate} className={PRIMARY_BUTTON}>
            Add skill
          </Button>
        </div>
      </div>

      {notice && <ErrorBanner>{notice}</ErrorBanner>}

      {loadError ? (
        <ErrorBanner>
          {loadError}{" "}
          <button type="button" onClick={() => void reload()} className="underline">
            Try again
          </button>
        </ErrorBanner>
      ) : rows === null ? (
        <p className="text-sm text-neutral-700">Loading skills…</p>
      ) : visible.length === 0 ? (
        <p className="text-sm text-neutral-700">{rows.length === 0 ? "No skills yet. Add one to teach Eggy a procedure." : `No skills match “${query.trim()}”.`}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-neutral-200 rounded-2xl bg-neutral-100">
          {visible.map((row) => (
            <li key={row.name} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
              <div className="min-w-0 flex-1">
                <p className="truncate font-mono text-[13px] text-foreground">{row.name}</p>
                <p className="line-clamp-2 text-[13px] leading-5 text-neutral-700">{row.invalid ? `Could not load: ${row.invalid}` : row.description}</p>
              </div>
              <div className="flex items-center gap-3">
                <span className={cn("text-[12px]", row.invalid ? "text-destructive" : row.disabled ? "text-neutral-700" : "text-accent-700")}>
                  {row.invalid ? "Invalid" : row.disabled ? "Disabled" : "Enabled"}
                </span>
                {!row.invalid && (
                  <Switch checked={!row.disabled} onCheckedChange={() => void toggle(row)} label={<span className="sr-only">Enable {row.name}</span>} />
                )}
                <Button type="button" variant="ghost" onClick={() => (row.invalid ? setNotice(`${row.name} cannot be opened: ${row.invalid}`) : void startEdit(row.name))} className={SECONDARY_BUTTON}>
                  {row.invalid ? "Details" : "Edit"}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}

      {editing && (
        <form
          className="flex flex-col gap-4 rounded-2xl bg-neutral-100 p-4 sm:p-5"
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <h3 className="text-[15px] font-semibold tracking-tight">{editing.mode === "create" ? "New skill" : `Edit ${editing.name}`}</h3>
          <label className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL}>Name</span>
            <Input
              value={draft.name}
              onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              readOnly={editing.mode === "edit"}
              placeholder="fix-flaky-tests"
              className={FIELD}
              aria-describedby="skill-name-help"
            />
            <span id="skill-name-help" className="text-[12px] text-neutral-700">
              {editing.mode === "edit" ? "A skill's name cannot change. Create a new one to rename it." : "Lowercase letters, digits, and hyphens."}
            </span>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL}>When to use it</span>
            <Input value={draft.description} onChange={(event) => setDraft({ ...draft, description: event.target.value })} placeholder="Use when a test fails intermittently" className={FIELD} />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL}>Instructions (Markdown)</span>
            <textarea
              value={draft.body}
              onChange={(event) => setDraft({ ...draft, body: event.target.value })}
              rows={12}
              className={cn("scrollbar-slim w-full rounded-xl bg-background p-3 font-mono text-[13px] leading-6 text-foreground shadow-[inset_0_0_0_1px_hsl(var(--neutral-200))] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40")}
            />
          </label>
          <Switch checked={!draft.disabled} onCheckedChange={(enabled) => setDraft({ ...draft, disabled: !enabled })} label="Available to Eggy" />

          {saveError && <ErrorBanner>{saveError}</ErrorBanner>}

          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
              {saving ? "Saving…" : "Save skill"}
            </Button>
            <Button type="button" variant="ghost" onClick={() => setEditing(null)} className={SECONDARY_BUTTON}>
              Cancel
            </Button>
            {editing.mode === "edit" &&
              (confirmingDelete ? (
                <span className="ml-auto flex items-center gap-2 text-[13px]">
                  Delete {editing.name} for everyone?
                  <Button type="button" variant="destructive" disabled={saving} onClick={() => void remove(editing.name)} className="min-h-10 rounded-xl">
                    Delete
                  </Button>
                  <Button type="button" variant="ghost" onClick={() => setConfirmingDelete(false)} className={SECONDARY_BUTTON}>
                    Keep
                  </Button>
                </span>
              ) : (
                <Button type="button" variant="ghost" onClick={() => setConfirmingDelete(true)} className={cn("ml-auto", SECONDARY_BUTTON)}>
                  Delete
                </Button>
              ))}
          </div>
        </form>
      )}
    </section>
  );
}
