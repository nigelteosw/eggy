import { useCallback, useEffect, useState } from "react";
import { CommandResult, MCPServerInput, SessionExpiredError, listMCPServers, removeMCPServer, setMCPServer } from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";
import { Select } from "./components/ui/select";
import { Switch } from "./components/ui/switch";

// Column positions in the rows /api/config/mcp returns. Named here so the row
// actions below read as intent rather than as indexes into an anonymous array.
const NAME = 0;
const AUTH = 3;

export function McpCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [transport, setTransport] = useState("streamable-http");
  const [auth, setAuth] = useState<MCPServerInput["auth"]>("oauth");
  const [bearerTokenEnv, setBearerTokenEnv] = useState("");
  const [oauthClientId, setOauthClientId] = useState("");
  const [oauthClientSecretEnv, setOauthClientSecretEnv] = useState("");
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
      await setMCPServer({
        name,
        url,
        transport,
        auth,
        bearer_token_env: bearerTokenEnv,
        oauth_client_id: oauthClientId,
        oauth_client_secret_env: oauthClientSecretEnv,
        enabled: String(enabled),
      });
      setName("");
      setUrl("");
      setTransport("streamable-http");
      setAuth("oauth");
      setBearerTokenEnv("");
      setOauthClientId("");
      setOauthClientSecretEnv("");
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
            <div className="flex items-center justify-end gap-1">
              {/*
                Authorizing is a full-page navigation rather than a fetch: the
                route redirects to the provider's consent screen, which cannot
                be followed from XHR.
              */}
              {row[AUTH] === "oauth" && (
                <Button asChild type="button" variant="ghost" size="sm">
                  <a href={`/auth/mcp/${encodeURIComponent(row[NAME])}`}>Authorize</a>
                </Button>
              )}
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => handleRemove(row[NAME])}
                className="text-destructive hover:bg-destructive/10 hover:text-destructive"
              >
                Remove
              </Button>
            </div>
          )}
        />
        <details className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">Add MCP server</summary>
          <form onSubmit={handleSubmit} className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} required />
            <Input placeholder="url (https://...)" value={url} onChange={(e) => setUrl(e.target.value)} required />
            <details className="sm:col-span-2">
              <summary className="cursor-pointer text-sm text-muted-foreground">Advanced options</summary>
              <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Select value={transport} onChange={(e) => setTransport(e.target.value)} aria-label="Transport">
                  <option value="streamable-http">streamable-http</option>
                </Select>
                <Select value={auth} onChange={(e) => setAuth(e.target.value as MCPServerInput["auth"])} aria-label="Authentication">
                  <option value="oauth">oauth</option>
                  <option value="bearer-env">bearer-env</option>
                  <option value="none">none</option>
                </Select>
                {auth === "bearer-env" && <Input placeholder="bearer_token_env" value={bearerTokenEnv} onChange={(e) => setBearerTokenEnv(e.target.value)} required className="sm:col-span-2" />}
                {auth === "oauth" && (
                  <>
                    <Input placeholder="oauth_client_id (optional)" value={oauthClientId} onChange={(e) => setOauthClientId(e.target.value)} />
                    <Input placeholder="oauth_client_secret_env (optional)" value={oauthClientSecretEnv} onChange={(e) => setOauthClientSecretEnv(e.target.value)} />
                    <p className="text-xs leading-relaxed text-muted-foreground sm:col-span-2">
                      Leave both empty for dynamic registration. Otherwise use callback{" "}
                      <code className="rounded bg-muted px-1 py-0.5 text-[0.9em]">
                        {typeof window === "undefined" ? "" : window.location.origin}/auth/mcp/{name || "<name>"}/callback
                      </code>
                      , then name the environment variable holding the secret.
                    </p>
                  </>
                )}
                <Switch className="sm:col-span-2" checked={enabled} onCheckedChange={setEnabled} label="Enabled" />
              </div>
            </details>
            <Button type="submit" disabled={saving} className="sm:col-span-2">
              {saving ? "Saving..." : "Save server"}
            </Button>
          </form>
        </details>
        <p className="text-xs leading-relaxed text-muted-foreground">
          Saved servers connect after a restart. OAuth servers then need <strong>Authorize</strong> above. Scopes,
          timeouts, and tool filters stay config.yaml-only.
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
