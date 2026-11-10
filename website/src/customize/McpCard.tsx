import { Field } from "../components/ui/field";
import { SaveBar } from "../components/ui/save-bar";
import { useCallback, useEffect, useState } from "react";
import { CommandResult, MCPServerInput, SessionExpiredError, listMCPServers, removeMCPServer, setMCPServer } from "../lib/api";
import { errorMessage } from "../lib/utils";
import { Button } from "../components/ui/button";
import { DataTable } from "../components/ui/data-table";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Switch } from "../components/ui/switch";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { FIELD } from "../components/ui/form";
import { McpEnableDialog } from "./McpEnableDialog";

// Column positions in the rows /api/config/mcp returns. Named here so the row
// actions below read as intent rather than as indexes into an anonymous array.
const NAME = 0;
const AUTH = 3;
const ENABLED = 4;
const TRANSPORT = 1;

export function McpCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  // The server whose enable review is open. Enabling is a trust decision, so a
  // disabled server is never switched on from the row alone.
  const [reviewing, setReviewing] = useState<string | null>(null);
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
        setError(errorMessage(err, "Failed to load"));
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
      setError(errorMessage(err, "Failed to save"));
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
      setError(errorMessage(err, "Failed to remove"));
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <CardHeader title="MCP servers" description="External tool servers Eggy can call during a turn." />
      <div className="flex flex-col gap-3">
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
              {row[TRANSPORT] !== "stdio" && (
                // Enabled servers can be reviewed again, so a tool added since
                // the last review can be allowed on purpose.
                <Button type="button" variant="ghost" size="sm" onClick={() => setReviewing(row[NAME])}>
                  {row[ENABLED] === "false" ? "Review and enable" : "Review tools"}
                </Button>
              )}
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => handleRemove(row[NAME])}
                className="text-tomato hover:bg-tomato-soft hover:text-tomato"
              >
                Remove
              </Button>
            </div>
          )}
        />
        {reviewing && (
          <McpEnableDialog
            server={reviewing}
            onClose={() => setReviewing(null)}
            // The dialog stays open on its saved notice; Close dismisses it.
            onEnabled={load}
            onSessionExpired={onSessionExpired}
          />
        )}
        <details className="rounded-card bg-neutral-100 p-4">
          <summary className="cursor-pointer text-small font-medium">Add MCP server</summary>
          <form onSubmit={handleSubmit} className="mt-3 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            <Field label="Name" configKey="name">
              <Input placeholder="github" value={name} onChange={(e) => setName(e.target.value)} required className={FIELD} />
            </Field>
            <Field label="Server address" configKey="url">
              <Input placeholder="https://example.com/mcp" value={url} onChange={(e) => setUrl(e.target.value)} required className={FIELD} />
            </Field>
            <details className="sm:col-span-2">
              <summary className="cursor-pointer text-small text-neutral-700">Advanced options</summary>
              <div className="mt-3 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
                <Field label="Transport" configKey="transport">
                  <Select value={transport} onChange={(e) => setTransport(e.target.value)} className="bg-background">
                    <option value="streamable-http">Streamable HTTP</option>
                  </Select>
                </Field>
                <Field label="Sign-in" configKey="auth">
                  <Select value={auth} onChange={(e) => setAuth(e.target.value as MCPServerInput["auth"])} className="bg-background">
                    <option value="oauth">OAuth — authorize in the browser</option>
                    <option value="bearer-env">A token from an environment variable</option>
                    <option value="none">None</option>
                  </Select>
                </Field>
                {auth === "bearer-env" && (
                  <Field label="Token, read from the environment variable" configKey="bearer_token_env" className="sm:col-span-2">
                    <Input placeholder="GITHUB_MCP_TOKEN" value={bearerTokenEnv} onChange={(e) => setBearerTokenEnv(e.target.value)} required className={FIELD} />
                  </Field>
                )}
                {auth === "oauth" && (
                  <>
                    <Field label="OAuth client ID (optional)" configKey="oauth_client_id">
                      <Input value={oauthClientId} onChange={(e) => setOauthClientId(e.target.value)} className={FIELD} />
                    </Field>
                    <Field label="Client secret, read from the environment variable (optional)" configKey="oauth_client_secret_env">
                      <Input value={oauthClientSecretEnv} onChange={(e) => setOauthClientSecretEnv(e.target.value)} className={FIELD} />
                    </Field>
                    <p className="text-caption leading-relaxed text-neutral-700 sm:col-span-2">
                      Leave both empty for dynamic registration. Otherwise use callback{" "}
                      <code className="rounded-mark bg-background px-1 py-0.5 text-[0.9em]">
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
            <SaveBar label="Add server" />
          </form>
        </details>
        <p className="mx-0.5 text-caption leading-relaxed text-neutral-700">
          An OAuth server needs <strong>Authorize</strong> above once it has connected. Scopes, timeouts, and tool
          filters stay config.yaml-only.
        </p>
        {error && (
          <ErrorBanner>
            {error}
          </ErrorBanner>
        )}
      </div>
    </section>
  );
}
