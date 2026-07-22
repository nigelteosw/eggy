import { useCallback, useEffect, useState } from "react";
import { CommandResult, MCPServerInput, SessionExpiredError, listMCPServers, removeMCPServer, setMCPServer } from "./api";

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
    <section className="rounded-lg bg-white p-6 shadow">
      <h2 className="mb-4 text-lg font-semibold text-slate-900">MCP servers</h2>
      {result?.table_rows && result.table_rows.length > 0 ? (
        <table className="mb-4 w-full text-left text-sm">
          <thead>
            <tr>
              {result.table_headers?.map((header) => (
                <th key={header} className="border-b border-slate-200 pb-2 pr-4 font-medium text-slate-500">
                  {header}
                </th>
              ))}
              <th className="border-b border-slate-200 pb-2 font-medium text-slate-500" />
            </tr>
          </thead>
          <tbody>
            {result.table_rows.map((row) => (
              <tr key={row[0]}>
                {row.map((cell, cellIndex) => (
                  <td key={cellIndex} className="border-b border-slate-100 py-2 pr-4 text-slate-700">
                    {cell}
                  </td>
                ))}
                <td className="border-b border-slate-100 py-2">
                  <button type="button" onClick={() => handleRemove(row[0])} className="text-sm text-red-600 hover:underline">
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="mb-4 text-sm text-slate-500">No MCP servers configured yet.</p>
      )}
      <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-3">
        <input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input
          placeholder="url (https://...)"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          className="rounded border border-slate-300 px-3 py-2"
          required
        />
        <select
          value={auth}
          onChange={(e) => setAuth(e.target.value as MCPServerInput["auth"])}
          className="rounded border border-slate-300 px-3 py-2"
        >
          <option value="oauth">oauth</option>
          <option value="bearer-env">bearer-env</option>
          <option value="none">none</option>
        </select>
        {auth === "bearer-env" && (
          <input
            placeholder="bearer_token_env"
            value={bearerTokenEnv}
            onChange={(e) => setBearerTokenEnv(e.target.value)}
            className="rounded border border-slate-300 px-3 py-2"
            required
          />
        )}
        <label className="col-span-2 flex items-center gap-2 text-sm text-slate-700">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          Enabled
        </label>
        <button type="submit" disabled={saving} className="col-span-2 rounded bg-slate-900 px-4 py-2 text-white disabled:opacity-50">
          {saving ? "Saving..." : "Add / update server"}
        </button>
      </form>
      <p className="mt-3 text-xs text-slate-500">
        An oauth server still needs /mcp login &lt;name&gt; via Telegram/CLI after restart. Advanced settings (timeouts, tool
        filters) stay config.yaml-only.
      </p>
      {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
    </section>
  );
}
