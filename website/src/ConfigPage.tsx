import { useState } from "react";
import { ProvidersCard } from "./ProvidersCard";
import { ModelsCard } from "./ModelsCard";
import { CalendarCard } from "./CalendarCard";
import { McpCard } from "./McpCard";
import { FilesPage } from "./FilesPage";

// Two views over the same instance: the guided forms below, and FilesPage's
// raw view of the home directory for everything the forms don't cover.
type Tab = "settings" | "files";

export function ConfigPage({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [tab, setTab] = useState<Tab>("settings");

  const tabs = (
    <div className="flex w-fit gap-1 rounded-md border border-border bg-card p-1" role="tablist">
      {(["settings", "files"] as Tab[]).map((name) => (
        <button
          key={name}
          type="button"
          role="tab"
          aria-selected={tab === name}
          onClick={() => setTab(name)}
          className={`rounded px-3 py-1 text-sm capitalize transition-colors ${
            tab === name ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground"
          }`}
        >
          {name}
        </button>
      ))}
    </div>
  );

  if (tab === "files") {
    return (
      <div className="min-h-screen bg-background px-4 py-10 sm:px-8">
        <div className="mx-auto max-w-5xl">{tabs}</div>
        <FilesPage onSessionExpired={onSessionExpired} />
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background px-4 py-10 sm:px-8">
      <div className="mx-auto flex max-w-2xl flex-col gap-6">
        <header className="flex flex-col gap-3 pb-2">
          {tabs}
          <div className="flex flex-col gap-1">
            <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
            <p className="text-sm text-muted-foreground">Providers, models, and integrations for your Eggy instance.</p>
          </div>
        </header>
        <ProvidersCard onSessionExpired={onSessionExpired} />
        <ModelsCard onSessionExpired={onSessionExpired} />
        <CalendarCard onSessionExpired={onSessionExpired} />
        <McpCard onSessionExpired={onSessionExpired} />
      </div>
    </div>
  );
}
