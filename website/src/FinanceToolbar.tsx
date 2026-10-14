import { useEffect, useRef, useState, type ReactNode } from "react";
import { ArrowsUpDownIcon, ChevronDownIcon, PlusIcon, SearchIcon, SlidersIcon } from "./components/ui/icons";
import { FIELD_COMPACT, FIELD_LABEL, PRIMARY_BUTTON } from "./components/ui/form";
import { Select } from "./components/ui/select";
import { KIND_LABEL, KINDS } from "./lib/finance";
import { SEARCH_DEBOUNCE_MS, SORTS, SORT_KEYS, chooseSort, filterCount, isFiltered, type EntryView } from "./lib/finance-list";
import { cn } from "./lib/utils";

type Menu = "filter" | "sort" | null;

function ToolButton({ label, active, expanded, badge, onClick, children }: { label: string; active: boolean; expanded: boolean; badge?: number; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      aria-haspopup="true"
      aria-expanded={expanded}
      title={label}
      onClick={onClick}
      className={cn(
        "relative flex h-11 w-11 shrink-0 items-center justify-center rounded-xl transition-colors hover:bg-neutral-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30",
        active ? "bg-neutral-200 text-foreground" : "text-neutral-700",
      )}
    >
      {children}
      {badge ? <span className="absolute right-1 top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-semibold text-primary-foreground">{badge}</span> : null}
    </button>
  );
}

// The search, filter and sort controls above the entries, with Add at the end.
// Search is debounced: a keystroke waits briefly before the list is asked again.
// Filter and sort open small panels; a click outside or Escape closes them. On a
// phone the panels open full width under the controls, so they stay in reach.
export function FinanceToolbar({
  view,
  categories,
  onChange,
  onAdd,
}: {
  view: EntryView;
  categories: string[];
  onChange: (change: Partial<EntryView>) => void;
  onAdd: () => void;
}) {
  const [draft, setDraft] = useState(view.query);
  const [menu, setMenu] = useState<Menu>(null);
  const wrap = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (draft === view.query) return;
    const timer = setTimeout(() => onChange({ query: draft }), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [draft]);

  useEffect(() => {
    if (menu === null) return;
    const onPointer = (event: MouseEvent) => {
      if (!wrap.current?.contains(event.target as Node)) setMenu(null);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenu(null);
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [menu]);

  const toggle = (next: Exclude<Menu, null>) => setMenu((open) => (open === next ? null : next));

  return (
    <div ref={wrap} className="flex items-center gap-2">
      <div className="relative min-w-0 flex-1">
        <SearchIcon className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-neutral-500" />
        <input
          type="search"
          aria-label="Search entries"
          placeholder="Search"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          className={cn(FIELD_COMPACT, "h-11 pl-9")}
        />
      </div>

      <div className="relative">
        <ToolButton label="Filter entries" active={isFiltered(view)} badge={filterCount(view)} expanded={menu === "filter"} onClick={() => toggle("filter")}>
          <SlidersIcon className="h-[18px] w-[18px]" />
        </ToolButton>
        {menu === "filter" && (
          <div className="absolute right-0 top-full z-20 mt-2 flex w-[min(15rem,calc(100vw-2.5rem))] flex-col gap-3 rounded-2xl border border-neutral-200 bg-card p-3 shadow-lg">
            <label className={`flex flex-col gap-1.5 ${FIELD_LABEL}`}>
              Kind
              <Select value={view.kind} onChange={(event) => onChange({ kind: event.target.value })} aria-label="Kind">
                <option value="">All kinds</option>
                {KINDS.map((kind) => (
                  <option key={kind} value={kind}>
                    {KIND_LABEL[kind]}
                  </option>
                ))}
              </Select>
            </label>
            <label className={`flex flex-col gap-1.5 ${FIELD_LABEL}`}>
              Category
              <Select value={view.category} onChange={(event) => onChange({ category: event.target.value })} aria-label="Category">
                <option value="">All categories</option>
                {categories.map((category) => (
                  <option key={category} value={category}>
                    {category}
                  </option>
                ))}
              </Select>
            </label>
          </div>
        )}
      </div>

      <div className="relative">
        <ToolButton label="Sort entries" active={false} expanded={menu === "sort"} onClick={() => toggle("sort")}>
          <ArrowsUpDownIcon className="h-[18px] w-[18px]" />
        </ToolButton>
        {menu === "sort" && (
          <div role="menu" aria-label="Sort by" className="absolute right-0 top-full z-20 mt-2 w-44 rounded-2xl border border-neutral-200 bg-card p-1.5 shadow-lg">
            {SORT_KEYS.map((key) => {
              const active = view.sort === key;
              return (
                <button
                  key={key}
                  type="button"
                  role="menuitemradio"
                  aria-checked={active}
                  onClick={() => {
                    const next = chooseSort(view, key);
                    onChange({ sort: next.sort, order: next.order });
                    setMenu(null);
                  }}
                  className="flex min-h-11 w-full items-center justify-between rounded-xl px-3 py-2 text-left text-[13.5px] transition-colors hover:bg-neutral-100"
                >
                  <span>{SORTS[key].label}</span>
                  {active && <span className="text-xs text-neutral-700">{view.order === "asc" ? "↑" : "↓"}</span>}
                </button>
              );
            })}
          </div>
        )}
      </div>

      <button type="button" onClick={onAdd} className={cn(PRIMARY_BUTTON, "flex h-11 items-center gap-1.5")}>
        <PlusIcon className="h-4 w-4" />
        <span className="hidden sm:inline">Add</span>
        <ChevronDownIcon className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}
