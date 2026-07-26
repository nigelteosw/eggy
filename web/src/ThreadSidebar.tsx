import { useEffect, useState } from "react";
import { Thread, createThread, listThreads } from "./api";
import { Button } from "./components/ui/button";
import { cn } from "./lib/utils";

function relativeTime(iso: string): string {
  const deltaMs = Date.now() - new Date(iso).getTime();
  const minutes = Math.round(deltaMs / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  return `${days}d ago`;
}

export function ThreadSidebar({
  activeThreadId,
  onSelect,
  reloadKey,
}: {
  activeThreadId: string | null;
  onSelect: (threadId: string) => void;
  reloadKey: number;
}) {
  const [threads, setThreads] = useState<Thread[]>([]);

  useEffect(() => {
    listThreads()
      .then(setThreads)
      .catch(() => setThreads([]));
  }, [reloadKey]);

  async function handleNew() {
    const id = await createThread();
    setThreads((current) => [{ id, title: "", updatedAt: new Date().toISOString() }, ...current]);
    onSelect(id);
  }

  return (
    <div className="flex h-full w-72 shrink-0 flex-col border-r border-border bg-card">
      <div className="flex items-center gap-2.5 px-4 py-4">
        <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-base leading-none">🥚</span>
        <span className="text-base font-semibold tracking-tight">Eggy</span>
      </div>

      <div className="px-3 pb-3">
        <Button type="button" onClick={handleNew} size="sm" className="h-9 w-full">
          <svg viewBox="0 0 20 20" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
            <path d="M10 5v10M5 10h10" />
          </svg>
          New chat
        </Button>
      </div>

      <div className="scrollbar-slim flex-1 overflow-y-auto px-2 pb-3">
        {threads.length === 0 ? (
          <p className="px-2 py-6 text-center text-xs text-muted-foreground">No chats yet.</p>
        ) : (
          <div className="flex flex-col gap-0.5">
            {threads.map((thread) => {
              const active = thread.id === activeThreadId;
              return (
                <button
                  key={thread.id}
                  type="button"
                  onClick={() => onSelect(thread.id)}
                  className={cn(
                    "group relative w-full rounded-md px-3 py-2.5 text-left transition-colors",
                    active ? "bg-accent text-accent-foreground" : "text-foreground/80 hover:bg-muted",
                  )}
                >
                  {/* Active marker: a short rule in the primary, rather than
                      restating the selection with yet another fill. */}
                  <span
                    aria-hidden="true"
                    className={cn(
                      "absolute left-0 top-1/2 h-5 w-0.5 -translate-y-1/2 rounded-r-full bg-primary transition-opacity",
                      active ? "opacity-100" : "opacity-0",
                    )}
                  />
                  <div className={cn("truncate text-sm", active ? "font-medium" : "font-normal")}>
                    {thread.title || "New chat"}
                  </div>
                  <div className={cn("mt-0.5 truncate text-xs", active ? "text-accent-foreground/70" : "text-muted-foreground")}>
                    {relativeTime(thread.updatedAt)}
                  </div>
                </button>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
