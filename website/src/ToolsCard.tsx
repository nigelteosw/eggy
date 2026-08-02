import { useEffect, useMemo, useState } from "react";
import { CommandResult, SessionExpiredError, listTools } from "./api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
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
    <Card>
      <CardHeader>
        <CardTitle>Tools</CardTitle>
        <CardDescription>
          Every tool Eggy can call this turn, read live from the one registry the agent loop runs on
          {sources && ` — ${sources}`}.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <Input placeholder="Filter by name, source, or description" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <DataTable
          headers={result?.table_headers}
          rows={visible}
          empty={filter ? `No tool matches "${filter}".` : "No tools are registered."}
        />
        <p className="text-xs leading-relaxed text-muted-foreground">
          The list is read-only. Kernel tools are compiled in; <code className="rounded bg-muted px-1 py-0.5 text-[0.9em]">mcp</code>{" "}
          tools come from the servers above and appear or disappear as those connect, reload, or are logged out of.
        </p>
        {error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
