import { useState } from "react";
import { cn } from "../../lib/utils";
import { DiscoverView } from "./DiscoverView";
import { InstalledView } from "./InstalledView";
import { SourcesCard } from "./SourcesCard";
import { PLUGIN_TABS, PluginTab } from "./marketplace";

const LABELS: Record<PluginTab, string> = { discover: "Discover", installed: "Installed", sources: "Sources" };

// The Plugins tab: browse what sources offer, see what is installed, and edit
// the sources themselves. It is mounted only when the marketplace runs.
export function PluginsTab({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [tab, setTab] = useState<PluginTab>("discover");
  const [installedVersion, setInstalledVersion] = useState(0);

  return (
    <div className="flex flex-col gap-4">
      <div role="tablist" aria-label="Plugins" className="flex gap-1 rounded-xl bg-neutral-100 p-1">
        {PLUGIN_TABS.map((id) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={tab === id}
            onClick={() => setTab(id)}
            className={cn("min-h-9 rounded-lg px-3.5 text-sm", tab === id ? "bg-background font-medium shadow-sm" : "text-neutral-700")}
          >
            {LABELS[id]}
          </button>
        ))}
      </div>
      {tab === "discover" && (
        <DiscoverView
          onSessionExpired={onSessionExpired}
          onInstalled={() => {
            setInstalledVersion((v) => v + 1);
            setTab("installed");
          }}
        />
      )}
      {tab === "installed" && <InstalledView key={installedVersion} onSessionExpired={onSessionExpired} />}
      {tab === "sources" && <SourcesCard onSessionExpired={onSessionExpired} />}
    </div>
  );
}
