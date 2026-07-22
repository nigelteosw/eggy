import { useState } from "react";
import { useConfigSection } from "./useConfigSection";

export function ProvidersCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("providers", onSessionExpired);
  const [name, setName] = useState("");
  const [adapter, setAdapter] = useState("openai_compatible");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKeyEnv, setApiKeyEnv] = useState("");

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({ name, adapter, base_url: baseUrl, api_key_env: apiKeyEnv });
    setName("");
    setBaseUrl("");
    setApiKeyEnv("");
  }

  return (
    <section className="rounded-lg bg-white p-6 shadow">
      <h2 className="mb-4 text-lg font-semibold text-slate-900">Providers</h2>
      {result?.table_rows && result.table_rows.length > 0 ? (
        <table className="mb-4 w-full text-left text-sm">
          <thead>
            <tr>
              {result.table_headers?.map((header) => (
                <th key={header} className="border-b border-slate-200 pb-2 pr-4 font-medium text-slate-500">
                  {header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {result.table_rows.map((row, index) => (
              <tr key={index}>
                {row.map((cell, cellIndex) => (
                  <td key={cellIndex} className="border-b border-slate-100 py-2 pr-4 text-slate-700">
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="mb-4 text-sm text-slate-500">No providers configured yet.</p>
      )}
      <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-3">
        <input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="adapter" value={adapter} onChange={(e) => setAdapter(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="base_url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="api_key_env" value={apiKeyEnv} onChange={(e) => setApiKeyEnv(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <button type="submit" disabled={saving} className="col-span-2 rounded bg-slate-900 px-4 py-2 text-white disabled:opacity-50">
          {saving ? "Saving..." : "Add provider"}
        </button>
      </form>
      {result?.detail && <p className="mt-3 text-xs text-slate-500">{result.detail}</p>}
      {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
    </section>
  );
}
