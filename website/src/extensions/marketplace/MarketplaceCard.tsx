import { useState } from "react";
import { useConfigSection } from "../../settings/useConfigSection";
import { CardHeader } from "../../components/ui/card-header";
import { ErrorBanner } from "../../components/ui/error-banner";
import { PRIMARY_BUTTON } from "../../components/ui/form";
import { Switch } from "../../components/ui/switch";

// The settings card for marketplace.enabled. Like finance, it applies on
// restart: savedEnabled is what config.yaml says and running is what this
// process started with, so a restart is needed exactly when the two differ.
export function MarketplaceCard({ onSessionExpired, running }: { onSessionExpired: () => void; running: boolean }) {
  const { result, error, saving, save } = useConfigSection("marketplace", onSessionExpired);
  const [draft, setDraft] = useState<boolean | null>(null);
  const fields = Object.fromEntries((result?.fields ?? []).map((field) => [field.label, field.value]));
  const savedEnabled = fields.enabled === "true";
  // Nothing to compare until the saved state has loaded.
  const needsRestart = result != null && savedEnabled !== running;

  async function handleSave() {
    if (await save({ enabled: String(draft ?? savedEnabled) })) setDraft(null);
  }

  return (
    <section className="flex flex-col gap-3">
      <CardHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span>Marketplace</span>
            <span role="status" aria-label="Marketplace status" className="rounded-full bg-neutral-100 px-2 py-0.5 text-[11px] font-medium text-neutral-700">
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
        className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4"
      >
        <Switch checked={draft ?? savedEnabled} onCheckedChange={setDraft} label="Enable the marketplace" />
        <div>
          <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
            {saving ? "Saving..." : "Save"}
          </button>
        </div>
      </form>
      {needsRestart && (
        <p className="rounded-2xl bg-neutral-100 px-3.5 py-2.5 text-sm text-neutral-700" role="status">
          Restart to apply: the marketplace is {running ? "on" : "off"} now and will be {savedEnabled ? "on" : "off"} after a restart.
        </p>
      )}
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </section>
  );
}
