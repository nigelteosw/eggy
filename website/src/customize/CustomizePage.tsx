import { PageHeader } from "../components/ui/page-header";
import { useEffect, useState } from "react";
import { cn } from "../lib/utils";
import { McpCard } from "./McpCard";
import { GoogleCard } from "./GoogleCard";
import { SkillsPanel } from "./SkillsPanel";
import { SkillDetail } from "./SkillDetail";
import { Input } from "../components/ui/input";
import { PRIMARY_BUTTON } from "../components/ui/form";
import { SearchIcon } from "../components/ui/icons";
import { PluginsTab } from "../extensions/marketplace/PluginsTab";
import { FinanceCard } from "../extensions/finance/FinanceCard";
import { MarketplaceCard } from "../extensions/marketplace/MarketplaceCard";
import { AgentRunnerCard } from "../extensions/agent-runner/AgentRunnerCard";
import { SharedLabel } from "../components/ui/shared-label";
import { TelegramCard } from "./TelegramCard";
import { DiscordCard } from "./DiscordCard";
import { pluginsEnabled } from "../extensions/marketplace/marketplace";

// The page's address holds where the owner is, so refresh, a shared link and
// browser back all land in the same place. `skill` is "new" for the create form,
// a skill name for its detail page, or absent for the list.
// Grouped by what a person is doing: where they talk to Eggy (Channels), what
// Eggy can reach (Connectors), procedures it follows (Skills), and optional
// features (Extensions). Plugins -- marketplace installs -- only while the
// marketplace runs.
type Tab = "channels" | "connectors" | "skills" | "extensions" | "plugins";
type Route = { tab: Tab; skill: string | null };

const TABS: { id: Tab; label: string }[] = [
  { id: "channels", label: "Channels" },
  { id: "connectors", label: "Connectors" },
  { id: "skills", label: "Skills" },
  { id: "extensions", label: "Extensions" },
];
const TAB_IDS: Tab[] = ["channels", "connectors", "skills", "extensions", "plugins"];

// Plugins is offered only when the marketplace runs, so it costs nothing and
// shows nothing otherwise.
export function tabsFor(features: string[]): { id: Tab; label: string }[] {
  return pluginsEnabled(features) ? [...TABS, { id: "plugins", label: "Plugins" }] : TABS;
}

function readRoute(): Route {
  if (typeof window === "undefined") return { tab: "skills", skill: null };
  const params = new URLSearchParams(window.location.search);
  const tab = params.get("tab");
  return { tab: TAB_IDS.includes(tab as Tab) ? (tab as Tab) : "skills", skill: params.get("skill") };
}

function routeUrl(route: Route): string {
  const params = new URLSearchParams({ tab: route.tab });
  if (route.skill) params.set("skill", route.skill);
  return `${window.location.pathname}?${params}`;
}

export function CustomizePage({ onSessionExpired, features, onOpenSidebar }: { onSessionExpired: () => void; features: string[]; onOpenSidebar?: () => void }) {
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
            <PageHeader
              title="Customize"
              onOpenSidebar={onOpenSidebar}
              description={
                <span className="flex flex-wrap items-center gap-2">
                  Where you talk to Eggy, what it can reach, and what it knows how to do. <SharedLabel />
                </span>
              }
              className="pb-1"
            />

            <div className="flex flex-wrap items-center gap-3">
              <div role="tablist" aria-label="Customize" className="scrollbar-none -mx-1 flex max-w-full gap-1 overflow-x-auto rounded-control bg-neutral-100 p-1">
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
                      "min-h-11 shrink-0 rounded-control px-3.5 text-small transition-colors sm:min-h-9 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                      route.tab === item.id ? "bg-background font-medium text-foreground shadow-sm" : "text-neutral-700 hover:text-foreground",
                    )}
                  >
                    {item.label}
                  </button>
                ))}
              </div>

              {route.tab === "skills" && (
                <div className="ml-auto flex items-center gap-2">
                  <label className="flex h-10 w-full max-w-[260px] items-center gap-2 rounded-control bg-neutral-100 px-3 text-neutral-700 focus-within:ring-2 focus-within:ring-ring/40">
                    <SearchIcon className="h-4 w-4 shrink-0" />
                    <Input
                      type="search"
                      value={query}
                      onChange={(event) => setQuery(event.target.value)}
                      placeholder="Search skills"
                      aria-label="Search skills"
                      className="h-11 min-w-0 flex-1 border-0 bg-transparent p-0 text-field shadow-none focus-visible:ring-0 sm:h-9 sm:text-small"
                    />
                  </label>
                  <button type="button" onClick={() => go({ tab: "skills", skill: "new" })} className={cn(PRIMARY_BUTTON, "min-h-11 shrink-0 sm:min-h-10")}>
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
              {route.tab === "channels" && (
                <>
                  <TelegramCard onSessionExpired={onSessionExpired} />
                  <DiscordCard onSessionExpired={onSessionExpired} />
                </>
              )}
              {route.tab === "connectors" && (
                <>
                  <GoogleCard onSessionExpired={onSessionExpired} />
                  <McpCard onSessionExpired={onSessionExpired} />
                </>
              )}
              {route.tab === "extensions" && (
                <>
                  <FinanceCard onSessionExpired={onSessionExpired} running={features.includes("finance")} />
                  <AgentRunnerCard onSessionExpired={onSessionExpired} running={features.includes("agent_runner")} />
                  <MarketplaceCard onSessionExpired={onSessionExpired} running={features.includes("marketplace")} />
                </>
              )}
            </div>
          </>
        )}
      </div>
    </main>
  );
}
