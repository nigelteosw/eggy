import { useState } from "react";
import { useConfigSection } from "../../settings/useConfigSection";
import { CardHeader } from "../../components/ui/card-header";
import { ErrorBanner } from "../../components/ui/error-banner";
import { PRIMARY_BUTTON } from "../../components/ui/form";
import { Switch } from "../../components/ui/switch";

// The settings card for marketplace.enabled. Like finance, it applies on
// restart, which the restart bar offers once for the whole panel.
export function MarketplaceCard({ onSessionExpired, running }: { onSessionExpired: () => void; running: boolean }) {
  const { result, error, saving, save } = useConfigSection("marketplace", onSessionExpired);
  const [draft, setDraft] = useState<boolean | null>(null);
  const fields = Object.fromEntries((result?.fields ?? []).map((field) => [field.label, field.value]));
  const savedEnabled = fields.enabled === "true";

  async function handleSave() {
    if (await save({ enabled: String(draft ?? savedEnabled) })) setDraft(null);
  }

  return (
    <section className="flex flex-col gap-3">
      <CardHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span>Marketplace</span>
            <span role="status" aria-label="Marketplace status" className="rounded-full bg-neutral-100 px-2 py-0.5 text-caption font-medium text-neutral-700">
              {running ? "Running" : "Not running"}
            </span>
          </span>
        }
        description="Install skills and remote MCP servers from catalogs on GitHub, after reviewing exactly what they write. Sources are edited in Customize → Plugins once it is running. Installed skills are shared by every account."
      />
      <form
        onSubmit={(event) => {
          event.preventDefault();
          handleSave();
        }}
        className="flex flex-col gap-3 rounded-card bg-neutral-100 p-4"
      >
        <Switch checked={draft ?? savedEnabled} onCheckedChange={setDraft} label="Enable the marketplace" />
        <div>
          <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
            {saving ? "Saving..." : "Save"}
          </button>
        </div>
      </form>
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </section>
  );
}
