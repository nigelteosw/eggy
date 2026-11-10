import { SaveBar } from "../components/ui/save-bar";
import { useEffect, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  SessionExpiredError,
  createSkill,
  deleteSkill,
  getSkill,
  updateSkill,
  type SkillDetail as SkillDetailData,
  type SkillInput,
} from "../lib/api";
import { cn, errorMessage } from "../lib/utils";
import { ErrorBanner } from "../components/ui/error-banner";
import { Input } from "../components/ui/input";
import { Switch } from "../components/ui/switch";
import { FIELD, FIELD_LABEL, PRIMARY_BUTTON, SECONDARY_BUTTON } from "../components/ui/form";
import { FileCodeIcon } from "../components/ui/icons";
import { CodeEditor, Highlighted, splitFrontMatter } from "../components/ui/code-editor";

// One skill's page. `name` is "new" for the create form. The page has two
// reading tabs, Overview and Contents, and an editor that create and edit share.
// Renaming is not offered: a rename would orphan the old name, so an owner
// creates the new name and deletes the old one.
type Tab = "overview" | "contents";
type Draft = { name: string; description: string; body: string; disabled: boolean };
const EMPTY: Draft = { name: "", description: "", body: "", disabled: false };

export function SkillDetail({
  name,
  onBack,
  onSaved,
  onSessionExpired,
}: {
  name: string;
  onBack: () => void;
  onSaved: (name: string) => void;
  onSessionExpired: () => void;
}) {
  const isNew = name === "new";
  const [skill, setSkill] = useState<SkillDetailData | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [editing, setEditing] = useState(isNew);
  const [draft, setDraft] = useState<Draft>(EMPTY);
  const [tab, setTab] = useState<Tab>("overview");
  const [view, setView] = useState<"preview" | "source">("preview");
  const [notice, setNotice] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  useEffect(() => {
    if (isNew) return;
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name]);

  async function load() {
    try {
      const detail = await getSkill(name);
      setSkill(detail);
      setLoadError(null);
    } catch (err) {
      if (err instanceof SessionExpiredError) return onSessionExpired();
      setLoadError(errorMessage(err, "Could not open this skill."));
    }
  }

  function fail(err: unknown, fallback: string) {
    if (err instanceof SessionExpiredError) return onSessionExpired();
    setNotice(errorMessage(err, fallback));
  }

  function startEdit() {
    if (!skill) return;
    setDraft({ name: skill.name, description: skill.description, body: skill.body, disabled: skill.disabled });
    setNotice(null);
    setEditing(true);
  }

  async function save() {
    setSaving(true);
    setNotice(null);
    const input: SkillInput = { description: draft.description, body: draft.body, disabled: draft.disabled };
    try {
      if (isNew) {
        const created = draft.name.trim();
        await createSkill(created, input);
        onSaved(created);
        return;
      }
      await updateSkill(name, input);
      setEditing(false);
      await load();
    } catch (err) {
      // The draft stays on screen, so a rejected save loses nothing.
      fail(err, "Could not save the skill.");
    } finally {
      setSaving(false);
    }
  }

  async function toggleEnabled() {
    if (!skill) return;
    try {
      await updateSkill(name, { description: skill.description, body: skill.body, disabled: !skill.disabled });
      await load();
    } catch (err) {
      fail(err, "Could not change this skill.");
    }
  }

  async function remove() {
    setSaving(true);
    try {
      await deleteSkill(name);
      onBack();
    } catch (err) {
      fail(err, "Could not delete the skill.");
      setSaving(false);
    }
  }

  const backLink = (
    <button type="button" onClick={onBack} className="flex min-h-9 w-fit items-center gap-1.5 text-small text-neutral-700 hover:text-foreground">
      <span aria-hidden="true">←</span> Your skills
    </button>
  );

  if (editing) {
    return (
      <section className="flex flex-col gap-5">
        {backLink}
        <h1 className="text-title font-semibold tracking-tight">{isNew ? "New skill" : draft.name}</h1>
        <form
          className="flex flex-col gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <label className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL}>Name</span>
            <Input
              value={draft.name}
              onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              readOnly={!isNew}
              placeholder="fix-flaky-tests"
              className={FIELD}
              aria-describedby="skill-name-help"
            />
            <span id="skill-name-help" className="text-caption text-neutral-700">
              {isNew ? "Lowercase letters, digits, and hyphens." : "A skill's name cannot change. Add a new one to rename it."}
            </span>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL}>When to use it</span>
            <Input
              value={draft.description}
              onChange={(event) => setDraft({ ...draft, description: event.target.value })}
              placeholder="Use when a test fails intermittently"
              className={FIELD}
            />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL}>Instructions (Markdown)</span>
            <CodeEditor language="markdown" label="Instructions" value={draft.body} onChange={(body) => setDraft({ ...draft, body })} minHeight="21rem" />
          </label>
          <Switch checked={!draft.disabled} onCheckedChange={(on) => setDraft({ ...draft, disabled: !on })} label="Available to Eggy" />
          <p className="text-caption text-neutral-700">Skills are shared by everyone on this Eggy. Changes apply to new turns.</p>

          {notice && <ErrorBanner>{notice}</ErrorBanner>}

          <div className="flex flex-wrap gap-2">
            <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
              {saving ? "Saving…" : "Save skill"}
            </button>
            <button
              type="button"
              onClick={() => (isNew ? onBack() : setEditing(false))}
              className={SECONDARY_BUTTON}
            >
              Cancel
            </button>
          </div>
          <SaveBar label="Save skill" />
        </form>
      </section>
    );
  }

  return (
    <section className="flex flex-col gap-6">
      {backLink}

      <header className="flex flex-wrap items-center gap-4">
        <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-control bg-neutral-100 text-neutral-700">
          <FileCodeIcon className="h-5 w-5" />
        </span>
        <div className="min-w-0 flex-1">
          <h1 className="truncate font-mono text-title font-semibold tracking-tight">{name}</h1>
          <p className="text-small text-neutral-700">{skill ? (skill.disabled ? "Disabled. Eggy will not use it." : "Enabled. Eggy can use it when it fits a task.") : "Could not load"}</p>
        </div>
        {skill && (
          <div className="flex items-center gap-2">
            <Switch checked={!skill.disabled} onCheckedChange={() => void toggleEnabled()} label={<span className="sr-only">Available to Eggy</span>} />
          </div>
        )}
        <div className="relative">
          <button
            type="button"
            aria-label="More actions"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((open) => !open)}
            className="flex h-9 w-9 items-center justify-center rounded-control text-neutral-700 hover:bg-neutral-100 hover:text-foreground"
          >
            <span aria-hidden="true" className="text-title leading-none">⋮</span>
          </button>
          {menuOpen && (
            <div className="absolute right-0 z-10 mt-1 min-w-[160px] rounded-control bg-background p-1 shadow-lg ring-1 ring-neutral-200">
              <button
                type="button"
                onClick={() => {
                  setMenuOpen(false);
                  setConfirmingDelete(true);
                }}
                className="flex min-h-9 w-full items-center rounded-control px-3 text-left text-small text-tomato hover:bg-neutral-100"
              >
                Delete skill
              </button>
            </div>
          )}
        </div>
      </header>

      {confirmingDelete && (
        <div className="flex flex-wrap items-center gap-2 rounded-card bg-neutral-100 p-3 text-small">
          Delete {name} for everyone? This cannot be undone.
          <button type="button" disabled={saving} onClick={() => void remove()} className="min-h-9 rounded-control bg-tomato px-3 font-medium text-on-tomato">
            Delete
          </button>
          <button type="button" onClick={() => setConfirmingDelete(false)} className={SECONDARY_BUTTON}>
            Keep
          </button>
        </div>
      )}

      {notice && <ErrorBanner>{notice}</ErrorBanner>}

      {loadError ? (
        <ErrorBanner>{loadError}</ErrorBanner>
      ) : !skill ? (
        <p className="text-small text-neutral-700">Loading…</p>
      ) : (
        <>
          <div role="tablist" aria-label="Skill sections" className="flex gap-6 border-b border-neutral-200">
            {(["overview", "contents"] as Tab[]).map((item) => (
              <button
                key={item}
                type="button"
                role="tab"
                aria-selected={tab === item}
                onClick={() => setTab(item)}
                className={cn(
                  "-mb-px min-h-10 border-b-2 text-small capitalize transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                  tab === item ? "border-foreground font-medium text-foreground" : "border-transparent text-neutral-700 hover:text-foreground",
                )}
              >
                {item}
              </button>
            ))}
          </div>

          {tab === "overview" && (
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <span className={FIELD_LABEL}>When to use it</span>
                <p className="text-body leading-7 text-foreground">{skill.description}</p>
              </div>
              <div>
                <button type="button" onClick={startEdit} className={SECONDARY_BUTTON}>
                  Edit skill
                </button>
              </div>
            </div>
          )}

          {tab === "contents" && (
            <div className="grid min-h-[320px] overflow-hidden rounded-card ring-1 ring-neutral-200 sm:grid-cols-[200px_1fr]">
              <ul className="border-b border-neutral-200 bg-neutral-100 p-2 sm:border-b-0 sm:border-r">
                <li className="flex items-center gap-2 rounded-control bg-background px-3 py-2 font-mono text-small">
                  <FileCodeIcon className="h-4 w-4 text-neutral-700" />
                  SKILL.md
                </li>
              </ul>
              <div className="flex min-w-0 flex-col">
                <div className="flex items-center justify-between gap-2 border-b border-neutral-200 px-4 py-2">
                  <span className="font-mono text-caption text-neutral-700">/SKILL.md</span>
                  <div role="group" aria-label="View" className="flex rounded-control bg-neutral-100 p-0.5 text-caption">
                    {(["preview", "source"] as const).map((mode) => (
                      <button
                        key={mode}
                        type="button"
                        aria-pressed={view === mode}
                        onClick={() => setView(mode)}
                        className={cn("rounded-control px-2.5 py-1 capitalize", view === mode ? "bg-background text-foreground shadow-sm" : "text-neutral-700")}
                      >
                        {mode}
                      </button>
                    ))}
                  </div>
                </div>
                <div className="scrollbar-slim max-h-[60vh] overflow-auto p-4">
                  {view === "preview" ? (
                    <SkillPreview body={skill.body} />
                  ) : (
                    <pre className="whitespace-pre-wrap break-words font-mono text-small leading-6 text-foreground">
                      <Highlighted value={skill.body} language="markdown" />
                    </pre>
                  )}
                </div>
              </div>
            </div>
          )}
        </>
      )}
    </section>
  );
}

// The rendered view of a SKILL.md: its front matter, if it has one, as the YAML
// it is, and only the rest rendered as Markdown.
function SkillPreview({ body }: { body: string }) {
  const parts = splitFrontMatter(body);
  return (
    <div className="flex flex-col gap-4">
      {parts.frontMatter !== null && (
        <pre className="m-0 whitespace-pre-wrap break-words rounded-control bg-neutral-100 px-3 py-2.5 font-mono text-caption leading-5 text-foreground">
          <Highlighted value={parts.frontMatter} language="yaml" />
        </pre>
      )}
      <div className="markdown prose max-w-none text-small prose-code:rounded-mark prose-code:bg-neutral-100 prose-code:px-1 prose-code:py-0.5 prose-code:font-mono prose-code:before:content-none prose-code:after:content-none">
        <Markdown remarkPlugins={[remarkGfm]}>{parts.body}</Markdown>
      </div>
    </div>
  );
}
