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
      <div className={cn("rounded-md border border-dashed border-border px-4 py-6 text-center text-sm text-muted-foreground", className)}>
        {empty}
      </div>
    );
  }

  return (
    <div className={cn("scrollbar-slim overflow-x-auto rounded-lg border border-border bg-background/35", className)}>
      <table className="w-full border-collapse text-left text-sm">
        <thead className="bg-muted/80">
          <tr>
            {headers?.map((header) => (
              <th
                key={header}
                className="whitespace-nowrap px-4 py-3 text-[0.6875rem] font-semibold uppercase tracking-[0.1em] text-muted-foreground"
              >
                {header}
              </th>
            ))}
            {renderRowAction && <th className="sticky right-0 z-10 bg-muted px-4 py-2.5" />}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={row[0] ?? index} className="border-t border-border transition-colors hover:bg-muted/50">
              {row.map((cell, cellIndex) => (
                <td
                  key={cellIndex}
                  className={cn(
                    "px-4 py-3 align-middle",
                    cellIndex === 0 ? "font-medium text-foreground" : "text-muted-foreground",
                  )}
                >
                  {cell}
                </td>
              ))}
              {renderRowAction && <td className="sticky right-0 z-10 bg-card px-4 py-2.5 text-right">{renderRowAction(row)}</td>}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
