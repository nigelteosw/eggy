import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import { Thread, deleteThread, listThreads, renameThread, type Account } from "./api";
import { AccountMenu } from "./AccountMenu";
import { ChevronDownIcon, ClockIcon, PanelIcon, PlugIcon, PlusIcon } from "./components/ui/icons";
import { runningExtensions } from "./extensions";
import { EggyBrand } from "./components/ui/brand";
import { cn, errorMessage } from "./lib/utils";
import type { View } from "./routing";

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

// Which date groups the owner has folded shut. It is a per-browser
// convenience, so when storage is unavailable every group simply starts open.
const collapsedGroupsKey = "eggy.chat.sidebar.collapsedGroups";

function storedCollapsedGroups(): Set<string> {
  try {
    const stored = JSON.parse(window.localStorage.getItem(collapsedGroupsKey) ?? "[]");
    return new Set(Array.isArray(stored) ? stored.filter((label): label is string => typeof label === "string") : []);
  } catch {
    return new Set();
  }
}

function rememberCollapsedGroups(collapsed: Set<string>) {
  try {
    window.localStorage.setItem(collapsedGroupsKey, JSON.stringify([...collapsed]));
  } catch {
    // Folding still works for this visit when storage is unavailable.
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

const DAY_MS = 24 * 60 * 60 * 1000;
export const THREAD_GROUPS = ["Today", "Yesterday", "Previous 7 days", "Older"] as const;
export type ThreadGroupLabel = (typeof THREAD_GROUPS)[number];

// Chats are listed under the day they were last written in, newest first, so
// the rail reads as a timeline rather than a bag of titles. Empty groups are
// dropped rather than shown as headings with nothing under them.
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
function NavRow({
  icon,
  label,
  active = false,
  disabled = false,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  active?: boolean;
  disabled?: boolean;
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
      {label}
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
  const [extensionsOpen, setExtensionsOpen] = useState(false);
  const extensionsRoot = useRef<HTMLDivElement | null>(null);
  const extensions = runningExtensions(features);
  const [collapsedGroups, setCollapsedGroups] = useState(storedCollapsedGroups);

  function toggleGroup(label: string) {
    const next = new Set(collapsedGroups);
    if (next.has(label)) next.delete(label);
    else next.add(label);
    rememberCollapsedGroups(next);
    setCollapsedGroups(next);
  }

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

  const newChatAvailable = !draftOpen && canStartNewChat(threads, activeThreadId);
  const groups = groupThreads(threads);

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

  async function handleDelete(thread: Thread) {
    setMenuFor(null);
    setError(null);
    const name = thread.title || "this chat";
    if (!window.confirm(`Delete ${name}? Its messages are deleted too, and this cannot be undone.`)) return;
    try {
      await deleteThread(thread.id);
    } catch (err) {
      setError(errorMessage(err, "Could not delete chat"));
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
      className="relative flex h-full w-[min(90vw,22rem)] shrink-0 flex-col bg-neutral-100 md:w-[var(--sidebar-width)]"
      style={{ "--sidebar-width": `${sidebarWidth}px` } as CSSProperties}
    >
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
        <NavRow
          icon={<PlusIcon className="h-4 w-4 text-accent-700" />}
          label="New chat"
          disabled={!newChatAvailable}
          onClick={handleNew}
        />
        <NavRow icon={<ClockIcon />} label="Scheduled" active={view === "scheduled"} onClick={() => onNavigate("scheduled")} />
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

      <div className="scrollbar-slim flex-1 overflow-y-auto px-2 pb-3">
        {threads.length === 0 ? (
          <p className="px-2 py-6 text-center text-xs text-muted-foreground">No chats yet.</p>
        ) : (
          groups.map((group) => (
            <section key={group.label} aria-label={group.label} className="flex flex-col gap-0.5">
              <h2 className="pb-2 pt-4">
                <button
                  type="button"
                  aria-expanded={!collapsedGroups.has(group.label)}
                  onClick={() => toggleGroup(group.label)}
                  className="group flex w-full items-center gap-1 rounded-lg px-2.5 py-1 text-left text-xs font-medium text-muted-foreground transition-colors hover:bg-neutral-200/60 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                >
                  {group.label}
                  <ChevronDownIcon
                    className={cn(
                      "h-3.5 w-3.5 shrink-0 opacity-0 transition-[opacity,transform] group-hover:opacity-100 group-focus-visible:opacity-100",
                      collapsedGroups.has(group.label) && "-rotate-90",
                    )}
                  />
                </button>
              </h2>
              {!collapsedGroups.has(group.label) && group.threads.map((thread) => {
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
        <AccountMenu account={account} onNavigate={onNavigate} onOpenSettings={onOpenSettings} onLogout={onLogout} />
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
