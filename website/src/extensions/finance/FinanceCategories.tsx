import { useState } from "react";
import type { FinanceCategory } from "../../lib/api";
import { FIELD_COMPACT, PRIMARY_BUTTON, SECONDARY_BUTTON } from "../../components/ui/form";
import { Input } from "../../components/ui/input";
import { categoryHex, entriesWord } from "./finance";
import { cn } from "../../lib/utils";

// The colour dot every category wears on the page. The colour comes from the
// server's palette name, so a category draws the same everywhere.
export function CategoryDot({ color, className }: { color: string; className?: string }) {
  return <span aria-hidden className={cn("inline-block h-2.5 w-2.5 shrink-0 rounded-full", className)} style={{ backgroundColor: categoryHex(color) }} />;
}

export function CategoryChip({ name, color }: { name: string; color: string }) {
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 rounded-full bg-neutral-200 px-2 py-0.5 text-[11px] text-foreground">
      <CategoryDot color={color} />
      <span className="truncate">{name}</span>
    </span>
  );
}

// The categories the account has used, each with its colour and a rename. A
// rename into an existing category merges the two, which the panel says before
// the owner does it.
export function CategoriesPanel({
  categories,
  palette,
  busy,
  onColour,
  onRename,
}: {
  categories: FinanceCategory[];
  palette: string[];
  busy: boolean;
  onColour: (name: string, color: string) => void;
  onRename: (from: string, to: string) => Promise<boolean>;
}) {
  if (categories.length === 0) return null;
  return (
    <details className="rounded-2xl bg-neutral-100 p-4">
      <summary className="flex cursor-pointer list-none items-center justify-between gap-3 text-[13.5px] font-medium">
        Categories
        <span className="text-[12px] tabular-nums text-neutral-700">{categories.length}</span>
      </summary>
      <p className="mt-3 text-[12px] leading-5 text-neutral-700">
        Pick a colour for each. Renaming a category to the name of another merges the two, and keeps the colour of the one you rename into.
      </p>
      <ul className="mt-4 flex flex-col gap-3">
        {categories.map((category) => (
          <CategoryRow key={category.name} category={category} palette={palette} busy={busy} onColour={onColour} onRename={onRename} />
        ))}
      </ul>
    </details>
  );
}

function CategoryRow({
  category,
  palette,
  busy,
  onColour,
  onRename,
}: {
  category: FinanceCategory;
  palette: string[];
  busy: boolean;
  onColour: (name: string, color: string) => void;
  onRename: (from: string, to: string) => Promise<boolean>;
}) {
  const [renaming, setRenaming] = useState(false);
  const [draft, setDraft] = useState(category.name);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    const name = draft.trim();
    if (name === "" || name.toLowerCase() === category.name) {
      setRenaming(false);
      return;
    }
    if (await onRename(category.name, name)) setRenaming(false);
  }

  return (
    <li className="flex flex-col gap-3 rounded-xl bg-background px-4 py-3">
      <div className="flex items-center gap-3">
        <CategoryDot color={category.color} className="h-3 w-3" />
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{category.name}</span>
        <span className="text-[11.5px] tabular-nums text-neutral-700">{entriesWord(category.count)}</span>
        <button type="button" aria-expanded={renaming} onClick={() => setRenaming((open) => !open)} className={cn(SECONDARY_BUTTON, "min-h-9 px-3 text-[12.5px]")}>
          Rename
        </button>
      </div>
      <div role="radiogroup" aria-label={`Colour for ${category.name}`} className="flex flex-wrap gap-1">
        {palette.map((name) => {
          const selected = name === category.color;
          return (
            <button
              key={name}
              type="button"
              role="radio"
              aria-checked={selected}
              aria-label={name}
              title={name}
              disabled={busy}
              onClick={() => onColour(category.name, name)}
              className={cn("flex h-11 w-11 items-center justify-center rounded-full transition-colors disabled:opacity-50", selected ? "bg-neutral-200" : "hover:bg-neutral-100")}
            >
              <span aria-hidden className={cn("h-6 w-6 rounded-full", selected && "ring-2 ring-foreground ring-offset-2 ring-offset-background")} style={{ backgroundColor: categoryHex(name) }} />
            </button>
          );
        })}
      </div>
      {renaming && (
        <form onSubmit={submit} className="flex gap-2">
          <Input aria-label="New name" value={draft} onChange={(event) => setDraft(event.target.value)} className={cn(FIELD_COMPACT, "min-w-0 flex-1")} />
          <button type="submit" disabled={busy} className={PRIMARY_BUTTON}>
            Save
          </button>
        </form>
      )}
    </li>
  );
}
