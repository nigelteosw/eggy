import { useEffect, useRef, useState } from "react";
import { Thread, createThread, deleteThread, listThreads, renameThread } from "./api";
import { PanelIcon, PlusIcon, SettingsIcon, TraceIcon } from "./components/ui/icons";
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
  onDeleted,
  onActiveTitleChange,
  onCollapse,
  onOpenSettings,
  onOpenTraces,
  reloadKey,
}: {
  activeThreadId: string | null;
  onSelect: (threadId: string) => void;
  onDeleted: (threadId: string) => void;
  // Reports the open chat's title so the chat pane can head itself with it.
  // It comes from here rather than from a fetch in ChatPage because this is
  // the component that already holds the list and reloads it after a rename.
  onActiveTitleChange: (title: string) => void;
  onCollapse: () => void;
  onOpenSettings: () => void;
  onOpenTraces: () => void;
  reloadKey: number;
}) {
  const [threads, setThreads] = useState<Thread[]>([]);
  // menuFor / renamingId are the row-level UI states: at most one row shows
  // its actions menu, and at most one row is an inline rename field.
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [draftTitle, setDraftTitle] = useState("");
  const [error, setError] = useState<string | null>(null);
  const renameInput = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    listThreads()
      .then(setThreads)
      .catch(() => setThreads([]));
  }, [reloadKey]);

  useEffect(() => {
    const active = threads.find((thread) => thread.id === activeThreadId);
    onActiveTitleChange(active?.title || "New chat");
  }, [threads, activeThreadId, onActiveTitleChange]);

  // A click anywhere else dismisses an open row menu, the way a menu is
  // expected to behave; without this it would linger while the user works
  // in the chat pane.
  useEffect(() => {
    if (!menuFor) return;
    const dismiss = () => setMenuFor(null);
    window.addEventListener("click", dismiss);
    return () => window.removeEventListener("click", dismiss);
  }, [menuFor]);

  useEffect(() => {
    if (renamingId) renameInput.current?.focus();
  }, [renamingId]);

  async function handleNew() {
    const id = await createThread();
    setThreads((current) => [{ id, title: "", updatedAt: new Date().toISOString() }, ...current]);
    onSelect(id);
  }

  function startRename(thread: Thread) {
    setMenuFor(null);
    setError(null);
    setRenamingId(thread.id);
    setDraftTitle(thread.title);
  }

  async function commitRename(threadId: string) {
    const title = draftTitle.trim();
    setRenamingId(null);
    const previous = threads.find((thread) => thread.id === threadId)?.title ?? "";
    if (!title || title === previous) return;
    setThreads((current) => current.map((thread) => (thread.id === threadId ? { ...thread, title } : thread)));
    try {
      await renameThread(threadId, title);
    } catch (err) {
      // Put the old title back rather than leaving the sidebar claiming a
      // rename that the server refused.
      setThreads((current) => current.map((thread) => (thread.id === threadId ? { ...thread, title: previous } : thread)));
      setError(err instanceof Error ? err.message : "Could not rename chat");
    }
  }

  async function handleDelete(thread: Thread) {
    setMenuFor(null);
    setError(null);
    const name = thread.title || "this chat";
    if (!window.confirm(`Delete ${name}? Its messages are deleted too, and this cannot be undone.`)) return;
    try {
      await deleteThread(thread.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not delete chat");
      return;
    }
    setThreads((current) => current.filter((row) => row.id !== thread.id));
    onDeleted(thread.id);
  }

  return (
    // bg-background, not bg-card: this is the same surface the config rail
    // sits on, so the two screens read as one app rather than as two.
    <div className="flex h-full w-72 shrink-0 flex-col border-r border-border/80 bg-background/95">
      {/* The close control sits at the top left, ahead of the wordmark: it is
          the same corner the reopen button occupies once the rail is gone, so
          the sidebar collapses and returns under one spot on the screen. */}
      <div className="flex h-16 items-center gap-2 border-b border-border/60 px-3">
        <button
          type="button"
          onClick={onCollapse}
          aria-label="Collapse sidebar"
          title="Collapse sidebar"
          className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <PanelIcon />
        </button>
        <span className="text-base font-semibold tracking-tight">Eggy</span>
      </div>

      {/* A section head with the new-chat control on its right, rather than a
          full-width button above the list: the list is the thing this rail is
          for, and a solid primary bar over it took the eye first. */}
      <div className="flex items-center justify-between px-4 pb-2 pt-5">
        <span className="text-[0.6875rem] font-medium uppercase tracking-wider text-muted-foreground">Chats</span>
        <button
          type="button"
          onClick={handleNew}
          aria-label="New chat"
          title="New chat"
          className="flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <PlusIcon className="h-4 w-4" />
        </button>
      </div>

      {error && (
        <p className="mx-3 mb-2 rounded-md bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive" role="alert">
          {error}
        </p>
      )}

      <div className="scrollbar-slim flex-1 overflow-y-auto px-2 pb-3">
        {threads.length === 0 ? (
          <p className="px-2 py-6 text-center text-xs text-muted-foreground">No chats yet.</p>
        ) : (
          <div className="flex flex-col gap-1">
            {threads.map((thread) => {
              const active = thread.id === activeThreadId;
              if (thread.id === renamingId) {
                return (
                  <input
                    key={thread.id}
                    ref={renameInput}
                    value={draftTitle}
                    maxLength={200}
                    aria-label="Chat name"
                    onChange={(event) => setDraftTitle(event.target.value)}
                    onBlur={() => commitRename(thread.id)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") commitRename(thread.id);
                      if (event.key === "Escape") setRenamingId(null);
                    }}
                    className="w-full rounded-md border border-ring bg-background px-3 py-2.5 text-sm outline-none ring-2 ring-ring/25"
                  />
                );
              }
              return (
                <div key={thread.id} className="group relative">
                  <button
                    type="button"
                    onClick={() => onSelect(thread.id)}
                    className={cn(
                      "w-full rounded-lg py-3 pl-3 pr-9 text-left transition-colors",
                      active ? "bg-accent text-accent-foreground shadow-subtle" : "text-foreground/80 hover:bg-muted/70",
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

                  <button
                    type="button"
                    aria-label={`Actions for ${thread.title || "New chat"}`}
                    onClick={(event) => {
                      // Stop the window-level dismiss listener from closing
                      // this menu in the same click that opened it.
                      event.stopPropagation();
                      setMenuFor((current) => (current === thread.id ? null : thread.id));
                    }}
                    className={cn(
                      "absolute right-1 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition-opacity hover:bg-background/70 hover:text-foreground focus:opacity-100",
                      menuFor === thread.id || active ? "opacity-100" : "opacity-0 group-hover:opacity-100",
                    )}
                  >
                    <svg viewBox="0 0 20 20" className="h-4 w-4" fill="currentColor">
                      <circle cx="4" cy="10" r="1.5" />
                      <circle cx="10" cy="10" r="1.5" />
                      <circle cx="16" cy="10" r="1.5" />
                    </svg>
                  </button>

                  {menuFor === thread.id && (
                    <div
                      onClick={(event) => event.stopPropagation()}
                      className="absolute right-1 top-[calc(100%-0.25rem)] z-10 w-36 overflow-hidden rounded-md border border-border bg-card py-1 shadow-lift"
                    >
                      <button
                        type="button"
                        onClick={() => startRename(thread)}
                        className="w-full px-3 py-1.5 text-left text-sm text-foreground/90 hover:bg-muted"
                      >
                        Rename
                      </button>
                      <button
                        type="button"
                        onClick={() => handleDelete(thread)}
                        className="w-full px-3 py-1.5 text-left text-sm text-destructive hover:bg-destructive/10"
                      >
                        Delete
                      </button>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Settings is the last thing in the rail rather than a button floating
          over the transcript: it is a destination like every chat above it,
          and pinning it here means the corner of the chat pane belongs to the
          conversation. */}
      <div className="shrink-0 border-t border-border p-2">
        {/* Traces sits beside Settings rather than inside it: it is a
            destination about the conversations above it, not a setting. */}
        <button
          type="button"
          onClick={onOpenTraces}
          className="flex h-9 w-full items-center gap-3 rounded-md px-2.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <span className="flex h-4 w-4 shrink-0 items-center justify-center">
            <TraceIcon />
          </span>
          Traces
        </button>
        <button
          type="button"
          onClick={onOpenSettings}
          className="flex h-9 w-full items-center gap-3 rounded-md px-2.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <span className="flex h-4 w-4 shrink-0 items-center justify-center">
            <SettingsIcon />
          </span>
          Settings
        </button>
      </div>
    </div>
  );
}
