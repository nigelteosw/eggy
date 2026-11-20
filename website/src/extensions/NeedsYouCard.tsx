import { useCallback, useEffect, useState } from "react";
import { SessionExpiredError, listAgentRuns } from "../lib/api";
import { relativeTime } from "../lib/time";
import { needsYouItems, type NeedsYouItem } from "./needs-you";
import { listNeedsYou } from "./terminal/terminal";

// How often Home rechecks. A blocked terminal or a waiting run is not urgent
// to the second, and Home is often left open.
const REFRESH_MS = 30_000;

// NeedsYouCard lists, on Home, what the extensions have waiting on the owner:
// blocked terminals and agent runs waiting for an answer. It reads only the
// extensions whose feature is running, and draws nothing when nothing waits.
// Picking an item opens its extension.
export function NeedsYouCard({
  features,
  onOpenExtension,
  onSessionExpired,
}: {
  features: string[];
  onOpenExtension: (id: NeedsYouItem["extension"]) => void;
  onSessionExpired: () => void;
}) {
  const [items, setItems] = useState<NeedsYouItem[]>([]);
  const terminal = features.includes("terminal");
  const runner = features.includes("agent_runner");

  const refresh = useCallback(async () => {
    // One source failing must not hide the other's items.
    const [terminals, runs] = await Promise.allSettled([terminal ? listNeedsYou() : Promise.resolve([]), runner ? listAgentRuns() : Promise.resolve([])]);
    for (const r of [terminals, runs]) {
      if (r.status === "rejected" && r.reason instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
    }
    setItems(needsYouItems(terminals.status === "fulfilled" ? terminals.value : [], runs.status === "fulfilled" ? runs.value : []));
  }, [terminal, runner, onSessionExpired]);

  useEffect(() => {
    if (!terminal && !runner) return;
    void refresh();
    const id = window.setInterval(() => void refresh(), REFRESH_MS);
    return () => window.clearInterval(id);
  }, [terminal, runner, refresh]);

  if (items.length === 0) return null;
  return (
    <div className="rounded-card bg-yolk-soft/60 p-1">
      <h2 className="px-3 pb-0.5 pt-2 text-caption font-medium text-yolk-ink">Waiting on you</h2>
      <ul>
        {items.map((item) => (
          <li key={item.key}>
            <button type="button" onClick={() => onOpenExtension(item.extension)} className="flex min-h-11 w-full items-center gap-3 rounded-control px-3 py-2 text-left hover:bg-yolk-soft">
              <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-full bg-yolk-ink" />
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="truncate text-small text-foreground">{item.title}</span>
                <span className="truncate text-caption text-neutral-600">
                  {item.extension === "terminal" ? "Terminal" : "Agent run"} · {item.detail}
                </span>
              </span>
              <span className="shrink-0 text-caption text-neutral-600">{relativeTime(item.changed, Date.now())}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
