import * as React from "react";
import { cn } from "../../lib/utils";

// A collapsible nav rail. Written here rather than pulled from shadcn's
// sidebar block: that block brings a provider, a context, keyboard shortcuts,
// mobile sheets, and several more Radix packages, and this needs a width
// transition and a label fade. The other components in this directory are
// hand-written copies too, so this follows them.
//
// Collapsed state lives in localStorage rather than in owner config: it is
// ephemeral per-device UI state, unlike the theme, which is a preference the
// owner should not have to set again on another machine.

const COLLAPSE_KEY = "eggy.sidebar.collapsed";

// A boolean that survives a reload, for the two rails that remember their
// width. Private-mode Safari throws on localStorage rather than returning
// null, and a rail that cannot remember its width is still a usable rail, so
// both ends of this swallow the failure.
export function useStoredFlag(key: string, fallback: boolean): [boolean, (value: boolean) => void] {
  const [value, setValue] = React.useState(() => {
    try {
      const stored = window.localStorage.getItem(key);
      return stored === null ? fallback : stored === "true";
    } catch {
      return fallback;
    }
  });
  const update = React.useCallback(
    (next: boolean) => {
      setValue(next);
      try {
        window.localStorage.setItem(key, String(next));
      } catch {
        /* see above */
      }
    },
    [key],
  );
  return [value, update];
}

export function useSidebarCollapsed(): [boolean, (collapsed: boolean) => void] {
  return useStoredFlag(COLLAPSE_KEY, false);
}

export function Sidebar({
  collapsed,
  className,
  children,
  ...props
}: React.HTMLAttributes<HTMLElement> & { collapsed: boolean }) {
  return (
    <nav
      className={cn(
        "flex h-full flex-col gap-1 border-r border-border bg-background transition-[width] duration-200 ease-out",
        collapsed ? "w-14 px-2 py-3" : "w-56 px-3 py-3",
        className,
      )}
      {...props}
    >
      {children}
    </nav>
  );
}

export function SidebarItem({
  icon,
  label,
  active = false,
  collapsed,
  className,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  icon: React.ReactNode;
  label: string;
  active?: boolean;
  collapsed: boolean;
}) {
  return (
    <button
      type="button"
      // The accessible name comes from the title/aria-label rather than from
      // the span, because the span is removed outright when collapsed --
      // hiding it with opacity alone would leave a 0-width label absorbing
      // clicks at the edge of a 56px rail.
      title={collapsed ? label : undefined}
      aria-label={label}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex h-9 shrink-0 items-center gap-3 rounded-md text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
        collapsed ? "w-9 justify-center px-0" : "w-full px-2.5",
        active ? "bg-muted text-foreground" : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
        className,
      )}
      {...props}
    >
      <span className="flex h-4 w-4 shrink-0 items-center justify-center">{icon}</span>
      {!collapsed && <span className="truncate">{label}</span>}
    </button>
  );
}

export function SidebarSeparator() {
  return <div className="my-2 h-px shrink-0 bg-border" />;
}
