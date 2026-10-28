import { useEffect, useMemo, useState } from "react";
import { CommandResult, SessionExpiredError, listTools } from "../lib/api";
import { Input } from "../components/ui/input";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { errorMessage } from "../lib/utils";

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
        setError(errorMessage(err, "Failed to load"));
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
      <CardHeader
        title="Tools"
        description={
          <>
            Every tool Eggy can call this turn, read live from the one registry the agent loop runs on
            {sources && ` — ${sources}`}.
          </>
        }
      />
      <div className="flex flex-col gap-3">
        <Input
          placeholder="Filter by name, source, or description"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="rounded-xl border-0 bg-neutral-100"
        />
        {visible.length === 0 ? (
          <p className="rounded-xl bg-neutral-100 px-4 py-6 text-center text-sm text-neutral-700">
            {filter ? `No tool matches "${filter}".` : "No tools are registered."}
          </p>
        ) : (
          <ul className="flex flex-col divide-y divide-neutral-200 overflow-hidden rounded-xl border border-neutral-200">
            {visible.map((row) => (
              <ToolRow key={`${row[SOURCE]}:${row[NAME]}`} name={row[NAME]} source={row[SOURCE]} description={row[DESCRIPTION]} />
            ))}
          </ul>
        )}
        <p className="mx-0.5 text-xs leading-relaxed text-neutral-700">
          The list is read-only. Kernel tools are compiled in; <code className="rounded bg-neutral-100 px-1 py-0.5 text-[0.9em]">mcp</code>{" "}
          tools come from the servers above and appear or disappear as those connect, reload, or are logged out of.
        </p>
        {error && (
          <ErrorBanner>
            {error}
          </ErrorBanner>
        )}
      </div>
    </section>
  );
}

// firstSentence is the one line the owner reads about a tool. Descriptions are
// written for the model and often run several sentences, so the first is taken,
// and cut if it is itself long.
export function firstSentence(description: string): string {
  const text = description.trim();
  const end = text.search(/[.!?](\s|$)/);
  const sentence = end === -1 ? text : text.slice(0, end + 1);
  return sentence.length > 160 ? `${sentence.slice(0, 160).trimEnd()}…` : sentence;
}

// sourceLabel names where a tool comes from in words an owner uses. "kernel" is
// the internal name for the tools built into Eggy.
export function sourceLabel(source: string): string {
  return source === "kernel" ? "built in" : source;
}

function ToolRow({ name, source, description }: { name: string; source: string; description: string }) {
  const [open, setOpen] = useState(false);
  return (
    <li className="flex flex-col gap-1.5 px-4 py-3">
      <div className="flex items-center gap-2">
        <code className="text-sm font-medium text-foreground">{name}</code>
        <span className="rounded-md bg-neutral-100 px-1.5 py-0.5 text-[11px] text-neutral-700">{sourceLabel(source)}</span>
      </div>
      <p className="text-[13px] leading-5 text-neutral-700">{firstSentence(description)}</p>
      {description.trim() !== firstSentence(description) && (
        <div>
          <button type="button" aria-expanded={open} onClick={() => setOpen((value) => !value)} className="text-xs font-medium text-neutral-700 underline-offset-2 hover:underline">
            {open ? "Hide details" : "Details"}
          </button>
          {open && <p className="mt-1.5 text-xs leading-5 text-neutral-700">{description}</p>}
        </div>
      )}
    </li>
  );
}
