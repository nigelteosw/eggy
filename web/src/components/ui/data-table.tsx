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
    <div className={cn("scrollbar-slim overflow-x-auto rounded-md border border-border", className)}>
      <table className="w-full border-collapse text-left text-sm">
        <thead className="bg-muted/60">
          <tr>
            {headers?.map((header) => (
              <th
                key={header}
                className="whitespace-nowrap px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
              >
                {header}
              </th>
            ))}
            {renderRowAction && <th className="px-4 py-2.5" />}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={row[0] ?? index} className="border-t border-border transition-colors hover:bg-muted/40">
              {row.map((cell, cellIndex) => (
                <td
                  key={cellIndex}
                  className={cn(
                    "px-4 py-2.5 align-middle",
                    cellIndex === 0 ? "font-medium text-foreground" : "text-muted-foreground",
                  )}
                >
                  {cell}
                </td>
              ))}
              {renderRowAction && <td className="px-4 py-2.5 text-right">{renderRowAction(row)}</td>}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
