import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import { useConfirm } from "../components/ui/confirm";
import { Thread, deleteThread, listApprovals, listThreads, renameThread, type Account, type Theme } from "../lib/api";
import { AccountMenu } from "../app/AccountMenu";
import { ClockIcon, HomeIcon, PanelIcon, PlugIcon, PlusIcon, SearchIcon, SlidersIcon } from "../components/ui/icons";
import { runningExtensions } from "../extensions";
import { EggyBrand } from "../components/ui/brand";
import { cn, errorMessage } from "../lib/utils";
import type { View } from "../app/routing";

const initialThreadMaxAgeMs = 5 * 60 * 1000;
// How long a deleted chat can be undone before the server delete happens.
const undoWindowMs = 6_000;

export const SIDEBAR_MIN_WIDTH = 240;
export const SIDEBAR_DEFAULT_WIDTH = 288;
export const SIDEBAR_MAX_WIDTH = 420;
const sidebarWidthKey = "eggy.chat.sidebar.width";

// reinsertThread puts a chat back at the row it held before it was deleted,
// or at the end when the list has shrunk since.
export function reinsertThread(threads: Thread[], thread: Thread, index: number): Thread[] {
  const next = [...threads];
  next.splice(Math.min(index, next.length), 0, thread);
  return next;
}

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

// Which date groups the owner has folded shut. It is a per-browser
// convenience, so when storage is unavailable every group simply starts open.
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

const DAY_MS = 24 * 60 * 60 * 1000;
export const THREAD_GROUPS = ["Today", "Yesterday", "Previous 7 days", "Older"] as const;
export type ThreadGroupLabel = (typeof THREAD_GROUPS)[number];

// Chats are listed under the day they were last written in, newest first, so
// the rail reads as a timeline rather than a bag of titles. Empty groups are
// dropped rather than shown as headings with nothing under them.
// filterThreads keeps the chats whose title contains the query, ignoring case.
// An empty query keeps everything. An untitled chat has no title to match.
export function filterThreads(threads: Thread[], query: string): Thread[] {
  const needle = query.trim().toLowerCase();
  if (needle === "") return threads;
  return threads.filter((thread) => thread.title.toLowerCase().includes(needle));
}

export function groupThreads(threads: Thread[], now = new Date()): { label: ThreadGroupLabel; threads: Thread[] }[] {
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const buckets = new Map<ThreadGroupLabel, Thread[]>(THREAD_GROUPS.map((label) => [label, []]));
  const newestFirst = [...threads].sort((left, right) => Date.parse(right.updatedAt) - Date.parse(left.updatedAt));
  for (const thread of newestFirst) {
    const at = Date.parse(thread.updatedAt);
    const label: ThreadGroupLabel =
      at >= startOfToday
        ? "Today"
        : at >= startOfToday - DAY_MS
          ? "Yesterday"
          : at >= startOfToday - 7 * DAY_MS
            ? "Previous 7 days"
            : "Older";
    buckets.get(label)?.push(thread);
  }
  return THREAD_GROUPS.map((label) => ({ label, threads: buckets.get(label) ?? [] })).filter((group) => group.threads.length > 0);
}

// One row in the rail's navigation block. The active row takes the same
// raised surface as the active chat, so "where am I" reads the same everywhere.
// approvalBadge is what the Home row shows for the approvals waiting on the
// owner: nothing when none are, the count otherwise, and "99+" past that so the
// pill keeps its shape.
export function approvalBadge(waiting: number): string | null {
  if (waiting <= 0) return null;
  return waiting > 99 ? "99+" : String(waiting);
}

function NavRow({
  icon,
  label,
  active = false,
  disabled = false,
  badge,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  active?: boolean;
  disabled?: boolean;
  badge?: string | null;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex h-10 w-full items-center gap-2.5 rounded-xl px-2.5 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:pointer-events-none disabled:opacity-40",
        active ? "bg-background font-medium shadow-subtle" : "text-foreground/80 hover:bg-neutral-200/60",
      )}
    >
      <span className="flex h-4 w-4 shrink-0 items-center justify-center text-muted-foreground">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {badge && (
        <span aria-label={`${badge} waiting for you`} className="flex h-5 min-w-5 shrink-0 items-center justify-center rounded-full bg-primary px-1.5 text-[11px] font-semibold text-primary-foreground">
          {badge}
        </span>
      )}
    </button>
  );
}

export function ThreadSidebar({
  activeThreadId,
  onSelect,
  onStartNew,
  onDeleted,
  onActiveTitleChange,
  onCollapse,
  reloadKey,
  draftOpen,
  view,
  onNavigate,
  features,
  onOpenExtension,
  onOpenSettings,
  account,
  onLogout,
  theme,
  onThemeChange,
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
  reloadKey: number;
  draftOpen: boolean;
  // The destination on screen, so its navigation row reads as current, and
  // the handler that moves to another one. Picking a chat or starting one
  // also asks for the chat view; the selection itself does not navigate, so
  // a deep link to Settings survives the first load picking a thread.
  view: View;
  onNavigate: (view: View) => void;
  features: string[];
  // Opens an extension's window by its id, from the Extensions menu.
  onOpenExtension: (extensionId: string) => void;
  onOpenSettings: () => void;
  account?: Account;
  onLogout: () => void;
  // The account's palette and how to change it from the menu. Optional, so a
  // caller that has no palette to show still gets the menu.
  theme?: Theme;
  onThemeChange?: (theme: Theme) => void;
}) {
  const [confirm, confirmDialog] = useConfirm();
  const [threads, setThreads] = useState<Thread[]>([]);
  // The count of approvals waiting, read whenever the rail is shown or the view
  // changes, so the Home badge is current without a timer.
  const [waitingApprovals, setWaitingApprovals] = useState(0);
  useEffect(() => {
    listApprovals()
      .then((result) => setWaitingApprovals((result.table_rows ?? []).filter((row) => row[3] !== "expired").length))
      .catch(() => setWaitingApprovals(0));
  }, [view, reloadKey]);
  // menuFor / renamingId are the row-level UI states: at most one row shows
  // its actions menu, and at most one row is an inline rename field.
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [draftTitle, setDraftTitle] = useState("");
  const [error, setError] = useState<string | null>(null);
  const renameInput = useRef<HTMLInputElement | null>(null);
  // The chat deleted in the last few seconds, held until the undo window ends.
  const [undoable, setUndoable] = useState<Thread | null>(null);
  const pendingDelete = useRef<{ thread: Thread; index: number; timer: ReturnType<typeof setTimeout> } | null>(null);
  const initialSelectionStarted = useRef(false);
  const [sidebarWidth, setSidebarWidth] = useState(storedSidebarWidth);
  const sidebarWidthRef = useRef(sidebarWidth);
  const dragStart = useRef({ x: 0, width: SIDEBAR_DEFAULT_WIDTH });
  const [resizing, setResizing] = useState(false);
  const [extensionsOpen, setExtensionsOpen] = useState(false);
  const extensionsRoot = useRef<HTMLDivElement | null>(null);
  const extensions = runningExtensions(features);
  const [chatQuery, setChatQuery] = useState("");

  // The Extensions menu dismisses the way the edit-sidebar popover does: a press
  // anywhere outside it, or Escape, closes it.
  useEffect(() => {
    if (!extensionsOpen) return;
    const onPointer = (event: globalThis.PointerEvent) => {
      if (!extensionsRoot.current?.contains(event.target as Node)) setExtensionsOpen(false);
    };
    const onKey = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") setExtensionsOpen(false);
    };
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [extensionsOpen]);

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

  // Leaving the rail inside the undo window makes the delete final now; the
  // timer would otherwise fire on an unmounted sidebar.
  useEffect(() => {
    return () => {
      const pending = pendingDelete.current;
      if (!pending) return;
      clearTimeout(pending.timer);
      pendingDelete.current = null;
      void deleteThread(pending.thread.id).catch(() => {});
    };
  }, []);

  const newChatAvailable = !draftOpen && canStartNewChat(threads, activeThreadId);
  const matching = filterThreads(threads, chatQuery);
  const groups = groupThreads(matching);

  function handleNew() {
    if (!newChatAvailable) return;
    setError(null);
    onStartNew();
    onNavigate("chat");
  }

  function pickThread(threadId: string) {
    onSelect(threadId);
    onNavigate("chat");
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
      setError(errorMessage(err, "Could not rename chat"));
    }
  }

  // A deleted chat leaves the list at once, but the server delete waits out a
  // short window so Undo can put the row back where it was. Only one delete is
  // held at a time: starting another one makes the held one final.
  async function handleDelete(thread: Thread) {
    setMenuFor(null);
    setError(null);
    const name = thread.title || "this chat";
    if (!(await confirm({ title: `Delete ${name}?`, body: "Its messages are deleted too.", confirmLabel: "Delete chat", destructive: true }))) return;
    finishDelete();
    const index = threads.findIndex((row) => row.id === thread.id);
    setThreads((current) => current.filter((row) => row.id !== thread.id));
    const timer = setTimeout(finishDelete, undoWindowMs);
    pendingDelete.current = { thread, index, timer };
    setUndoable(thread);
  }

  function finishDelete() {
    const pending = pendingDelete.current;
    if (!pending) return;
    clearTimeout(pending.timer);
    pendingDelete.current = null;
    setUndoable(null);
    deleteThread(pending.thread.id)
      .then(() => onDeleted(pending.thread.id))
      .catch((err) => {
        setThreads((current) => reinsertThread(current, pending.thread, pending.index));
        setError(errorMessage(err, "Could not delete chat"));
      });
  }

  function undoDelete() {
    const pending = pendingDelete.current;
    if (!pending) return;
    clearTimeout(pending.timer);
    pendingDelete.current = null;
    setUndoable(null);
    setThreads((current) => reinsertThread(current, pending.thread, pending.index));
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
      className="relative flex h-full w-[min(90vw,22rem)] shrink-0 flex-col bg-neutral-100 md:w-[var(--sidebar-width)]"
      style={{ "--sidebar-width": `${sidebarWidth}px` } as CSSProperties}
    >
      {confirmDialog}
      {/* The header strip drags the window when Eggy runs in an app window with
          its title bar hidden, the way the sidebar's top edge does in Claude's
          desktop app. The collapse control opts out so it still takes clicks. */}
      <div className="flex h-14 shrink-0 items-center gap-1 pl-4 pr-2 [-webkit-app-region:drag]">
        <span className="flex-1 truncate">
          <EggyBrand />
        </span>
        <button
          type="button"
          onClick={onCollapse}
          aria-label="Collapse sidebar"
          title="Collapse sidebar"
          className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-neutral-800 transition-colors hover:bg-neutral-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 [-webkit-app-region:no-drag]"
        >
          <PanelIcon />
        </button>
      </div>

      <nav aria-label="Main navigation" className="flex flex-col gap-0.5 px-2 pb-2">
        <NavRow icon={<HomeIcon />} label="Home" active={view === "home"} badge={approvalBadge(waitingApprovals)} onClick={() => onNavigate("home")} />
        <NavRow
          icon={<PlusIcon className="h-4 w-4 text-accent-700" />}
          label="New chat"
          disabled={!newChatAvailable}
          onClick={handleNew}
        />
        <NavRow icon={<ClockIcon />} label="Scheduled" active={view === "scheduled"} onClick={() => onNavigate("scheduled")} />
        <NavRow icon={<SlidersIcon />} label="Customize" active={view === "customize"} onClick={() => onNavigate("customize")} />
        {extensions.length > 0 && (
          <div ref={extensionsRoot} className="relative">
            <NavRow icon={<PlugIcon />} label="Extensions" active={extensionsOpen} onClick={() => setExtensionsOpen((open) => !open)} />
            {extensionsOpen && (
              <div
                role="menu"
                aria-label="Extensions"
                className="mt-1 overflow-hidden rounded-2xl bg-card py-1.5 shadow-lift md:absolute md:left-full md:top-0 md:z-30 md:ml-2 md:mt-0 md:w-48"
              >
                {extensions.map((extension) => (
                  <button
                    key={extension.id}
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      setExtensionsOpen(false);
                      onOpenExtension(extension.id);
                    }}
                    className="flex h-11 w-full items-center gap-3 px-3.5 text-left text-sm text-foreground/90 transition-colors hover:bg-neutral-100 focus-visible:outline-none focus-visible:bg-neutral-100"
                  >
                    <span className="flex h-4 w-4 shrink-0 items-center justify-center text-muted-foreground">{extension.icon}</span>
                    {extension.label}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </nav>

      {error && (
        <p className="mx-3 mb-2 rounded-xl bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive" role="alert">
          {error}
        </p>
      )}

      {undoable && (
        <div role="status" className="mx-3 mb-2 flex items-center gap-2 rounded-xl bg-card py-1 pl-3 pr-1 text-xs shadow-subtle">
          <span className="min-w-0 flex-1 truncate">Deleted “{undoable.title || "New chat"}”</span>
          <button
            type="button"
            onClick={undoDelete}
            className="flex h-11 shrink-0 items-center rounded-lg px-3 font-semibold text-foreground underline-offset-4 transition-colors hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            Undo
          </button>
        </div>
      )}

      <div className="scrollbar-slim flex-1 overflow-y-auto px-2 pb-3">
        {threads.length > 0 && (
          <label className="mx-1 mb-2 flex h-9 items-center gap-2 rounded-xl bg-background px-2.5 text-neutral-700 shadow-[inset_0_0_0_1px_hsl(var(--neutral-200))] focus-within:ring-2 focus-within:ring-ring/40">
            <SearchIcon className="h-3.5 w-3.5 shrink-0" />
            <input
              type="search"
              value={chatQuery}
              onChange={(event) => setChatQuery(event.target.value)}
              placeholder="Search chats"
              aria-label="Search chats"
              className="min-w-0 flex-1 bg-transparent text-sm text-foreground placeholder:text-neutral-500 focus-visible:outline-none"
            />
          </label>
        )}
        {threads.length === 0 ? (
          <p className="px-2 py-6 text-center text-xs text-muted-foreground">No chats yet.</p>
        ) : matching.length === 0 ? (
          <p className="px-2 py-6 text-center text-xs text-muted-foreground">No chats match “{chatQuery.trim()}”.</p>
        ) : (
          groups.map((group) => (
            <section key={group.label} aria-label={group.label} className="flex flex-col gap-0.5">
              <h2 className="px-2.5 pb-2 pt-4 text-xs font-medium text-muted-foreground">{group.label}</h2>
              {group.threads.map((thread) => {
                const active = view === "chat" && thread.id === activeThreadId;
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
                      className="h-11 w-full rounded-2xl bg-background px-3 text-sm outline-none ring-2 ring-ring/40"
                    />
                  );
                }
                return (
                  <div key={thread.id} className="group relative">
                    <button
                      type="button"
                      onClick={() => pickThread(thread.id)}
                      aria-current={active ? "true" : undefined}
                      className={cn(
                        "flex h-9 w-full items-center rounded-xl pl-2.5 pr-9 text-left text-sm transition-colors",
                        active ? "bg-background font-medium shadow-subtle" : "font-normal text-foreground/80 hover:bg-neutral-200/60",
                      )}
                    >
                      <span className="truncate">{thread.title || "New chat"}</span>
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
                        "absolute right-0 top-1/2 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-xl text-muted-foreground transition-opacity hover:bg-background/70 hover:text-foreground focus:opacity-100",
                        menuFor === thread.id || active ? "opacity-100" : "opacity-0 group-hover:opacity-100 [@media(hover:none)]:opacity-100",
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
                        className="absolute right-1 top-[calc(100%-0.25rem)] z-10 w-36 overflow-hidden rounded-2xl bg-card py-1 shadow-lift"
                      >
                        <button
                          type="button"
                          onClick={() => startRename(thread)}
                          className="h-11 w-full px-3 text-left text-sm text-foreground/90 hover:bg-neutral-100"
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
            </section>
          ))
        )}
      </div>

      <div className="shrink-0 border-t border-border px-2 py-2">
        <AccountMenu account={account} onNavigate={onNavigate} onOpenSettings={onOpenSettings} onLogout={onLogout} theme={theme} onThemeChange={onThemeChange} />
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
        className={cn(
          "group absolute inset-y-0 -right-1.5 z-10 hidden w-3 cursor-col-resize touch-none outline-none md:block",
          // A grey grip, drawn as a short pill on the edge. It stays hidden until
          // the pointer has rested on the edge for 0.3s, so the edge is quiet
          // while the owner is only passing over it. While dragging it shows at
          // once, since the pointer has usually left the 12px target by then.
          "after:absolute after:left-1/2 after:top-1/2 after:h-14 after:w-1 after:-translate-x-1/2 after:-translate-y-1/2 after:rounded-full after:bg-neutral-400 after:transition-opacity after:duration-150 after:content-['']",
          resizing ? "after:opacity-100" : "after:opacity-0 hover:after:opacity-100 hover:after:delay-300",
          "focus-visible:after:opacity-100",
        )}
      >
      </button>
    </div>
  );
}
