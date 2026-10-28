import { CardHeader } from "../../components/ui/card-header";
import type { FinanceFlow, FinanceSpending, FinanceTrendPoint, FinanceDay } from "../../lib/api";
import { CategoryDot } from "./FinanceCategories";
import { categoryHex, daysOfRange, dayLabel, entriesWord, money, monthShort, movement, percentOf, type Movement } from "./finance";
import { cn } from "../../lib/utils";

// The figures and charts for one currency. Everything here is drawn from values
// the server computed: amounts as text, bars and changes as shares and
// percentages, so the page only places and labels them.

const TONE: Record<Movement["tone"], string> = {
  good: "text-emerald-700 dark:text-emerald-400",
  bad: "text-eg-red-ink",
  neutral: "text-neutral-700",
};
const ARROW: Record<Movement["arrow"], string> = { up: "▲", down: "▼", flat: "–" };

// A change against last month, in its colour and with its arrow.
function MovementText({ move }: { move: Movement }) {
  return (
    <span className={cn("whitespace-nowrap font-medium tabular-nums", TONE[move.tone])}>
      {`${ARROW[move.arrow]} ${move.text}`}
    </span>
  );
}

export function CurrencySwitch({ currencies, value, onChange }: { currencies: string[]; value: string; onChange: (code: string) => void }) {
  return (
    <div role="tablist" aria-label="Currency" className="flex gap-1 self-start rounded-full bg-neutral-100 p-1">
      {currencies.map((code) => (
        <button
          key={code}
          type="button"
          role="tab"
          aria-selected={code === value}
          onClick={() => onChange(code)}
          className={cn(
            "min-h-10 rounded-full px-4 text-[12.5px] font-medium tabular-nums transition-colors",
            code === value ? "bg-background text-foreground shadow-subtle" : "text-neutral-700 hover:text-foreground",
          )}
        >
          {code}
        </button>
      ))}
    </div>
  );
}

function Tile({ label, value, move, note }: { label: string; value: string; move?: Movement | null; note?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5 rounded-2xl bg-neutral-100 p-4">
      <p className="text-[11.5px] text-neutral-700">{label}</p>
      <p className="truncate text-[17px] font-semibold tabular-nums tracking-tight">{value}</p>
      <p className="truncate text-[11.5px] text-neutral-700">
        {move ? (
          <>
            <MovementText move={move} /> vs last month
          </>
        ) : (
          note
        )}
      </p>
    </div>
  );
}

// The month at a glance: what was left over, the savings rate, and the four
// figures that make it up.
export function Overview({ flow, currency }: { flow: FinanceFlow; currency: string }) {
  const net = movement(flow.change.net, true);
  const negative = flow.net.startsWith("-");
  return (
    <section aria-label="Overview" className="flex flex-col gap-3">
      <div className="rounded-3xl bg-neutral-100 p-5 sm:p-6">
        <p className="text-[11.5px] font-medium uppercase tracking-wide text-neutral-700">Left over this month</p>
        <p className={cn("mt-2 text-[34px] font-semibold leading-none tracking-tight tabular-nums sm:text-[40px]", negative && "text-eg-red-ink")}>
          {money(currency, flow.net)}
        </p>
        <p className="mt-2 text-[12px] text-neutral-700">After spending and investing.</p>
        <div className="mt-4 flex flex-wrap items-center gap-2 text-[12.5px]">
          {flow.savings_rate !== null && (
            <span className="rounded-full bg-background px-3 py-1 font-medium tabular-nums">{`${flow.savings_rate}% saved`}</span>
          )}
          {net && (
            <span className="text-neutral-700">
              <MovementText move={net} /> vs last month
            </span>
          )}
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Tile label="Money in" value={money(currency, flow.inflow)} move={movement(flow.change.inflow, true)} />
        <Tile label="Spent" value={money(currency, flow.outflow)} move={movement(flow.change.outflow, false)} />
        <Tile label="Invested" value={money(currency, flow.invested)} move={movement(flow.change.invested, null)} note={`${money(currency, flow.net_invested)} added`} />
        <Tile label="A day, on average" value={money(currency, flow.average_day)} note={entriesWord(flow.count)} />
      </div>
    </section>
  );
}

export function Insights({ items }: { items: string[] }) {
  if (items.length === 0) return null;
  return (
    <ul aria-label="Insights" className="flex flex-col gap-2">
      {items.map((text) => (
        <li key={text} className="flex items-start gap-3 rounded-2xl bg-neutral-100 px-4 py-3 text-[13.5px] leading-6">
          <span aria-hidden className="mt-2.5 h-1.5 w-1.5 shrink-0 rounded-full bg-primary" />
          {text}
        </li>
      ))}
    </ul>
  );
}

// Money in and out for each of the last six months, as a pair of bars on one
// scale, with the month's net beneath.
export function Trend({ points, current }: { points: FinanceTrendPoint[]; current: string }) {
  if (points.length === 0) return null;
  return (
    <section className="flex flex-col gap-3">
      <CardHeader title="Last six months" description="Money in and out, month by month." />
      <div className="rounded-2xl bg-neutral-100 p-4">
        <div className="mb-4 flex gap-4 text-[11.5px] text-neutral-700">
          <span className="flex items-center gap-1.5">
            <span aria-hidden className="h-2.5 w-2.5 rounded-sm bg-eg-green" /> In
          </span>
          <span className="flex items-center gap-1.5">
            <span aria-hidden className="h-2.5 w-2.5 rounded-sm bg-eg-red" /> Out
          </span>
        </div>
        <ol aria-label="Money in and out by month" className="grid gap-2" style={{ gridTemplateColumns: `repeat(${points.length}, minmax(0, 1fr))` }}>
          {points.map((point) => (
            <li
              key={point.month}
              className="flex min-w-0 flex-col items-center gap-2"
              title={`${monthShort(point.month)}: in ${point.inflow}, out ${point.outflow}, net ${point.net}`}
            >
              <div className="flex h-36 w-full items-end justify-center gap-1">
                <span className="w-2.5 rounded-t-md bg-eg-green sm:w-3.5" style={{ height: percentOf(point.inflow_share) }} />
                <span className="w-2.5 rounded-t-md bg-eg-red sm:w-3.5" style={{ height: percentOf(point.outflow_share) }} />
              </div>
              <span className={cn("text-[11.5px]", point.month === current ? "font-semibold text-foreground" : "text-neutral-700")}>
                {monthShort(point.month)}
              </span>
              <span className="w-full truncate text-center text-[10.5px] tabular-nums text-neutral-700">{point.net}</span>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

// Spending split by category: one bar showing the whole, then each category with
// its amount, its share, and how it moved against last month.
export function Spending({ lines, currency }: { lines: FinanceSpending[]; currency: string }) {
  return (
    <section className="flex flex-col gap-3">
      <CardHeader title="Where it went" description={`${currency}, by category, against last month.`} />
      <div className="flex flex-col gap-4 rounded-2xl bg-neutral-100 p-4">
        {lines.length === 0 ? (
          <p className="text-sm text-neutral-700">{`Nothing spent in ${currency} this month.`}</p>
        ) : (
          <>
            <div role="img" aria-label="Spending split by category" className="flex h-3 overflow-hidden rounded-full bg-neutral-200">
              {lines.map((line) => (
                <span key={line.category} title={line.category} className="h-full" style={{ width: percentOf(line.share), backgroundColor: categoryHex(line.color) }} />
              ))}
            </div>
            <ul className="flex flex-col divide-y divide-neutral-200">
              {lines.map((line) => {
                const move = movement(line.change, false);
                return (
                  <li key={line.category} className="flex items-center gap-3 py-3 first:pt-0 last:pb-0" title={`Last month: ${currency} ${line.previous}`}>
                    <CategoryDot color={line.color} className="h-3 w-3" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">{line.category}</p>
                      <p className="flex flex-wrap items-center gap-x-2 text-[11.5px] text-neutral-700">
                        <span>{entriesWord(line.count)}</span>
                        {move && <MovementText move={move} />}
                      </p>
                    </div>
                    <div className="text-right">
                      <p className="text-sm font-semibold tabular-nums">{line.amount}</p>
                      <p className="text-[11.5px] tabular-nums text-neutral-700">{`${Math.round(line.share * 100)}%`}</p>
                    </div>
                  </li>
                );
              })}
            </ul>
          </>
        )}
      </div>
    </section>
  );
}

// Each day of the month as a bar, with the average day drawn across them as a
// dashed line, so a day that ran above the usual one is visible at a glance.
export function DailyStrip({ days, flow, currency, from, to }: { days: FinanceDay[]; flow: FinanceFlow; currency: string; from: string; to: string }) {
  const rows = new Map(days.map((row) => [row.day, row]));
  return (
    <section className="flex flex-col gap-3">
      <CardHeader title="Daily spending" description={`${currency}. The dashed line is the average day so far, ${money(currency, flow.average_day)}.`} />
      <div className="rounded-2xl bg-neutral-100 p-4">
        <div className="relative">
          <ol data-testid="day-strip" className="flex h-28 items-end gap-0.5">
            {daysOfRange(from, to).map((day) => {
              const row = rows.get(day);
              return (
                <li
                  key={day}
                  title={row ? `${dayLabel(day)}: ${money(currency, row.amount)}` : `${dayLabel(day)}: nothing`}
                  className={cn("min-w-0 flex-1 rounded-sm", row ? "bg-primary/80" : "bg-neutral-200")}
                  style={{ height: row ? `${Math.max(row.share * 100, 4)}%` : "4%" }}
                />
              );
            })}
          </ol>
          <span aria-hidden className="pointer-events-none absolute inset-x-0 border-t border-dashed border-foreground/50" style={{ bottom: percentOf(flow.average_share) }} />
        </div>
        <div className="mt-2 flex justify-between text-[11px] text-neutral-700">
          <span>{dayLabel(from)}</span>
          <span>{dayLabel(to)}</span>
        </div>
      </div>
    </section>
  );
}
