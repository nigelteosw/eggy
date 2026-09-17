// A one-record section rendered as label/value rows: the shape a config
// section with a single row (heartbeat, tracing) takes, where a table with
// one row would be a header over nothing.
export function SummaryRows({ headers, row }: { headers: string[]; row: string[] }) {
  return (
    <div className="mb-3 flex flex-col">
      {headers.map((label, i) => (
        <div
          key={label}
          className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-1 py-3.5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]"
        >
          <span className="min-w-0 text-sm">{label}</span>
          <span className="min-w-0 text-right text-[13.5px] tabular-nums text-neutral-700 [overflow-wrap:anywhere]">{row[i]}</span>
        </div>
      ))}
    </div>
  );
}
