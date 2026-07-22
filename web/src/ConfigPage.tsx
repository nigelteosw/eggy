import { ProvidersCard } from "./ProvidersCard";
import { ModelsCard } from "./ModelsCard";
import { CalendarCard } from "./CalendarCard";
import { McpCard } from "./McpCard";

export function ConfigPage({ onSessionExpired }: { onSessionExpired: () => void }) {
  return (
    <div className="min-h-screen bg-slate-100 p-8">
      <div className="mx-auto flex max-w-2xl flex-col gap-6">
        <h1 className="text-2xl font-semibold text-slate-900">Eggy config</h1>
        <ProvidersCard onSessionExpired={onSessionExpired} />
        <ModelsCard onSessionExpired={onSessionExpired} />
        <CalendarCard onSessionExpired={onSessionExpired} />
        <McpCard onSessionExpired={onSessionExpired} />
      </div>
    </div>
  );
}
