import { useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";

export function ModelsCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("models", onSessionExpired);
  const [alias, setAlias] = useState("");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [reasoningEfforts, setReasoningEfforts] = useState("");

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({ alias, provider, model, reasoning_efforts: reasoningEfforts });
    setAlias("");
    setProvider("");
    setModel("");
    setReasoningEfforts("");
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Models</CardTitle>
        <CardDescription>Aliases that map a short name onto a provider's model.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable headers={result?.table_headers} rows={result?.table_rows} empty="No models configured yet." />
        <form onSubmit={handleSubmit} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Input placeholder="alias" value={alias} onChange={(e) => setAlias(e.target.value)} required />
          <Input placeholder="provider" value={provider} onChange={(e) => setProvider(e.target.value)} required />
          <Input placeholder="model" value={model} onChange={(e) => setModel(e.target.value)} required />
          <Input
            placeholder="reasoning_efforts (comma-separated, optional)"
            value={reasoningEfforts}
            onChange={(e) => setReasoningEfforts(e.target.value)}
          />
          <Button type="submit" disabled={saving} className="sm:col-span-2">
            {saving ? "Saving..." : "Add model"}
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
