import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { ErrorBanner } from "../../components/ui/error-banner";
import { Input } from "../../components/ui/input";
import { Select } from "../../components/ui/select";
import { getSourcesConfig, handleFailure, saveSourcesConfig, SourceConfig } from "./marketplace";

// Sources are the catalogs the owner reads. Saving validates them on the server,
// which refuses the whole list if any row is wrong.
export function SourcesCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [rows, setRows] = useState<SourceConfig[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    getSourcesConfig()
      .then((result) => setRows(result.sources))
      .catch((err) => handleFailure(err, onSessionExpired, setError, "Could not read sources"));
  }, [onSessionExpired]);

  function update(index: number, patch: Partial<SourceConfig>) {
    setRows(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
    setSaved(false);
  }

  async function save() {
    setError(null);
    try {
      await saveSourcesConfig(rows);
      setSaved(true);
    } catch (err) {
      handleFailure(err, onSessionExpired, setError, "Could not save sources");
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <p className="text-xs leading-relaxed text-neutral-700">
        Public GitHub repositories only. Sources apply to the next check; turning the marketplace on or off takes effect after a restart.
      </p>
      {rows.map((row, index) => (
        <div key={index} className="grid grid-cols-1 gap-2 rounded-xl bg-neutral-100 p-3 sm:grid-cols-2">
          <Input aria-label="Source name" placeholder="name" value={row.name} onChange={(e) => update(index, { name: e.target.value })} />
          <Select aria-label="Source kind" value={row.kind} onChange={(e) => update(index, { kind: e.target.value })} className="bg-background">
            <option value="skills_repo">Skills repository</option>
            <option value="claude_marketplace">Claude marketplace</option>
          </Select>
          <Input aria-label="Repository" placeholder="owner/repo" value={row.repo} onChange={(e) => update(index, { repo: e.target.value })} />
          <Input aria-label="Path" placeholder="path (optional)" value={row.path ?? ""} onChange={(e) => update(index, { path: e.target.value })} />
          <Input aria-label="Ref" placeholder="branch or tag (optional)" value={row.ref ?? ""} onChange={(e) => update(index, { ref: e.target.value })} />
          <Button type="button" variant="ghost" size="sm" onClick={() => setRows(rows.filter((_, i) => i !== index))}>
            Remove source
          </Button>
        </div>
      ))}
      <div className="flex gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={() => setRows([...rows, { name: "", kind: "skills_repo", repo: "" }])}>
          Add source
        </Button>
        <Button type="button" onClick={save}>
          Save sources
        </Button>
      </div>
      {saved && <p className="text-xs text-neutral-700">Saved.</p>}
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </section>
  );
}
