import { useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Button } from "./components/ui/button";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";

const FIELD = "rounded-xl border-0 bg-background shadow-[inset_0_0_0_1px_hsl(var(--neutral-200))]";

export function ProvidersCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("providers", onSessionExpired);
  const [name, setName] = useState("");
  const [adapter, setAdapter] = useState("openai_compatible");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKeyEnv, setApiKeyEnv] = useState("");
  // On by default, matching the config field: a provider that can list its
  // models is the ordinary case, and opting out is the deliberate act.
  const [discoverModels, setDiscoverModels] = useState(true);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({
      name,
      adapter,
      base_url: baseUrl,
      api_key_env: apiKeyEnv,
      discover_models: discoverModels ? "true" : "false",
    });
    setName("");
    setBaseUrl("");
    setApiKeyEnv("");
    setDiscoverModels(true);
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="mx-0.5">
        <h3 className="text-[15px] font-semibold tracking-tight">Providers</h3>
        <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">Model endpoints Eggy can talk to.</p>
      </div>
      <div className="flex flex-col gap-3">
        <DataTable headers={result?.table_headers} rows={result?.table_rows} empty="No providers configured yet." />
        <details className="rounded-2xl bg-neutral-100 p-4">
          <summary className="cursor-pointer text-[12.5px] font-medium">Add provider</summary>
          <form onSubmit={handleSubmit} className="mt-3 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            <Input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} required className={FIELD} />
            <Input placeholder="adapter" value={adapter} onChange={(e) => setAdapter(e.target.value)} required className={FIELD} />
            <details className="sm:col-span-2">
              <summary className="cursor-pointer text-[12.5px] text-neutral-700">Advanced options</summary>
              <div className="mt-3 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
                <Input placeholder="base_url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} required className={FIELD} />
                <Input placeholder="api_key_env" value={apiKeyEnv} onChange={(e) => setApiKeyEnv(e.target.value)} required className={FIELD} />
                <Switch
                  className="sm:col-span-2"
                  checked={discoverModels}
                  onCheckedChange={setDiscoverModels}
                  label="Browse this provider's model catalog"
                />
              </div>
            </details>
            <Button type="submit" disabled={saving} className="sm:col-span-2">
              {saving ? "Saving..." : "Save provider"}
            </Button>
          </form>
        </details>
        {result?.detail && <p className="mx-0.5 text-[12.5px] text-neutral-700">{result.detail}</p>}
        {error && (
          <p className="rounded-2xl bg-eg-red-tint px-3.5 py-2.5 text-sm text-eg-red-ink" role="alert">
            {error}
          </p>
        )}
      </div>
    </section>
  );
}
