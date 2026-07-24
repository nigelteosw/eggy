import { useEffect, useState } from "react";
import { Thread, createThread, listThreads } from "./api";

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
    <div className="flex h-full w-64 shrink-0 flex-col border-r border-slate-200 bg-white">
      <div className="border-b border-slate-200 p-3">
        <button
          type="button"
          onClick={handleNew}
          className="w-full rounded bg-slate-900 px-3 py-2 text-sm text-white hover:bg-slate-800"
        >
          + New chat
        </button>
      </div>
      <div className="flex-1 overflow-y-auto">
        {threads.map((thread) => (
          <button
            key={thread.id}
            type="button"
            onClick={() => onSelect(thread.id)}
            className={`block w-full truncate border-b border-slate-100 px-3 py-3 text-left text-sm ${
              thread.id === activeThreadId ? "bg-slate-100 font-medium text-slate-900" : "text-slate-600 hover:bg-slate-50"
            }`}
          >
            <div className="truncate">{thread.title || "New chat"}</div>
            <div className="text-xs text-slate-400">{relativeTime(thread.updatedAt)}</div>
          </button>
        ))}
      </div>
    </div>
  );
}
