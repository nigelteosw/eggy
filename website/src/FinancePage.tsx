import { useCallback, useEffect, useRef, useState } from "react";
import {
  SessionExpiredError,
  createFinanceEntry,
  deleteFinanceEntry,
  getFinanceEntries,
  getFinanceSummary,
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
import { Input } from "./components/ui/input";
import { Select } from "./components/ui/select";
import { CATEGORY_SUGGESTIONS, dayLabel, daysOfRange, monthLabel, shiftMonth } from "./lib/finance";
import { errorMessage } from "./lib/utils";

// Money is text all the way down: the server formats every amount and sizes
// every bar, so nothing here adds, compares or converts it.

const entriesWord = (count: number) => `${count} ${count === 1 ? "entry" : "entries"}`;
const percent = (share: number) => `${(share * 100).toFixed(2)}%`;

function Field({ label, children, className }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <label className={`flex flex-col gap-1.5 ${FIELD_LABEL} ${className ?? ""}`}>
      {label}
      {children}
    </label>
  );
}

function CategoryList() {
  return (
    <datalist id="finance-categories">
      {CATEGORY_SUGGESTIONS.map((category) => (
        <option key={category} value={category} />
      ))}
    </datalist>
  );
}

export function AddEntryForm({
  currencies,
  defaultCurrency,
  busy,
  onCreate,
}: {
  currencies: string[];
  defaultCurrency: string;
  busy: boolean;
  // Resolves true when the entry was recorded, so the form clears only then
  // and a refused entry keeps what was typed.
  onCreate: (input: FinanceInput) => Promise<boolean>;
}) {
  const [amount, setAmount] = useState("");
  const [currency, setCurrency] = useState(defaultCurrency);
  const [date, setDate] = useState("");
  const [category, setCategory] = useState("");
  const [merchant, setMerchant] = useState("");
  const [note, setNote] = useState("");

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    const input: FinanceInput = { amount, category, currency };
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
    <details className="rounded-2xl bg-neutral-100 p-4">
      <summary className="cursor-pointer text-[12.5px] font-medium">Add entry</summary>
      <form onSubmit={submit} className="mt-3 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
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
        <Field label="Note">
          <Input value={note} onChange={(e) => setNote(e.target.value)} className={FIELD_COMPACT} />
        </Field>
        <div className="sm:col-span-2">
          <button type="submit" disabled={busy} className={PRIMARY_BUTTON}>
            {busy ? "Saving..." : "Add"}
          </button>
        </div>
      </form>
      <CategoryList />
    </details>
  );
}

function EntryEditor({
  entry,
  currencies,
  busy,
  onSave,
  onCancel,
}: {
  entry: FinanceEntry;
  currencies: string[];
  busy: boolean;
  onSave: (patch: FinancePatch) => Promise<void>;
  onCancel: () => void;
}) {
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
    <form onSubmit={submit} className="grid w-full grid-cols-1 gap-2.5 sm:grid-cols-2">
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
      <Field label="Note">
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
    </form>
  );
}

// The presentational half: everything on the page below the month switcher,
// from fixtures, so what it draws can be checked without a server.
export function FinanceReport({
  summary,
  entries,
  total,
  editingId,
  busy,
  onEdit,
  onSave,
  onDelete,
}: {
  summary: FinanceSummary;
  entries: FinanceEntry[];
  total: number;
  editingId: string | null;
  busy: boolean;
  onEdit: (id: string | null) => void;
  onSave: (id: string, patch: FinancePatch) => Promise<void>;
  onDelete: (id: string) => void;
}) {
  const empty = summary.totals.length === 0 && entries.length === 0;
  // The strip is drawn in one currency, because bars in two would be
  // comparing yen to dollars. The server puts the default first.
  const stripCurrency = summary.totals[0]?.currency ?? summary.default_currency;
  const dayRows = new Map(summary.by_day.filter((row) => row.currency === stripCurrency).map((row) => [row.day, row]));
  const several = summary.totals.length > 1;

  return (
    <>
      {empty ? (
        <p className="rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">
          {`No spending logged in ${monthLabel(summary.month)}. Tell Eggy what you spent, or send a receipt on Telegram.`}
        </p>
      ) : (
        <>
          <section aria-label="Totals" className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {summary.totals.map((row) => (
              <div key={row.currency} className="rounded-2xl bg-neutral-100 p-4">
                <p className="text-2xl font-semibold tracking-tight tabular-nums">{`${row.currency} ${row.amount}`}</p>
                <p className="mt-1 text-xs text-neutral-700">{entriesWord(row.count)}</p>
              </div>
            ))}
          </section>

          <section className="flex flex-col gap-3">
            <CardHeader title="By category" />
            {summary.totals.map((currencyTotal) => (
              <div key={currencyTotal.currency} className="flex flex-col gap-2 rounded-2xl bg-neutral-100 p-4">
                {several && <p className="text-[11.5px] font-semibold uppercase tracking-wide text-neutral-700">{currencyTotal.currency}</p>}
                {summary.by_category
                  .filter((row) => row.currency === currencyTotal.currency)
                  .map((row) => (
                    <div key={row.category} className="grid grid-cols-[7rem_1fr_auto] items-center gap-3 text-sm">
                      <span className="truncate">{row.category}</span>
                      <span className="h-2 rounded-full bg-neutral-200">
                        <span className="block h-2 rounded-full bg-primary/80" style={{ width: percent(row.share) }} />
                      </span>
                      <span className="tabular-nums text-neutral-700">{row.amount}</span>
                    </div>
                  ))}
              </div>
            ))}
          </section>

          <section className="flex flex-col gap-3">
            <CardHeader title="By day" description={`Spending in ${stripCurrency}, day by day.`} />
            <ol data-testid="day-strip" className="flex h-24 items-end gap-0.5 rounded-2xl bg-neutral-100 p-3">
              {daysOfRange(summary.from, summary.to).map((day) => {
                const row = dayRows.get(day);
                return (
                  <li
                    key={day}
                    title={row ? `${dayLabel(day)}: ${stripCurrency} ${row.amount}` : `${dayLabel(day)}: nothing`}
                    className={`min-w-0 flex-1 rounded-sm ${row ? "bg-primary/80" : "bg-neutral-200"}`}
                    style={{ height: row ? `${Math.max(row.share * 100, 4)}%` : "4%" }}
                  />
                );
              })}
            </ol>
          </section>
        </>
      )}

      {entries.length > 0 && (
        <section className="flex flex-col gap-3">
          <CardHeader title="Entries" />
          <ul className="flex flex-col gap-2">
            {entries.map((entry) => (
              <li key={entry.id} className="flex flex-col gap-2 rounded-2xl bg-neutral-100 px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
                {editingId === entry.id ? (
                  <EntryEditor
                    entry={entry}
                    currencies={summary.currencies}
                    busy={busy}
                    onSave={(patch) => onSave(entry.id, patch)}
                    onCancel={() => onEdit(null)}
                  />
                ) : (
                  <>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">{entry.merchant || entry.category}</p>
                      <p className="truncate text-xs text-neutral-700">{`${entry.date} · ${entry.category}${entry.note ? ` · ${entry.note}` : ""}`}</p>
                    </div>
                    <span className="w-fit rounded-full bg-neutral-200 px-2 py-0.5 text-[11px] text-neutral-700">{entry.source}</span>
                    <p className="text-sm font-semibold tabular-nums">{`${entry.currency} ${entry.amount}`}</p>
                    <div className="flex gap-1">
                      <button type="button" onClick={() => onEdit(entry.id)} className={SECONDARY_BUTTON}>
                        Edit
                      </button>
                      <button type="button" disabled={busy} onClick={() => onDelete(entry.id)} className={SECONDARY_BUTTON}>
                        Delete
                      </button>
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

export function FinancePage({ onSessionExpired }: { onSessionExpired: () => void }) {
  // An empty month asks the server for the owner's current one; after the
  // first answer the switcher steps from the month it reports.
  const [month, setMonth] = useState("");
  const [summary, setSummary] = useState<FinanceSummary | null>(null);
  const [list, setList] = useState<FinanceEntryList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  // Only the newest load may land: stepping through months quickly must not
  // end on whichever response happened to arrive last.
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

  const load = useCallback(
    async (target: string) => {
      const mine = ++latest.current;
      try {
        const nextSummary = await getFinanceSummary(target || undefined);
        const nextList = await getFinanceEntries({ from: nextSummary.from, to: nextSummary.to });
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

  useEffect(() => {
    void load(month);
  }, [load, month]);

  // Runs a write, then reloads what the page shows. Resolves true on success.
  async function mutate(write: () => Promise<unknown>, failure: string): Promise<boolean> {
    setBusy(true);
    setError(null);
    try {
      await write();
      await load(summary?.month ?? month);
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
      <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-5 pb-11 pt-6 sm:px-7">
        <header className="flex items-center justify-between gap-3">
          <h1 className="text-xl font-semibold tracking-tight">Finance</h1>
          {summary && (
            <div className="flex items-center gap-1">
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
              entries={list.entries}
              total={list.total}
              editingId={editingId}
              busy={busy}
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
              busy={busy}
              onCreate={(input) => mutate(() => createFinanceEntry(input), "Could not add the entry")}
            />
          </>
        )}
      </div>
    </div>
  );
}
