import { useCallback, useEffect, useState } from "react";
import { SessionExpiredError } from "../../lib/api";
import { cn } from "../../lib/utils";
import { CloseIcon, MenuIcon } from "../../components/ui/icons";
import { ErrorBanner } from "../../components/ui/error-banner";
import { SpaceMenu } from "./SpaceMenu";
import { TerminalView } from "./TerminalView";
import {
  INBOX_LABEL,
  SPACE_NAME,
  age,
  commandLabel,
  createSpace,
  deleteSpace,
  killTerminal,
  listSpaces,
  orderedCommands,
  sectionOf,
  spaceNote,
  startTerminal,
  terminalLabel,
  updateRepo,
  type InboxSection,
  type TerminalInfo,
  type TerminalSpace,
} from "./terminal";

// How often the list refreshes. Status changes arrive on the server's event
// stream; the page polls that state rather than holding a second socket.
const REFRESH_MS = 3000;

// The space a first terminal starts in when the owner has made none: starting
// should never wait on naming a folder.
const FIRST_SPACE = "home";

// TerminalPage is one list and one terminal. The list is the spaces, each with
// its terminals and its own start button, with anything waiting on the owner
// pinned above them; picking a row opens it. There are no separate Inbox and
// Spaces views and no tab strip: one place to look, one place to start.
export function TerminalPage({ onClose, onOpenSidebar, onSessionExpired }: { onClose: () => void; onOpenSidebar?: () => void; onSessionExpired: () => void }) {
  const [spaces, setSpaces] = useState<TerminalSpace[] | null>(null);
  const [commands, setCommands] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

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

  const named = (spaces ?? []).filter((s) => s.name !== "");
  const all = (spaces ?? []).flatMap((s) => s.terminals);
  const current = open ? all.find((t) => t.address === open) : undefined;
  const waiting = all.filter((t) => sectionOf(t) === "needs_you");

  async function start(space: string | null, command: string) {
    setError(null);
    setBusy(true);
    try {
      let target = space ?? current?.space ?? named.find((s) => !s.preparing)?.name ?? null;
      if (!target) {
        await createSpace(FIRST_SPACE);
        target = FIRST_SPACE;
      }
      const t = await startTerminal(target, command, 100, 30);
      await refresh();
      setOpen(t.address);
    } catch (err) {
      fail(err);
    } finally {
      setBusy(false);
    }
  }

  async function end(address: string) {
    setError(null);
    try {
      await killTerminal(address);
      setOpen((now) => (now === address ? null : now));
      await refresh();
    } catch (err) {
      fail(err);
    }
  }

  async function removeSpace(name: string) {
    setError(null);
    try {
      await deleteSpace(name);
      setOpen((now) => (now && all.find((t) => t.address === now)?.space === name ? null : now));
      await refresh();
    } catch (err) {
      fail(err);
    }
  }

  async function refreshRepo(name: string) {
    setError(null);
    try {
      await updateRepo(name);
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

  return (
    <div className="flex h-full min-h-0 bg-background">
      {/* The list: the whole screen on a phone until a terminal is open. */}
      <aside className={cn("flex min-h-0 w-full flex-col border-r border-border md:w-[280px] md:shrink-0", open ? "hidden md:flex" : "flex")}>
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
        <div className="scrollbar-slim min-h-0 flex-1 overflow-y-auto px-2 pb-4">
          {spaces === null ? (
            <p className="px-2 text-small text-neutral-700">Loading...</p>
          ) : (
            <>
              {waiting.length > 0 && (
                <section aria-label="Needs you" className="mb-3 rounded-card bg-yolk-soft/60 p-1">
                  <h2 className="px-2 pb-0.5 pt-1 text-caption font-medium text-yolk-ink">Needs you</h2>
                  {waiting.map((t) => (
                    <Row key={t.address} terminal={t} selected={open === t.address} onOpen={setOpen} showSpace />
                  ))}
                </section>
              )}
              {named.map((space) => (
                <SpaceGroup key={space.name} space={space} commands={commands} open={open} busy={busy} onOpen={setOpen} onStart={start} onRemove={end} onDelete={removeSpace} onUpdate={refreshRepo} />
              ))}
              {/* Terminals a program started itself carry no space. */}
              {(spaces.find((s) => s.name === "")?.terminals ?? []).map((t) => (
                <Row key={t.address} terminal={t} selected={open === t.address} onOpen={setOpen} onRemove={end} />
              ))}
              <NewSpace onAdd={addSpace} />
            </>
          )}
        </div>
      </aside>

      {/* The work area. */}
      <section className={cn("min-h-0 min-w-0 flex-1 flex-col", open ? "flex" : "hidden md:flex")}>
        {open && current ? (
          <>
            <TerminalHeader terminal={current} onBack={() => setOpen(null)} onEnd={() => void end(current.address)} />
            <div className="min-h-0 flex-1">
              <TerminalView key={open} address={open} onEnded={() => void refresh()} />
            </div>
          </>
        ) : (
          <EmptyState commands={commands} busy={busy} hasTerminals={all.length > 0} onStart={(command) => void start(null, command)} />
        )}
      </section>
    </div>
  );
}

// The open terminal's own header: what it is, where, and how it is doing.
function TerminalHeader({ terminal, onBack, onEnd }: { terminal: TerminalInfo; onBack: () => void; onEnd: () => void }) {
  const [confirming, setConfirming] = useState(false);
  useEffect(() => setConfirming(false), [terminal.address]);
  const section = sectionOf(terminal);
  return (
    <div className="flex min-h-12 shrink-0 items-center gap-2 border-b border-border px-2">
      <button type="button" onClick={onBack} aria-label="Back to the list" className="flex h-10 w-10 items-center justify-center rounded-control text-neutral-700 hover:bg-neutral-100 md:hidden">
        ←
      </button>
      <StatusMark terminal={terminal} />
      <div className="flex min-w-0 flex-1 flex-col py-1">
        <span className="truncate text-small font-medium">{terminalLabel(terminal)}</span>
        <span className="truncate text-caption text-neutral-600">
          {[terminal.space, terminal.message || (terminal.live ? INBOX_LABEL[section] : "Ended")].filter(Boolean).join(" · ")}
        </span>
      </div>
      {terminal.live &&
        (confirming ? (
          <span className="flex shrink-0 items-center gap-1">
            <button type="button" className="h-8 rounded-control px-2.5 text-small font-medium text-tomato hover:bg-tomato-soft" onClick={onEnd}>
              End it
            </button>
            <button type="button" className="h-8 rounded-control px-2.5 text-small text-neutral-700 hover:bg-neutral-100" onClick={() => setConfirming(false)}>
              Keep
            </button>
          </span>
        ) : (
          <button type="button" title="Stop the program" className="h-8 shrink-0 rounded-control px-2.5 text-small text-neutral-700 hover:bg-neutral-100" onClick={() => setConfirming(true)}>
            End
          </button>
        ))}
    </div>
  );
}

// What the work area shows with nothing open: on a first visit, the way to start.
function EmptyState({ commands, busy, hasTerminals, onStart }: { commands: string[]; busy: boolean; hasTerminals: boolean; onStart: (command: string) => void }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 p-6 text-center">
      <p className="max-w-sm text-small text-neutral-700">
        {hasTerminals ? "Pick a terminal from the list, or start a new one." : "Start Claude Code or a shell. It keeps running when you close the page, and you can come back to it from any device."}
      </p>
      <div className="flex flex-wrap justify-center gap-2">
        {orderedCommands(commands).map((command, i) => (
          <button
            key={command}
            type="button"
            disabled={busy}
            onClick={() => onStart(command)}
            className={cn("h-10 rounded-control px-4 text-small font-medium disabled:opacity-50", i === 0 ? "bg-foreground text-background hover:opacity-90" : "text-foreground ring-1 ring-border hover:bg-neutral-100")}
          >
            {commandLabel(command)}
          </button>
        ))}
      </div>
    </div>
  );
}

// Claude Code first: it is why the extension exists.
function SpaceGroup({
  space,
  commands,
  open,
  busy,
  onOpen,
  onStart,
  onRemove,
  onDelete,
  onUpdate,
}: {
  space: TerminalSpace;
  commands: string[];
  open: string | null;
  busy: boolean;
  onOpen: (address: string) => void;
  onStart: (space: string, command: string) => void;
  onRemove: (address: string) => void;
  onDelete: (space: string) => void;
  onUpdate: (space: string) => Promise<void>;
}) {
  const terminals = [...space.terminals].sort((a, b) => Number(b.live) - Number(a.live) || Date.parse(b.changed) - Date.parse(a.changed));
  const note = spaceNote(space);
  return (
    <section aria-label={space.name} className="mb-2">
      <div className="flex items-center gap-2 px-2 py-1">
        <span className="min-w-0 flex-1 truncate text-caption font-medium uppercase tracking-wide text-neutral-600">{space.name}</span>
        {space.repo && <span title="A configured repository, copied from its mirror" className="shrink-0 rounded-full bg-neutral-100 px-1.5 text-caption text-neutral-600">repo</span>}
        <SpaceMenu space={space} commands={commands} disabled={busy || !!space.preparing} onStart={onStart} onDelete={onDelete} onUpdate={onUpdate} />
      </div>
      {note ? (
        <p className={cn("px-2 pb-1 text-caption", note.problem ? "text-tomato" : "text-neutral-500")} title={space.error}>
          {note.text}
        </p>
      ) : (
        terminals.map((t) => <Row key={t.address} terminal={t} selected={open === t.address} onOpen={onOpen} onRemove={onRemove} />)
      )}
    </section>
  );
}

function Row({
  terminal,
  selected,
  onOpen,
  onRemove,
  showSpace = false,
}: {
  terminal: TerminalInfo;
  selected: boolean;
  onOpen: (address: string) => void;
  onRemove?: (address: string) => void;
  showSpace?: boolean;
}) {
  const detail = [showSpace ? terminal.space : "", terminal.message].filter(Boolean).join(" · ");
  return (
    <div className={cn("group flex min-h-11 items-center rounded-control", selected ? "bg-neutral-100" : "hover:bg-neutral-100", !terminal.live && "opacity-60")}>
      <button type="button" onClick={() => onOpen(terminal.address)} className="flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left">
        <StatusMark terminal={terminal} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-small text-foreground">{terminalLabel(terminal)}</span>
          {detail && <span className="truncate text-caption text-neutral-600">{detail}</span>}
        </span>
        <span className="shrink-0 text-caption text-neutral-600">{terminal.live ? age(terminal.changed) : "Ended"}</span>
      </button>
      {/* An ended terminal is only a record of what ran; clearing it is safe. */}
      {onRemove && !terminal.live && (
        <button type="button" aria-label="Clear" title="Clear" onClick={() => onRemove(terminal.address)} className="mr-1 flex h-8 w-8 shrink-0 items-center justify-center rounded-mark text-neutral-500 hover:text-foreground">
          ×
        </button>
      )}
    </div>
  );
}

// A new space is a folder name; it is typed in place, not in a form of its own.
function NewSpace({ onAdd }: { onAdd: (name: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState("");
  const valid = SPACE_NAME.test(name);
  if (!editing) {
    return (
      <button type="button" onClick={() => setEditing(true)} className="mt-1 flex h-9 w-full items-center rounded-control px-2 text-left text-small text-neutral-700 hover:bg-neutral-100">
        + New space
      </button>
    );
  }
  return (
    <form
      className="mt-1 flex gap-1 px-1"
      onSubmit={(event) => {
        event.preventDefault();
        if (valid) {
          onAdd(name);
          setName("");
          setEditing(false);
        }
      }}
    >
      <input
        autoFocus
        value={name}
        onChange={(event) => setName(event.target.value.toLowerCase().replace(/\s+/g, "-"))}
        onKeyDown={(event) => event.key === "Escape" && setEditing(false)}
        onBlur={() => name === "" && setEditing(false)}
        placeholder="space name, e.g. my-app"
        aria-label="New space name"
        className="h-9 min-w-0 flex-1 rounded-control bg-neutral-100 px-2 text-field md:text-small"
      />
      <button type="submit" disabled={!valid} className="h-9 rounded-control px-2.5 text-small font-medium text-foreground ring-1 ring-border disabled:opacity-40">
        Add
      </button>
    </form>
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
  return <span aria-label={terminal.live ? INBOX_LABEL[section] : "Ended"} className={cn("inline-block h-2.5 w-2.5 shrink-0 rounded-full", terminal.live ? MARK[section] : "bg-neutral-300")} />;
}
