import { useEffect, useRef, useState } from "react";
import { commandLabel, orderedCommands, type TerminalSpace } from "./terminal";

const ITEM = "flex h-10 w-full items-center rounded-control px-3 text-left text-small";

// SpaceMenu is a space's + button and what it offers: start a program, then
// the one operation the space's kind allows. A repository space fetches from
// GitHub; any other space can be deleted, after a second click that says what
// goes with it.
export function SpaceMenu({
  space,
  commands,
  disabled,
  onStart,
  onDelete,
  onUpdate,
}: {
  space: TerminalSpace;
  commands: string[];
  disabled: boolean;
  onStart: (space: string, command: string) => void;
  onDelete: (space: string) => void;
  onUpdate: (space: string) => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [updating, setUpdating] = useState(false);
  const root = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!open) return;
    const away = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open]);
  const live = space.terminals.filter((t) => t.live).length;

  return (
    <div ref={root} className="relative">
      <button
        type="button"
        aria-label={`Start in ${space.name}`}
        aria-expanded={open}
        title={`Start in ${space.name}`}
        disabled={disabled}
        onClick={() => {
          setConfirmDelete(false);
          setOpen((v) => !v);
        }}
        className="flex h-7 w-7 items-center justify-center rounded-mark text-base text-neutral-700 hover:bg-neutral-100 disabled:opacity-40"
      >
        +
      </button>
      {open && (
        <div role="menu" className="absolute right-0 top-full z-20 mt-1 w-44 overflow-hidden rounded-card bg-card p-1 shadow-lift">
          {orderedCommands(commands).map((command) => (
            <button
              key={command}
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false);
                onStart(space.name, command);
              }}
              className={`${ITEM} hover:bg-neutral-100`}
            >
              {commandLabel(command)}
            </button>
          ))}
          <div className="my-1 border-t border-border" />
          {space.repo ? (
            <button
              type="button"
              role="menuitem"
              disabled={updating}
              onClick={async () => {
                setUpdating(true);
                try {
                  await onUpdate(space.name);
                } finally {
                  setUpdating(false);
                  setOpen(false);
                }
              }}
              className={`${ITEM} hover:bg-neutral-100 disabled:opacity-50`}
            >
              {updating ? "Fetching..." : "Fetch from GitHub"}
            </button>
          ) : confirmDelete ? (
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false);
                onDelete(space.name);
              }}
              className={`${ITEM} h-auto min-h-10 py-1.5 font-medium text-tomato hover:bg-tomato-soft`}
            >
              {live > 0 ? `Delete folder and end ${live} terminal${live === 1 ? "" : "s"}` : "Delete folder and its files"}
            </button>
          ) : (
            <button type="button" role="menuitem" onClick={() => setConfirmDelete(true)} className={`${ITEM} text-tomato hover:bg-tomato-soft`}>
              Delete space...
            </button>
          )}
        </div>
      )}
    </div>
  );
}
