import { ProvidersCard } from "./ProvidersCard";
import { ModelsCard } from "./ModelsCard";
import { McpCard } from "./McpCard";
import { GoogleCard } from "./GoogleCard";
import { HeartbeatCard } from "./HeartbeatCard";
import { ToolsCard } from "./ToolsCard";
import { SchedulesCard } from "./SchedulesCard";
import { ApprovalsCard } from "./ApprovalsCard";

export function ConfigPage({ onSessionExpired }: { onSessionExpired: () => void }) {
  return (
    <div className="min-h-screen bg-background px-4 py-10 sm:px-8">
      <div className="mx-auto flex max-w-2xl flex-col gap-6">
        <header className="flex flex-col gap-1 pb-2">
          <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
          <p className="text-sm text-muted-foreground">Providers, models, and integrations for your Eggy instance.</p>
        </header>
        <ProvidersCard onSessionExpired={onSessionExpired} />
        <ModelsCard onSessionExpired={onSessionExpired} />
        <McpCard onSessionExpired={onSessionExpired} />
        <ToolsCard onSessionExpired={onSessionExpired} />
        <GoogleCard onSessionExpired={onSessionExpired} />
        <HeartbeatCard onSessionExpired={onSessionExpired} />
        <SchedulesCard onSessionExpired={onSessionExpired} />
        <ApprovalsCard onSessionExpired={onSessionExpired} />
      </div>
    </div>
  );
}
