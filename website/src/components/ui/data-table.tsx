import * as React from "react";
import { cn } from "../../lib/utils";

/**
 * The config cards all render the same shape: a CommandResult's
 * table_headers/table_rows, or an empty-state line when there's nothing
 * configured yet. This keeps that presentation in one place.
 */
export function DataTable({
  headers,
  rows,
  empty,
  renderRowAction,
  className,
}: {
  headers?: string[];
  rows?: string[][];
  empty: string;
  renderRowAction?: (row: string[]) => React.ReactNode;
  className?: string;
}) {
  if (!rows || rows.length === 0) {
    return (
      <div className={cn("rounded-2xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700", className)}>
        {empty}
      </div>
    );
  }

  return (
    <div className={cn("rounded-2xl bg-neutral-100", className)}>
      {/* Wide screens keep the grid. A phone cannot show four columns without
          sideways scrolling, so below the breakpoint each row becomes a card
          whose values carry their column's label. */}
      <div className="scrollbar-slim hidden overflow-x-auto sm:block">
        <table className="w-full border-collapse text-left text-sm">
          <thead>
            <tr>
              {headers?.map((header) => (
                <th
                  key={header}
                  className="whitespace-nowrap px-4 py-2.5 text-[10.5px] font-semibold uppercase tracking-[0.05em] text-neutral-700"
                >
                  {header}
                </th>
              ))}
              {renderRowAction && <th className="sticky right-0 z-10 bg-neutral-100 px-4 py-2.5" />}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, index) => (
              <tr key={row[0] ?? index} className="shadow-[inset_0_1px_0_hsl(var(--neutral-200))]">
                {row.map((cell, cellIndex) => (
                  <td
                    key={cellIndex}
                    className={cn(
                      "px-4 py-3 align-middle",
                      cellIndex === 0 ? "font-medium text-foreground" : "text-neutral-700",
                    )}
                  >
                    {cell}
                  </td>
                ))}
                {renderRowAction && <td className="sticky right-0 z-10 bg-neutral-100 px-4 py-2.5 text-right">{renderRowAction(row)}</td>}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ul className="flex flex-col sm:hidden">
        {rows.map((row, index) => (
          <li key={row[0] ?? index} className="flex flex-col gap-2 px-4 py-3.5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))] first:shadow-none">
            <span className="break-words text-sm font-medium text-foreground">{row[0]}</span>
            {row.slice(1).map((cell, cellIndex) => (
              <span key={cellIndex + 1} className="flex min-w-0 items-baseline justify-between gap-4 text-xs">
                <span className="shrink-0 text-neutral-700">{headers?.[cellIndex + 1] ?? ""}</span>
                <span className="min-w-0 break-words text-right text-foreground">{cell}</span>
              </span>
            ))}
            {renderRowAction && <span className="flex justify-end pt-1">{renderRowAction(row)}</span>}
          </li>
        ))}
      </ul>
    </div>
  );
}
