import { useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";

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
    <Card>
      <CardHeader>
        <CardTitle>Providers</CardTitle>
        <CardDescription>Model endpoints Eggy can talk to.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable headers={result?.table_headers} rows={result?.table_rows} empty="No providers configured yet." />
        <form onSubmit={handleSubmit} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} required />
          <Input placeholder="adapter" value={adapter} onChange={(e) => setAdapter(e.target.value)} required />
          <Input placeholder="base_url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} required />
          <Input placeholder="api_key_env" value={apiKeyEnv} onChange={(e) => setApiKeyEnv(e.target.value)} required />
          <Switch
            className="sm:col-span-2"
            checked={discoverModels}
            onCheckedChange={setDiscoverModels}
            label="Let the Models card browse this provider's catalog"
          />
          <Button type="submit" disabled={saving} className="sm:col-span-2">
            {saving ? "Saving..." : "Add provider"}
          </Button>
        </form>
        {result?.detail && <p className="text-xs text-muted-foreground">{result.detail}</p>}
        {error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
