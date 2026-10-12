import { useState } from "react";
import { cn } from "./lib/utils";
import { McpCard } from "./McpCard";
import { GoogleCard } from "./GoogleCard";
import { SkillsPanel } from "./SkillsPanel";

// The tab lives in the query string, so a refresh or a shared link lands on the
// same tab. The address is replaced rather than pushed: switching tabs is not a
// place the owner navigates back to.
type CustomizeTab = "skills" | "connectors";

const TABS: { id: CustomizeTab; label: string }[] = [
  { id: "skills", label: "Skills" },
  { id: "connectors", label: "Connectors" },
];

function tabFromLocation(): CustomizeTab {
  if (typeof window === "undefined") return "skills";
  return new URLSearchParams(window.location.search).get("tab") === "connectors" ? "connectors" : "skills";
}

export function CustomizePage({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [tab, setTab] = useState<CustomizeTab>(tabFromLocation);

  function selectTab(next: CustomizeTab) {
    setTab(next);
    const url = new URL(window.location.href);
    url.searchParams.set("tab", next);
    window.history.replaceState({}, "", url);
  }

  return (
    <main className="app-canvas scrollbar-slim h-full min-h-0 min-w-0 overflow-y-auto">
      <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-5 pb-11 pt-10 sm:px-7">
        <header className="flex flex-col gap-1.5 pb-1">
          <h1 className="text-xl font-semibold tracking-tight">Customize</h1>
          <p className="max-w-2xl text-sm leading-6 text-neutral-700">
            Skills are instructions Eggy can follow. Connectors are external services Eggy can use. Both are shared by everyone on this Eggy.
          </p>
        </header>

        <div role="tablist" aria-label="Customize" className="flex gap-1 border-b border-neutral-200">
          {TABS.map((item) => (
            <button
              key={item.id}
              type="button"
              role="tab"
              id={`customize-tab-${item.id}`}
              aria-selected={tab === item.id}
              aria-controls="customize-panel"
              onClick={() => selectTab(item.id)}
              className={cn(
                "-mb-px min-h-11 border-b-2 px-3 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                tab === item.id ? "border-foreground font-medium text-foreground" : "border-transparent text-neutral-700 hover:text-foreground",
              )}
            >
              {item.label}
            </button>
          ))}
        </div>

        <div id="customize-panel" role="tabpanel" aria-labelledby={`customize-tab-${tab}`} className="flex flex-col gap-6">
          {tab === "skills" && <SkillsPanel onSessionExpired={onSessionExpired} />}
          {tab === "connectors" && (
            <>
              <McpCard onSessionExpired={onSessionExpired} />
              <GoogleCard onSessionExpired={onSessionExpired} />
            </>
          )}
        </div>
      </div>
    </main>
  );
}
