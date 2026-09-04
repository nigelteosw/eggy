import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent } from "react";
import { Thread, deleteThread, listThreads, renameThread } from "./api";
import { PanelIcon, PlusIcon, SettingsIcon, TraceIcon } from "./components/ui/icons";
import { cn } from "./lib/utils";

const initialThreadMaxAgeMs = 5 * 60 * 1000;

export const SIDEBAR_MIN_WIDTH = 240;
export const SIDEBAR_DEFAULT_WIDTH = 288;
export const SIDEBAR_MAX_WIDTH = 420;
const sidebarWidthKey = "eggy.chat.sidebar.width";

export function clampSidebarWidth(width: number): number {
  return Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, width));
}

export function sidebarWidthForKey(width: number, key: string): number {
  if (key === "Home") return SIDEBAR_MIN_WIDTH;
  if (key === "End") return SIDEBAR_MAX_WIDTH;
  if (key === "ArrowLeft") return clampSidebarWidth(width - 8);
  if (key === "ArrowRight") return clampSidebarWidth(width + 8);
  return width;
}

function storedSidebarWidth(): number {
  if (typeof window === "undefined") return SIDEBAR_DEFAULT_WIDTH;
  try {
    const stored = Number(window.localStorage.getItem(sidebarWidthKey));
    return Number.isFinite(stored) && stored > 0 ? clampSidebarWidth(stored) : SIDEBAR_DEFAULT_WIDTH;
  } catch {
    return SIDEBAR_DEFAULT_WIDTH;
  }
}

function rememberSidebarWidth(width: number) {
  try {
    window.localStorage.setItem(sidebarWidthKey, String(width));
  } catch {
    // Resizing still works when storage is unavailable.
  }
}

export function initialThreadSelection(threads: Thread[], now = Date.now()): string | null {
  const recent = threads
    .filter((thread) => {
      const age = now - Date.parse(thread.updatedAt);
      return age >= 0 && age <= initialThreadMaxAgeMs;
    })
    .sort((left, right) => Date.parse(right.updatedAt) - Date.parse(left.updatedAt));
  return recent[0]?.id ?? null;
}

// An untitled chat is one nobody has written in yet: the server auto-titles a
// thread from its first message, so a blank title is the one reliable mark of
// an unused chat. Sitting in one and pressing "+" should do nothing -- without
// this the button mints another empty thread on every press, and the rail
// fills with untitled rows nobody asked for.
export function canStartNewChat(threads: Thread[], activeThreadId: string | null): boolean {
  const active = threads.find((thread) => thread.id === activeThreadId);
  return !active || active.title !== "";
}

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
  onStartNew,
  onDeleted,
  onActiveTitleChange,
  onCollapse,
  onOpenSettings,
  onOpenTraces,
  reloadKey,
  draftOpen,
}: {
  activeThreadId: string | null;
  onSelect: (threadId: string) => void;
  onStartNew: () => void;
  onDeleted: (threadId: string) => void;
  // Reports the open chat's title so the chat pane can head itself with it.
  // It comes from here rather than from a fetch in ChatPage because this is
  // the component that already holds the list and reloads it after a rename.
  onActiveTitleChange: (title: string) => void;
  onCollapse: () => void;
  onOpenSettings: () => void;
  onOpenTraces: () => void;
  reloadKey: number;
  draftOpen: boolean;
}) {
  const [threads, setThreads] = useState<Thread[]>([]);
  // menuFor / renamingId are the row-level UI states: at most one row shows
  // its actions menu, and at most one row is an inline rename field.
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [draftTitle, setDraftTitle] = useState("");
  const [error, setError] = useState<string | null>(null);
  const renameInput = useRef<HTMLInputElement | null>(null);
  const initialSelectionStarted = useRef(false);
  const [sidebarWidth, setSidebarWidth] = useState(storedSidebarWidth);
  const sidebarWidthRef = useRef(sidebarWidth);
  const dragStart = useRef({ x: 0, width: SIDEBAR_DEFAULT_WIDTH });
  const [resizing, setResizing] = useState(false);

  function updateSidebarWidth(width: number) {
    const next = clampSidebarWidth(width);
    sidebarWidthRef.current = next;
    setSidebarWidth(next);
  }

  useEffect(() => {
    if (!resizing) return;
    const move = (event: globalThis.PointerEvent) => {
      updateSidebarWidth(dragStart.current.width + event.clientX - dragStart.current.x);
    };
    const stop = () => {
      setResizing(false);
      rememberSidebarWidth(sidebarWidthRef.current);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop, { once: true });
    window.addEventListener("pointercancel", stop, { once: true });
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
      window.removeEventListener("pointercancel", stop);
    };
  }, [resizing]);

  useEffect(() => {
    listThreads()
      .then((loaded) => {
        setThreads(loaded);
        if (initialSelectionStarted.current) return;
        initialSelectionStarted.current = true;

        const selection = initialThreadSelection(loaded);
        if (selection) {
          onSelect(selection);
          return;
        }
        // Opening the composer is local UI state. Creating a durable row here
        // would make merely returning to the app look like a new chat; the
        // first non-empty message creates the server thread.
        onStartNew();
      })
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

  const newChatAvailable = !draftOpen && canStartNewChat(threads, activeThreadId);

  function handleNew() {
    if (!newChatAvailable) return;
    setError(null);
    onStartNew();
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

  function beginResize(event: PointerEvent<HTMLButtonElement>) {
    dragStart.current = { x: event.clientX, width: sidebarWidth };
    setResizing(true);
  }

  function resizeWithKeyboard(event: KeyboardEvent<HTMLButtonElement>) {
    const next = sidebarWidthForKey(sidebarWidth, event.key);
    if (next === sidebarWidth) return;
    event.preventDefault();
    updateSidebarWidth(next);
    rememberSidebarWidth(next);
  }

  function resetSidebarWidth() {
    updateSidebarWidth(SIDEBAR_DEFAULT_WIDTH);
    rememberSidebarWidth(SIDEBAR_DEFAULT_WIDTH);
  }

  return (
    <div
      className="relative flex h-full w-[min(90vw,22rem)] shrink-0 flex-col border-r bg-background md:w-[var(--sidebar-width)]"
      style={{ "--sidebar-width": `${sidebarWidth}px` } as CSSProperties}
    >
      <div className="flex h-16 items-center gap-2 border-b border-border/60 px-3">
        <button
          type="button"
          onClick={onCollapse}
          aria-label="Collapse sidebar"
          title="Collapse sidebar"
          className="flex h-11 w-11 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <PanelIcon />
        </button>
        <span className="text-base font-semibold tracking-tight">Eggy</span>
      </div>

      <div className="flex items-center justify-between px-4 pb-2 pt-5">
        <span className="text-sm font-medium">Chats</span>
        <button
          type="button"
          onClick={handleNew}
          disabled={!newChatAvailable}
          title={newChatAvailable ? "New chat" : "Write a message to start a new chat"}
          className="flex h-11 items-center gap-2 rounded-md px-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:pointer-events-none disabled:opacity-40"
        >
          <PlusIcon className="h-4 w-4" />
          New chat
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
                    className="h-11 w-full rounded-md border border-ring bg-background px-3 text-sm outline-none ring-2 ring-ring/25"
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
                      "absolute right-0 top-1/2 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition-opacity hover:bg-background/70 hover:text-foreground focus:opacity-100",
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
                        className="h-11 w-full px-3 text-left text-sm text-foreground/90 hover:bg-muted"
                      >
                        Rename
                      </button>
                      <button
                        type="button"
                        onClick={() => handleDelete(thread)}
                        className="h-11 w-full px-3 text-left text-sm text-destructive hover:bg-destructive/10"
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
        <button
          type="button"
          onClick={onOpenTraces}
          className="flex h-11 w-full items-center gap-3 rounded-md px-2.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <span className="flex h-4 w-4 shrink-0 items-center justify-center">
            <TraceIcon />
          </span>
          Traces
        </button>
        <button
          type="button"
          onClick={onOpenSettings}
          className="flex h-11 w-full items-center gap-3 rounded-md px-2.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <span className="flex h-4 w-4 shrink-0 items-center justify-center">
            <SettingsIcon />
          </span>
          Settings
        </button>
      </div>
      <button
        type="button"
        role="separator"
        aria-label="Resize chat sidebar"
        aria-orientation="vertical"
        aria-valuemin={SIDEBAR_MIN_WIDTH}
        aria-valuemax={SIDEBAR_MAX_WIDTH}
        aria-valuenow={sidebarWidth}
        onPointerDown={beginResize}
        onKeyDown={resizeWithKeyboard}
        onDoubleClick={resetSidebarWidth}
        className="absolute inset-y-0 -right-1.5 z-10 hidden w-3 cursor-col-resize touch-none outline-none after:absolute after:inset-y-0 after:left-1/2 after:w-px after:bg-transparent hover:after:bg-primary/50 focus-visible:after:bg-primary md:block"
      />
    </div>
  );
}
