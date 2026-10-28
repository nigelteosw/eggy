import { useEffect, useRef, useState } from "react";
import { SessionExpiredError, type Account, type Theme, applyTheme, setAgentTheme } from "../lib/api";
import { ChevronDownIcon, LogoutIcon, SettingsIcon, TraceIcon } from "../components/ui/icons";
import type { View } from "./routing";

// The owner's row at the foot of the rail. Everything that used to sit in the
// top bar's right-hand corner lives behind it: where the owner's settings and
// traces are, and signing out. The menu opens upward because the row is at the
// bottom of the window.
export function AccountMenu({
  account,
  onNavigate,
  onOpenSettings,
  onLogout,
  theme,
  onThemeChange,
}: {
  account?: Account;
  onNavigate: (view: View) => void;
  onOpenSettings: () => void;
  onLogout: () => void;
  theme?: Theme;
  onThemeChange?: (theme: Theme) => void;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement | null>(null);
  const name = account?.name || account?.username || "Eggy";

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  function choose(action: () => void) {
    setOpen(false);
    action();
  }

  return (
    <div ref={root} className="relative">
      {open && (
        <div
          role="menu"
          aria-label="Account"
          className="absolute inset-x-0 bottom-full z-20 mb-2 overflow-hidden rounded-2xl bg-card py-1.5 shadow-lift"
        >
          <p className="truncate px-3.5 pb-1.5 pt-1 text-xs text-muted-foreground" title={name}>
            {name}
          </p>
          <MenuItem icon={<SettingsIcon />} label="Settings" onClick={() => choose(onOpenSettings)} />
          <MenuItem icon={<TraceIcon />} label="Traces" onClick={() => choose(() => onNavigate("traces"))} />
          {theme && onThemeChange && (
            <>
              <div aria-hidden="true" className="my-1 h-px bg-border" />
              <ThemeSwitch theme={theme} onChange={onThemeChange} />
            </>
          )}
          <div aria-hidden="true" className="my-1 h-px bg-border" />
          <MenuItem icon={<LogoutIcon />} label="Sign out" onClick={() => choose(onLogout)} />
        </div>
      )}
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
        className="flex h-12 w-full items-center gap-2.5 rounded-2xl px-2.5 text-left transition-colors hover:bg-neutral-200/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        <span
          aria-hidden="true"
          className="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-accent-100 text-xs font-semibold text-accent-700"
        >
          {name.charAt(0).toUpperCase()}
        </span>
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{name}</span>
        <ChevronDownIcon className="h-4 w-4 shrink-0 text-muted-foreground" />
      </button>
    </div>
  );
}

// ThemeSwitch is the account's palette in one tap from anywhere. It is the same
// stored choice as My settings, so the two cannot disagree.
const THEME_CHOICES: { value: Theme; label: string }[] = [
  { value: "system", label: "Match system" },
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
];

function ThemeSwitch({ theme, onChange }: { theme: Theme; onChange: (theme: Theme) => void }) {
  async function choose(next: Theme) {
    if (next === theme) return;
    onChange(next);
    applyTheme(next);
    try {
      await setAgentTheme(next);
    } catch (err) {
      // Put the previous palette back rather than showing a choice the server
      // did not keep.
      onChange(theme);
      applyTheme(theme);
      if (err instanceof SessionExpiredError) return;
    }
  }
  return (
    <div role="group" aria-label="Theme" className="mx-2.5 my-1 flex rounded-xl bg-neutral-100 p-0.5">
      {THEME_CHOICES.map((choice) => (
        <button
          key={choice.value}
          type="button"
          aria-pressed={theme === choice.value}
          onClick={() => choose(choice.value)}
          className={`flex h-9 flex-1 items-center justify-center rounded-lg text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 ${
            theme === choice.value ? "bg-card text-foreground shadow-subtle" : "text-neutral-700 hover:text-foreground"
          }`}
        >
          {choice.label}
        </button>
      ))}
    </div>
  );
}

function MenuItem({ icon, label, onClick }: { icon: React.ReactNode; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      className="flex h-11 w-full items-center gap-3 px-3.5 text-left text-sm text-foreground/90 transition-colors hover:bg-neutral-100 focus-visible:outline-none focus-visible:bg-neutral-100"
    >
      <span className="flex h-4 w-4 shrink-0 items-center justify-center text-muted-foreground">{icon}</span>
      {label}
    </button>
  );
}
