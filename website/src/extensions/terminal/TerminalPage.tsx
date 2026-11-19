import { useCallback, useEffect, useState } from "react";
import { SessionExpiredError } from "../../lib/api";
import { cn } from "../../lib/utils";
import { CloseIcon, MenuIcon } from "../../components/ui/icons";
import { ErrorBanner } from "../../components/ui/error-banner";
import { TerminalView } from "./TerminalView";
import {
  INBOX_LABEL,
  SPACE_NAME,
  age,
  commandLabel,
  createSpace,
  inbox,
  killTerminal,
  listSpaces,
  sectionOf,
  startTerminal,
  terminalLabel,
  type InboxSection,
  type TerminalInfo,
  type TerminalSpace,
} from "./terminal";

// How often the lists refresh. Status changes arrive on the server's event
// stream; the page polls that state rather than holding a second socket.
const REFRESH_MS = 3000;

// TerminalPage: spaces and their agents on the left, the open terminal on the
// right. On a phone the list is the home screen (an Inbox sorted by what needs
// you) and a terminal opens full screen over it.
export function TerminalPage({ onClose, onOpenSidebar, onSessionExpired }: { onClose: () => void; onOpenSidebar?: () => void; onSessionExpired: () => void }) {
  const [spaces, setSpaces] = useState<TerminalSpace[] | null>(null);
  const [commands, setCommands] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [tabs, setTabs] = useState<string[]>([]);
  const [view, setView] = useState<"inbox" | "spaces">("inbox");

  const fail = useCallback(
    (err: unknown) => {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : String(err));
    },
    [onSessionExpired],
  );

  const refresh = useCallback(async () => {
    try {
      const result = await listSpaces();
      setSpaces(result.spaces);
      setCommands(result.commands);
    } catch (err) {
      fail(err);
    }
  }, [fail]);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [refresh]);

  const all = (spaces ?? []).flatMap((s) => s.terminals);
  const find = (address: string) => all.find((t) => t.address === address);

  const show = (address: string) => {
    setTabs((current) => (current.includes(address) ? current : [...current, address]));
    setOpen(address);
  };

  async function start(space: string, command: string) {
    setError(null);
    try {
      const t = await startTerminal(space, command, 100, 30);
      await refresh();
      show(t.address);
    } catch (err) {
      fail(err);
    }
  }

  async function kill(address: string) {
    setError(null);
    try {
      await killTerminal(address);
      setTabs((current) => current.filter((a) => a !== address));
      setOpen((current) => (current === address ? null : current));
      await refresh();
    } catch (err) {
      fail(err);
    }
  }

  async function addSpace(name: string) {
    setError(null);
    try {
      await createSpace(name);
      await refresh();
    } catch (err) {
      fail(err);
    }
  }

  const sidebar = (
    <Sidebar
      spaces={spaces}
      commands={commands}
      view={view}
      open={open}
      onView={setView}
      onOpen={show}
      onStart={start}
      onAddSpace={addSpace}
    />
  );

  return (
    <div className="flex h-full min-h-0 bg-background">
      {/* The list: the whole screen on a phone until a terminal is open. */}
      <aside className={cn("flex min-h-0 w-full flex-col border-r border-border md:w-[268px] md:shrink-0", open ? "hidden md:flex" : "flex")}>
        <header className="flex min-h-14 items-center gap-1 px-3">
          {onOpenSidebar && (
            <button type="button" onClick={onOpenSidebar} aria-label="Open sidebar" className="flex h-11 w-11 items-center justify-center rounded-control text-neutral-700 hover:bg-neutral-100 md:hidden">
              <MenuIcon />
            </button>
          )}
          <h1 className="min-w-0 flex-1 truncate text-title font-semibold">Terminal</h1>
          <button type="button" onClick={onClose} aria-label="Close" title="Close" className="flex h-11 w-11 items-center justify-center rounded-control text-neutral-700 hover:bg-neutral-100">
            <CloseIcon />
          </button>
        </header>
        {error && (
          <div className="px-3 pb-2">
            <ErrorBanner>{error}</ErrorBanner>
          </div>
        )}
        {sidebar}
      </aside>

      {/* The work area. */}
      <section className={cn("min-h-0 min-w-0 flex-1 flex-col", open ? "flex" : "hidden md:flex")}>
        <div className="flex h-[42px] shrink-0 items-center gap-1 overflow-x-auto border-b border-border px-2">
          <button type="button" className="mr-1 flex h-9 items-center rounded-control px-2 text-small text-neutral-700 hover:bg-neutral-100 md:hidden" onClick={() => setOpen(null)}>
            ← Inbox
          </button>
          {tabs.map((address) => {
            const t = find(address);
            return (
              <div
                key={address}
                className={cn("flex h-8 shrink-0 items-center gap-1.5 rounded-control pl-2.5 pr-1 text-small", open === address ? "bg-neutral-100 text-foreground" : "text-neutral-700 hover:bg-neutral-100")}
              >
                <button type="button" className="flex items-center gap-1.5" onClick={() => setOpen(address)}>
                  {t && <StatusMark terminal={t} />}
                  <span className="max-w-40 truncate">{t ? terminalLabel(t) : address}</span>
                </button>
                <button
                  type="button"
                  aria-label="Close tab"
                  title="Close tab (the program keeps running)"
                  className="flex h-6 w-6 items-center justify-center rounded-mark text-neutral-500 hover:text-foreground"
                  onClick={() => {
                    setTabs((current) => current.filter((a) => a !== address));
                    setOpen((current) => (current === address ? null : current));
                  }}
                >
                  ×
                </button>
              </div>
            );
          })}
          {open && find(open)?.live && (
            <button type="button" className="ml-auto h-8 shrink-0 rounded-control px-2.5 text-small text-tomato hover:bg-tomato-soft" onClick={() => void kill(open)}>
              End program
            </button>
          )}
        </div>
        <div className="min-h-0 flex-1">
          {open ? (
            <TerminalView key={open} address={open} onEnded={() => void refresh()} />
          ) : (
            <div className="flex h-full items-center justify-center p-6 text-center text-small text-neutral-700">
              {all.length === 0 ? "Make a space, then start a shell or Claude Code in it." : "Pick a terminal on the left."}
            </div>
          )}
        </div>
      </section>
    </div>
  );
}

function Sidebar({
  spaces,
  commands,
  view,
  open,
  onView,
  onOpen,
  onStart,
  onAddSpace,
}: {
  spaces: TerminalSpace[] | null;
  commands: string[];
  view: "inbox" | "spaces";
  open: string | null;
  onView: (view: "inbox" | "spaces") => void;
  onOpen: (address: string) => void;
  onStart: (space: string, command: string) => void;
  onAddSpace: (name: string) => void;
}) {
  if (spaces === null) {
    return <p className="px-4 text-small text-neutral-700">Loading...</p>;
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div role="tablist" aria-label="Terminal lists" className="mx-3 mb-2 grid grid-cols-2 gap-1 rounded-control bg-neutral-100 p-1">
        {(["inbox", "spaces"] as const).map((name) => (
          <button
            key={name}
            role="tab"
            type="button"
            aria-selected={view === name}
            onClick={() => onView(name)}
            className={cn("h-9 rounded-mark text-small", view === name ? "bg-background font-medium text-foreground shadow-subtle" : "text-neutral-700")}
          >
            {name === "inbox" ? "Inbox" : "Spaces"}
          </button>
        ))}
      </div>
      <div className="scrollbar-slim min-h-0 flex-1 overflow-y-auto px-2 pb-4">
        {view === "inbox" ? <InboxList spaces={spaces} open={open} onOpen={onOpen} /> : <SpacesList spaces={spaces} commands={commands} open={open} onOpen={onOpen} onStart={onStart} onAddSpace={onAddSpace} />}
      </div>
    </div>
  );
}

export function InboxList({ spaces, open, onOpen }: { spaces: TerminalSpace[]; open: string | null; onOpen: (address: string) => void }) {
  const groups = inbox(spaces);
  if (groups.length === 0) {
    return <p className="px-2 py-4 text-small text-neutral-700">No terminals yet. Open Spaces to start one.</p>;
  }
  return (
    <div className="flex flex-col gap-3">
      {groups.map((group) => (
        <section key={group.section} aria-label={INBOX_LABEL[group.section]}>
          <h2 className="px-2 pb-1 text-caption font-medium text-neutral-600">{INBOX_LABEL[group.section]}</h2>
          {group.terminals.map((t) => (
            <AgentRow key={t.address} terminal={t} selected={open === t.address} onOpen={onOpen} showSpace />
          ))}
        </section>
      ))}
    </div>
  );
}

function SpacesList({
  spaces,
  commands,
  open,
  onOpen,
  onStart,
  onAddSpace,
}: {
  spaces: TerminalSpace[];
  commands: string[];
  open: string | null;
  onOpen: (address: string) => void;
  onStart: (space: string, command: string) => void;
  onAddSpace: (name: string) => void;
}) {
  const [name, setName] = useState("");
  const valid = SPACE_NAME.test(name);
  return (
    <div className="flex flex-col gap-3">
      {spaces
        .filter((s) => s.name !== "")
        .map((space) => (
          <section key={space.name} aria-label={space.name}>
            <div className="flex items-center gap-2 px-2 py-1.5">
              <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-mark bg-accent-200 text-caption font-semibold uppercase text-accent-900">{space.name[0]}</span>
              <span className="min-w-0 flex-1 truncate text-small font-medium">{space.name}</span>
            </div>
            {space.terminals.map((t) => (
              <AgentRow key={t.address} terminal={t} selected={open === t.address} onOpen={onOpen} />
            ))}
            <div className="flex flex-wrap gap-1 px-2 pt-1">
              {commands.map((command) => (
                <button key={command} type="button" onClick={() => onStart(space.name, command)} className="h-8 rounded-control px-2 text-caption text-neutral-700 ring-1 ring-border hover:bg-neutral-100">
                  + {commandLabel(command)}
                </button>
              ))}
            </div>
          </section>
        ))}
      <form
        className="flex gap-1 px-2 pt-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) {
            onAddSpace(name);
            setName("");
          }
        }}
      >
        <input
          value={name}
          onChange={(event) => setName(event.target.value.toLowerCase())}
          placeholder="new space"
          aria-label="New space name"
          className="h-9 min-w-0 flex-1 rounded-control bg-neutral-100 px-2 text-field md:text-small"
        />
        <button type="submit" disabled={!valid} className="h-9 rounded-control px-2.5 text-small font-medium text-foreground ring-1 ring-border disabled:opacity-40">
          Add
        </button>
      </form>
    </div>
  );
}

function AgentRow({ terminal, selected, onOpen, showSpace = false }: { terminal: TerminalInfo; selected: boolean; onOpen: (address: string) => void; showSpace?: boolean }) {
  const section = sectionOf(terminal);
  return (
    <button
      type="button"
      onClick={() => onOpen(terminal.address)}
      className={cn("flex min-h-11 w-full items-center gap-2 rounded-control px-2 py-1.5 text-left", selected ? "bg-neutral-100" : "hover:bg-neutral-100")}
    >
      <StatusMark terminal={terminal} />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-small text-foreground">{terminalLabel(terminal)}</span>
        {(showSpace || terminal.message) && (
          <span className="truncate text-caption text-neutral-600">{[showSpace ? terminal.space : "", terminal.message].filter(Boolean).join(" · ")}</span>
        )}
      </span>
      {section === "needs_you" ? (
        <span className="shrink-0 rounded-full bg-yolk-soft px-2 py-0.5 text-caption font-medium text-yolk-ink">Needs you</span>
      ) : (
        <span className="shrink-0 text-caption text-neutral-600">{age(terminal.changed)}</span>
      )}
    </button>
  );
}

const MARK: Record<InboxSection, string> = {
  needs_you: "bg-yolk-ink",
  done: "bg-accent-500",
  working: "border-2 border-accent-500 border-t-transparent animate-spin",
  idle: "bg-neutral-400",
};

export function StatusMark({ terminal }: { terminal: TerminalInfo }) {
  const section = sectionOf(terminal);
  return <span aria-label={INBOX_LABEL[section]} className={cn("inline-block h-2.5 w-2.5 shrink-0 rounded-full", MARK[section])} />;
}
