import { useCallback, useEffect, useState } from "react";
import { CommandResult, MCPServerInput, SessionExpiredError, listMCPServers, removeMCPServer, setMCPServer } from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";
import { Select } from "./components/ui/select";
import { Switch } from "./components/ui/switch";

export function McpCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [auth, setAuth] = useState<MCPServerInput["auth"]>("oauth");
  const [bearerTokenEnv, setBearerTokenEnv] = useState("");
  const [enabled, setEnabled] = useState(true);

  const load = useCallback(() => {
    listMCPServers()
      .then(setResult)
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to load");
      });
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await setMCPServer({ name, url, auth, bearer_token_env: bearerTokenEnv, enabled });
      setName("");
      setUrl("");
      setAuth("oauth");
      setBearerTokenEnv("");
      setEnabled(true);
      load();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Failed to save");
    } finally {
      setSaving(false);
    }
  }

  async function handleRemove(serverName: string) {
    setError(null);
    try {
      await removeMCPServer(serverName);
      load();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Failed to remove");
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>MCP servers</CardTitle>
        <CardDescription>External tool servers Eggy can call during a turn.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable
          headers={result?.table_headers}
          rows={result?.table_rows}
          empty="No MCP servers configured yet."
          renderRowAction={(row) => (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => handleRemove(row[0])}
              className="text-destructive hover:bg-destructive/10 hover:text-destructive"
            >
              Remove
            </Button>
          )}
        />
        <form onSubmit={handleSubmit} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} required />
          <Input placeholder="url (https://...)" value={url} onChange={(e) => setUrl(e.target.value)} required />
          <Select value={auth} onChange={(e) => setAuth(e.target.value as MCPServerInput["auth"])}>
            <option value="oauth">oauth</option>
            <option value="bearer-env">bearer-env</option>
            <option value="none">none</option>
          </Select>
          {auth === "bearer-env" && (
            <Input
              placeholder="bearer_token_env"
              value={bearerTokenEnv}
              onChange={(e) => setBearerTokenEnv(e.target.value)}
              required
            />
          )}
          <div className="rounded-md border border-border bg-muted/40 px-3 py-2.5 sm:col-span-2">
            <Switch checked={enabled} onCheckedChange={setEnabled} label="Enabled" />
          </div>
          <Button type="submit" disabled={saving} className="sm:col-span-2">
            {saving ? "Saving..." : "Add / update server"}
          </Button>
        </form>
        <p className="text-xs leading-relaxed text-muted-foreground">
          An oauth server still needs <code className="rounded bg-muted px-1 py-0.5 text-[0.9em]">/mcp login &lt;name&gt;</code> via
          Telegram/CLI after restart. Advanced settings (timeouts, tool filters) stay config.yaml-only.
        </p>
        {error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
