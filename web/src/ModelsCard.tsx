import { useState } from "react";
import { useConfigSection } from "./useConfigSection";

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
    <section className="rounded-lg bg-white p-6 shadow">
      <h2 className="mb-4 text-lg font-semibold text-slate-900">Models</h2>
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
        <p className="mb-4 text-sm text-slate-500">No models configured yet.</p>
      )}
      <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-3">
        <input placeholder="alias" value={alias} onChange={(e) => setAlias(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="provider" value={provider} onChange={(e) => setProvider(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input placeholder="model" value={model} onChange={(e) => setModel(e.target.value)} className="rounded border border-slate-300 px-3 py-2" required />
        <input
          placeholder="reasoning_efforts (comma-separated, optional)"
          value={reasoningEfforts}
          onChange={(e) => setReasoningEfforts(e.target.value)}
          className="rounded border border-slate-300 px-3 py-2"
        />
        <button type="submit" disabled={saving} className="col-span-2 rounded bg-slate-900 px-4 py-2 text-white disabled:opacity-50">
          {saving ? "Saving..." : "Add model"}
        </button>
      </form>
      {result?.detail && <p className="mt-3 text-xs text-slate-500">{result.detail}</p>}
      {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
    </section>
  );
}
