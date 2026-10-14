import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import {
  SessionExpiredError,
  createFinanceEntry,
  deleteFinanceEntry,
  getFinanceEntries,
  getFinanceSummary,
  updateFinanceCategory,
  updateFinanceEntry,
  type FinanceEntry,
  type FinanceEntryList,
  type FinanceInput,
  type FinancePatch,
  type FinanceSummary,
} from "./api";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD_COMPACT, FIELD_LABEL, PRIMARY_BUTTON, SECONDARY_BUTTON } from "./components/ui/form";
import { CloseIcon } from "./components/ui/icons";
import { Input } from "./components/ui/input";
import { Select } from "./components/ui/select";
import { CategoriesPanel, CategoryChip } from "./FinanceCategories";
import { CurrencySwitch, DailyStrip, Insights, Overview, Spending, Trend } from "./FinanceOverview";
import { FinanceToolbar } from "./FinanceToolbar";
import {
  CATEGORY_SUGGESTIONS,
  KIND_LABEL,
  KINDS,
  activeCurrency,
  entryTitle,
  forCurrency,
  isCashIn,
  monthLabel,
  shiftMonth,
  signedAmount,
  type Kind,
} from "./lib/finance";
import { DEFAULT_VIEW, isFiltered, listRequest, type EntryView } from "./lib/finance-list";
import { cn, errorMessage } from "./lib/utils";

// Money is text all the way down: the server formats every amount and sizes
// every bar, so nothing here adds, compares or converts it.

function Field({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return (
    <label className={`flex flex-col gap-1.5 ${FIELD_LABEL} ${className ?? ""}`}>
      {label}
      {children}
    </label>
  );
}

// The four things money can do, named the way the owner thinks of them. Spent
// and Received are the everyday ones, so they lead.
function KindPicker({ value, onChange }: { value: Kind; onChange: (kind: Kind) => void }) {
  return (
    <div role="radiogroup" aria-label="Kind" className="grid grid-cols-2 gap-1 rounded-2xl bg-background p-1 sm:grid-cols-4">
      {KINDS.map((kind) => (
        <button
          key={kind}
          type="button"
          role="radio"
          aria-checked={kind === value}
          onClick={() => onChange(kind)}
          className={cn(
            "min-h-10 rounded-xl px-3 text-[13px] font-medium transition-colors",
            kind === value ? "bg-neutral-200 text-foreground" : "text-neutral-700 hover:text-foreground",
          )}
        >
          {KIND_LABEL[kind]}
        </button>
      ))}
    </div>
  );
}

function KindSelect({ value, onChange }: { value: string; onChange: (kind: Kind) => void }) {
  return (
    <Select value={value} onChange={(event) => onChange(event.target.value as Kind)} aria-label="Kind">
      {KINDS.map((kind) => (
        <option key={kind} value={kind}>
          {KIND_LABEL[kind]}
        </option>
      ))}
    </Select>
  );
}

// The categories offered as suggestions in the form: the ones already in use,
// then the starter set, with no repeats.
function CategoryList({ categories }: { categories: string[] }) {
  const names = [...new Set([...categories, ...CATEGORY_SUGGESTIONS])];
  return (
    <datalist id="finance-categories">
      {names.map((category) => (
        <option key={category} value={category} />
      ))}
    </datalist>
  );
}

export function AddEntryForm({
  currencies,
  defaultCurrency,
  categories,
  busy,
  open,
  onOpenChange,
  onCreate,
}: {
  currencies: string[];
  defaultCurrency: string;
  categories: string[];
  busy: boolean;
  // Optional so the form opens and closes on its own unless the page, as the
  // toolbar's Add button does, takes that over.
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  // Resolves true when the entry was recorded, so the form clears only then
  // and a refused entry keeps what was typed.
  onCreate: (input: FinanceInput) => Promise<boolean>;
}) {
  const [kind, setKind] = useState<Kind>("expense");
  const [amount, setAmount] = useState("");
  const [currency, setCurrency] = useState(defaultCurrency);
  const [date, setDate] = useState("");
  const [category, setCategory] = useState("");
  const [merchant, setMerchant] = useState("");
  const [note, setNote] = useState("");

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    const input: FinanceInput = { amount, kind, category, currency };
    if (date) input.date = date;
    if (merchant) input.merchant = merchant;
    if (note) input.note = note;
    if (await onCreate(input)) {
      setAmount("");
      setCategory("");
      setMerchant("");
      setNote("");
    }
  }

  return (
    <details id="finance-add-entry" className="rounded-2xl bg-neutral-100 p-4" open={open} onToggle={(event) => onOpenChange?.(event.currentTarget.open)}>
      <summary className="cursor-pointer text-[13.5px] font-medium">Add an entry</summary>
      <form onSubmit={submit} className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="sm:col-span-2">
          <KindPicker value={kind} onChange={setKind} />
        </div>
        <Field label="Amount">
          <Input required inputMode="decimal" placeholder="14.50" value={amount} onChange={(e) => setAmount(e.target.value)} className={FIELD_COMPACT} />
        </Field>
        <Field label="Currency">
          <Select value={currency} onChange={(e) => setCurrency(e.target.value)} aria-label="Currency">
            {currencies.map((code) => (
              <option key={code} value={code}>
                {code}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Category">
          <Input required list="finance-categories" placeholder="food" value={category} onChange={(e) => setCategory(e.target.value)} className={FIELD_COMPACT} />
        </Field>
        <Field label="Date (blank is today)">
          <Input type="date" value={date} onChange={(e) => setDate(e.target.value)} className={FIELD_COMPACT} />
        </Field>
        <Field label="Merchant">
          <Input value={merchant} onChange={(e) => setMerchant(e.target.value)} className={FIELD_COMPACT} />
        </Field>
        <Field label="Note" className="sm:col-span-2">
          <Input value={note} onChange={(e) => setNote(e.target.value)} className={FIELD_COMPACT} />
        </Field>
        <div className="sm:col-span-2">
          <button type="submit" disabled={busy} className={cn(PRIMARY_BUTTON, "w-full sm:w-auto")}>
            {busy ? "Saving..." : "Add entry"}
          </button>
        </div>
      </form>
      <CategoryList categories={categories} />
    </details>
  );
}

function EntryEditor({
  entry,
  currencies,
  categories,
  busy,
  onSave,
  onCancel,
}: {
  entry: FinanceEntry;
  currencies: string[];
  categories: string[];
  busy: boolean;
  onSave: (patch: FinancePatch) => Promise<void>;
  onCancel: () => void;
}) {
  const [kind, setKind] = useState(entry.kind as Kind);
  const [amount, setAmount] = useState(entry.amount);
  const [currency, setCurrency] = useState(entry.currency);
  const [date, setDate] = useState(entry.date);
  const [category, setCategory] = useState(entry.category);
  const [merchant, setMerchant] = useState(entry.merchant);
  const [note, setNote] = useState(entry.note);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    // Only what changed is sent. The exception is the amount: a number means
    // something different in another currency, so changing the currency
    // restates it, and the server refuses a currency change that does not.
    const patch: FinancePatch = {};
    if (kind !== entry.kind) patch.kind = kind;
    if (amount !== entry.amount || currency !== entry.currency) patch.amount = amount;
    if (currency !== entry.currency) patch.currency = currency;
    if (date !== entry.date) patch.date = date;
    if (category !== entry.category) patch.category = category;
    if (merchant !== entry.merchant) patch.merchant = merchant;
    if (note !== entry.note) patch.note = note;
    if (Object.keys(patch).length === 0) {
      onCancel();
      return;
    }
    await onSave(patch);
  }

  return (
    <form onSubmit={submit} className="grid w-full grid-cols-1 gap-3 sm:grid-cols-2">
      <Field label="Kind">
        <KindSelect value={kind} onChange={setKind} />
      </Field>
      <Field label="Amount">
        <Input required inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} className={FIELD_COMPACT} />
      </Field>
      <Field label="Currency">
        <Select value={currency} onChange={(e) => setCurrency(e.target.value)} aria-label="Currency">
          {currencies.map((code) => (
            <option key={code} value={code}>
              {code}
            </option>
          ))}
        </Select>
      </Field>
      <Field label="Category">
        <Input required list="finance-categories" value={category} onChange={(e) => setCategory(e.target.value)} className={FIELD_COMPACT} />
      </Field>
      <Field label="Date">
        <Input required type="date" value={date} onChange={(e) => setDate(e.target.value)} className={FIELD_COMPACT} />
      </Field>
      <Field label="Merchant">
        <Input value={merchant} onChange={(e) => setMerchant(e.target.value)} className={FIELD_COMPACT} />
      </Field>
      <Field label="Note" className="sm:col-span-2">
        <Input value={note} onChange={(e) => setNote(e.target.value)} className={FIELD_COMPACT} />
      </Field>
      <div className="flex gap-2 sm:col-span-2">
        <button type="submit" disabled={busy} className={PRIMARY_BUTTON}>
          Save
        </button>
        <button type="button" onClick={onCancel} className={SECONDARY_BUTTON}>
          Cancel
        </button>
      </div>
      <CategoryList categories={categories} />
    </form>
  );
}

// The presentational half: everything on the page below the month switcher,
// from fixtures, so what it draws can be checked without a server.
export function FinanceReport({
  summary,
  currency,
  onCurrency,
  entries,
  total,
  editingId,
  busy,
  onEdit,
  onSave,
  onDelete,
  listControls,
  filtered = false,
}: {
  summary: FinanceSummary;
  currency: string;
  onCurrency: (code: string) => void;
  entries: FinanceEntry[];
  total: number;
  editingId: string | null;
  busy: boolean;
  onEdit: (id: string | null) => void;
  onSave: (id: string, patch: FinancePatch) => Promise<void>;
  onDelete: (id: string) => void;
  // The search, filter and sort controls, drawn above the entries. A search can match
  // nothing, so the section stays while filtered even with no rows to show.
  listControls?: ReactNode;
  filtered?: boolean;
}) {
  const flow = summary.flows.find((row) => row.currency === currency);
  const empty = summary.flows.length === 0 && entries.length === 0;
  const colors = new Map(summary.categories.map((category) => [category.name, category.color]));
  const categoryNames = summary.categories.map((category) => category.name);

  return (
    <>
      {empty ? (
        <p className="rounded-2xl bg-neutral-100 px-5 py-8 text-center text-sm leading-6 text-neutral-700">
          {`No money logged in ${monthLabel(summary.month)} yet. Tell Eggy what you spent or earned, or add an entry below.`}
        </p>
      ) : (
        <>
          {summary.flows.length > 1 && <CurrencySwitch currencies={summary.flows.map((row) => row.currency)} value={currency} onChange={onCurrency} />}
          {flow && <Overview flow={flow} currency={currency} />}
          {currency === summary.default_currency && <Insights items={summary.insights} />}
          <Trend points={forCurrency(summary.trend, currency)} current={summary.month} />
          <Spending lines={forCurrency(summary.spending, currency)} currency={currency} />
          {flow && <DailyStrip days={forCurrency(summary.days, currency)} flow={flow} currency={currency} from={summary.from} to={summary.to} />}
        </>
      )}

      {(entries.length > 0 || filtered) && (
        <section className="flex flex-col gap-3">
          <CardHeader title="Entries" />
          {listControls}
          {entries.length === 0 && <p className="rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">No entries match.</p>}
          <ul className="flex flex-col gap-2">
            {entries.map((entry) => (
              <li key={entry.id} className="flex flex-col gap-3 rounded-2xl bg-neutral-100 px-4 py-3">
                {editingId === entry.id ? (
                  <EntryEditor
                    entry={entry}
                    currencies={summary.currencies}
                    categories={categoryNames}
                    busy={busy}
                    onSave={(patch) => onSave(entry.id, patch)}
                    onCancel={() => onEdit(null)}
                  />
                ) : (
                  <>
                    <div className="flex items-start gap-3">
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-sm font-medium">{entryTitle(entry)}</p>
                        <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-neutral-700">
                          <CategoryChip name={entry.category} color={colors.get(entry.category) ?? ""} />
                          <span className="tabular-nums">{entry.date}</span>
                          {entry.note && <span className="truncate">{entry.note}</span>}
                        </div>
                      </div>
                      <div className="shrink-0 text-right">
                        <p className={cn("text-sm font-semibold tabular-nums", isCashIn(entry.kind) && "text-emerald-700 dark:text-emerald-400")}>
                          {signedAmount(entry.kind, entry.currency, entry.amount)}
                        </p>
                        <p className="mt-0.5 text-[11px] text-neutral-700">{KIND_LABEL[entry.kind as Kind] ?? entry.kind}</p>
                      </div>
                    </div>
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-[11px] text-neutral-700">{`via ${entry.source}`}</span>
                      <div className="flex gap-1">
                        <button type="button" onClick={() => onEdit(entry.id)} className={SECONDARY_BUTTON}>
                          Edit
                        </button>
                        <button type="button" disabled={busy} onClick={() => onDelete(entry.id)} className={SECONDARY_BUTTON}>
                          Delete
                        </button>
                      </div>
                    </div>
                  </>
                )}
              </li>
            ))}
          </ul>
          {total > entries.length && <p className="text-xs text-neutral-700">{`${total - entries.length} more not shown`}</p>}
        </section>
      )}
    </>
  );
}

// onClose is set when the page is opened as an extension window, which returns
// to the view underneath. The page draws its own heading, the way Scheduled does.
export function FinancePage({ onSessionExpired, onClose }: { onSessionExpired: () => void; onClose?: () => void }) {
  // An empty month asks the server for the owner's current one; after the
  // first answer the switcher steps from the month it reports.
  const [month, setMonth] = useState("");
  const [summary, setSummary] = useState<FinanceSummary | null>(null);
  const [list, setList] = useState<FinanceEntryList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  // The currency the owner picked for the figures; null follows the server's choice.
  const [currency, setCurrency] = useState<string | null>(null);
  // The search, filter and sort the list is asked for. Changing it asks only
  // for the list again; the month's totals do not depend on it.
  const [view, setView] = useState<EntryView>(DEFAULT_VIEW);
  // Only the newest load may land: stepping through months quickly, or
  // retyping a search, must not end on whichever response arrived last.
  const latest = useRef(0);

  const fail = useCallback(
    (err: unknown, fallback: string) => {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, fallback));
    },
    [onSessionExpired],
  );

  // Loads a month's totals and its entries as the view asks for them.
  const loadMonth = useCallback(
    async (target: string, next: EntryView) => {
      const mine = ++latest.current;
      try {
        const nextSummary = await getFinanceSummary(target || undefined);
        const nextList = await getFinanceEntries(listRequest(nextSummary, next));
        if (mine !== latest.current) return;
        setSummary(nextSummary);
        setList(nextList);
        setError(null);
      } catch (err) {
        if (mine === latest.current) fail(err, "Could not load finance");
      }
    },
    [fail],
  );

  // Asks for the list again under the current search and sort, for the month
  // already on screen.
  const loadList = useCallback(
    async (range: FinanceSummary, next: EntryView) => {
      const mine = ++latest.current;
      try {
        const nextList = await getFinanceEntries(listRequest(range, next));
        if (mine !== latest.current) return;
        setList(nextList);
        setError(null);
      } catch (err) {
        if (mine === latest.current) fail(err, "Could not load finance");
      }
    },
    [fail],
  );

  // The view is read here without being listed: changing it has its own effect
  // below, so moving between months never fetches the list twice.
  useEffect(() => {
    void loadMonth(month, view);
  }, [loadMonth, month]);

  // Nothing to refine until a month has loaded; the month load above fetches it.
  useEffect(() => {
    if (summary) void loadList(summary, view);
  }, [view]);

  // Runs a write, then reloads what the page shows. Resolves true on success.
  async function mutate(write: () => Promise<unknown>, failure: string): Promise<boolean> {
    setBusy(true);
    setError(null);
    try {
      await write();
      await loadMonth(summary?.month ?? month, view);
      return true;
    } catch (err) {
      fail(err, failure);
      return false;
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="app-canvas scrollbar-slim h-full min-h-0 overflow-y-auto">
      <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-4 pb-11 pt-8 sm:px-7 sm:pt-10">
        <header className="flex flex-col gap-3 pb-1">
          <div className="flex items-center justify-between gap-3">
            <h1 className="text-xl font-semibold tracking-tight">Finance</h1>
            {onClose && (
              <button
                type="button"
                onClick={onClose}
                aria-label="Close Finance"
                title="Close"
                className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              >
                <CloseIcon />
              </button>
            )}
          </div>
          {summary && (
            <div className="flex items-center gap-1 self-start">
              <button type="button" aria-label="Previous month" onClick={() => setMonth(shiftMonth(summary.month, -1))} className={SECONDARY_BUTTON}>
                ‹
              </button>
              <span className="min-w-[8.5rem] text-center text-sm font-medium">{monthLabel(summary.month)}</span>
              <button type="button" aria-label="Next month" onClick={() => setMonth(shiftMonth(summary.month, 1))} className={SECONDARY_BUTTON}>
                ›
              </button>
            </div>
          )}
        </header>
        {error && <ErrorBanner>{error}</ErrorBanner>}
        {!summary || !list ? (
          !error && <p className="text-sm text-muted-foreground">Loading...</p>
        ) : (
          <>
            <FinanceReport
              summary={summary}
              currency={activeCurrency(summary, currency)}
              onCurrency={setCurrency}
              entries={list.entries}
              total={list.total}
              editingId={editingId}
              busy={busy}
              filtered={isFiltered(view)}
              listControls={
                <FinanceToolbar
                  view={view}
                  categories={summary.categories.map((category) => category.name)}
                  onChange={(change) => setView((current) => ({ ...current, ...change }))}
                  onAdd={() => {
                    setAddOpen(true);
                    document.getElementById("finance-add-entry")?.scrollIntoView({ block: "center", behavior: "smooth" });
                  }}
                />
              }
              onEdit={setEditingId}
              onSave={async (id, patch) => {
                if (await mutate(() => updateFinanceEntry(id, patch), "Could not save the entry")) setEditingId(null);
              }}
              onDelete={(id) => {
                if (!window.confirm("Delete this entry?")) return;
                void mutate(() => deleteFinanceEntry(id), "Could not delete the entry");
              }}
            />
            <AddEntryForm
              currencies={summary.currencies}
              defaultCurrency={summary.default_currency}
              categories={summary.categories.map((category) => category.name)}
              busy={busy}
              open={addOpen}
              onOpenChange={setAddOpen}
              onCreate={(input) => mutate(() => createFinanceEntry(input), "Could not add the entry")}
            />
            <CategoriesPanel
              categories={summary.categories}
              palette={summary.palette}
              busy={busy}
              onColour={(name, color) => void mutate(() => updateFinanceCategory(name, { color }), "Could not save the colour")}
              onRename={(from, to) => mutate(() => updateFinanceCategory(from, { rename: to }), "Could not rename the category")}
            />
          </>
        )}
      </div>
    </div>
  );
}
