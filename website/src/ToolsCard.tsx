import { useEffect, useMemo, useState } from "react";
import { CommandResult, SessionExpiredError, listTools } from "./api";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";

// Column positions in the rows /api/tools returns.
const NAME = 0;
const SOURCE = 1;
const DESCRIPTION = 2;

export function ToolsCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    listTools()
      .then(setResult)
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to load");
      });
  }, [onSessionExpired]);

  const rows = result?.table_rows ?? [];

  // Filtering runs over name, source, and description together, because
  // "calendar" is as likely to be how the owner remembers a tool as its name
  // is. Sorting stays as the server sent it: that order is the catalog order
  // the model itself sees, kernel tools first and each MCP server's tools
  // after, and reordering it here would make the page disagree with the turn.
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    if (!needle) {
      return rows;
    }
    return rows.filter((row) =>
      [row[NAME], row[SOURCE], row[DESCRIPTION]].some((cell) => (cell ?? "").toLowerCase().includes(needle)),
    );
  }, [rows, filter]);

  const sources = useMemo(() => {
    const counts = new Map<string, number>();
    for (const row of rows) {
      counts.set(row[SOURCE], (counts.get(row[SOURCE]) ?? 0) + 1);
    }
    return [...counts.entries()].map(([source, count]) => `${count} ${source}`).join(", ");
  }, [rows]);

  return (
    <section className="flex flex-col gap-3">
      <div className="mx-0.5">
        <h3 className="text-[15px] font-semibold tracking-tight">Tools</h3>
        <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">
          Every tool Eggy can call this turn, read live from the one registry the agent loop runs on
          {sources && ` — ${sources}`}.
        </p>
      </div>
      <div className="flex flex-col gap-3">
        <Input
          placeholder="Filter by name, source, or description"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="rounded-xl border-0 bg-neutral-100"
        />
        <DataTable
          headers={result?.table_headers}
          rows={visible}
          empty={filter ? `No tool matches "${filter}".` : "No tools are registered."}
        />
        <p className="mx-0.5 text-xs leading-relaxed text-neutral-700">
          The list is read-only. Kernel tools are compiled in; <code className="rounded bg-neutral-100 px-1 py-0.5 text-[0.9em]">mcp</code>{" "}
          tools come from the servers above and appear or disappear as those connect, reload, or are logged out of.
        </p>
        {error && (
          <p className="rounded-2xl bg-eg-red-tint px-3.5 py-2.5 text-sm text-eg-red-ink" role="alert">
            {error}
          </p>
        )}
      </div>
    </section>
  );
}
