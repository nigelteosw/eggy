import { useEffect, useState } from "react";
import { cn } from "./lib/utils";
import { McpCard } from "./McpCard";
import { GoogleCard } from "./GoogleCard";
import { SkillsPanel } from "./SkillsPanel";
import { SkillDetail } from "./SkillDetail";
import { Input } from "./components/ui/input";
import { PRIMARY_BUTTON } from "./components/ui/form";
import { SearchIcon } from "./components/ui/icons";
import { PluginsTab } from "./extensions/marketplace/PluginsTab";
import { pluginsEnabled } from "./extensions/marketplace/marketplace";

// The page's address holds where the owner is, so refresh, a shared link and
// browser back all land in the same place. `skill` is "new" for the create form,
// a skill name for its detail page, or absent for the list.
type Tab = "skills" | "connectors" | "plugins";
type Route = { tab: Tab; skill: string | null };

const TABS: { id: Tab; label: string }[] = [
  { id: "skills", label: "Skills" },
  { id: "connectors", label: "Connectors" },
];

// Plugins is offered only when the marketplace runs, so it costs nothing and
// shows nothing otherwise.
function tabsFor(features: string[]): { id: Tab; label: string }[] {
  return pluginsEnabled(features) ? [...TABS, { id: "plugins", label: "Plugins" }] : TABS;
}

function readRoute(): Route {
  if (typeof window === "undefined") return { tab: "skills", skill: null };
  const params = new URLSearchParams(window.location.search);
  const tab = params.get("tab");
  return { tab: tab === "connectors" || tab === "plugins" ? tab : "skills", skill: params.get("skill") };
}

function routeUrl(route: Route): string {
  const params = new URLSearchParams({ tab: route.tab });
  if (route.skill) params.set("skill", route.skill);
  return `${window.location.pathname}?${params}`;
}

export function CustomizePage({ onSessionExpired, features }: { onSessionExpired: () => void; features: string[] }) {
  const [route, setRoute] = useState<Route>(readRoute);
  const [query, setQuery] = useState("");

  // Opening a skill pushes history so browser back returns to the list; tab
  // switches replace the entry, because switching tabs is not somewhere to go back to.
  function go(next: Route, push = true) {
    const url = routeUrl(next);
    if (push) window.history.pushState({}, "", url);
    else window.history.replaceState({}, "", url);
    setRoute(next);
  }

  useEffect(() => {
    const handlePopState = () => setRoute(readRoute());
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  // A ?tab=plugins link on a deployment without the marketplace lands on Skills
  // rather than on an empty panel.
  useEffect(() => {
    if (route.tab === "plugins" && !pluginsEnabled(features)) go({ tab: "skills", skill: null }, false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [route.tab, features]);

  const backToList = () => go({ tab: "skills", skill: null });

  return (
    <main className="app-canvas scrollbar-slim h-full min-h-0 min-w-0 overflow-y-auto">
      <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-5 pb-11 pt-10 sm:px-7">
        {route.tab === "skills" && route.skill ? (
          <SkillDetail
            key={route.skill}
            name={route.skill}
            onBack={backToList}
            onSessionExpired={onSessionExpired}
            onSaved={(saved) => go({ tab: "skills", skill: saved }, false)}
          />
        ) : (
          <>
            <header className="flex flex-col gap-1.5 pb-1">
              <h1 className="text-xl font-semibold tracking-tight">Customize</h1>
              <p className="max-w-2xl text-sm leading-6 text-neutral-700">Skills and connectors shared by everyone on this Eggy.</p>
            </header>

            <div className="flex flex-wrap items-center gap-3">
              <div role="tablist" aria-label="Customize" className="flex gap-1 rounded-xl bg-neutral-100 p-1">
                {tabsFor(features).map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    role="tab"
                    id={`customize-tab-${item.id}`}
                    aria-selected={route.tab === item.id}
                    aria-controls="customize-panel"
                    onClick={() => go({ tab: item.id, skill: null }, false)}
                    className={cn(
                      "min-h-9 rounded-lg px-3.5 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                      route.tab === item.id ? "bg-background font-medium text-foreground shadow-sm" : "text-neutral-700 hover:text-foreground",
                    )}
                  >
                    {item.label}
                  </button>
                ))}
              </div>

              {route.tab === "skills" && (
                <div className="ml-auto flex items-center gap-2">
                  <label className="flex h-10 w-full max-w-[260px] items-center gap-2 rounded-xl bg-neutral-100 px-3 text-neutral-700 focus-within:ring-2 focus-within:ring-ring/40">
                    <SearchIcon className="h-4 w-4 shrink-0" />
                    <Input
                      type="search"
                      value={query}
                      onChange={(event) => setQuery(event.target.value)}
                      placeholder="Search skills"
                      aria-label="Search skills"
                      className="h-9 min-w-0 flex-1 border-0 bg-transparent p-0 text-sm shadow-none focus-visible:ring-0"
                    />
                  </label>
                  <button type="button" onClick={() => go({ tab: "skills", skill: "new" })} className={cn("shrink-0", PRIMARY_BUTTON)}>
                    + Add skill
                  </button>
                </div>
              )}
            </div>

            <div id="customize-panel" role="tabpanel" aria-labelledby={`customize-tab-${route.tab}`} className="flex flex-col gap-6">
              {route.tab === "skills" && (
                <SkillsPanel query={query} onOpen={(name) => go({ tab: "skills", skill: name })} onSessionExpired={onSessionExpired} />
              )}
              {route.tab === "plugins" && pluginsEnabled(features) && <PluginsTab onSessionExpired={onSessionExpired} />}
              {route.tab === "connectors" && (
                <>
                  <McpCard onSessionExpired={onSessionExpired} />
                  <GoogleCard onSessionExpired={onSessionExpired} />
                </>
              )}
            </div>
          </>
        )}
      </div>
    </main>
  );
}
