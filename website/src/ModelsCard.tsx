import { useEffect, useMemo, useRef, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { CommandResult, SessionExpiredError, discoverModels, removeModelAlias } from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";
import { Select } from "./components/ui/select";

// aliasFor suggests a short name from a model ID: "anthropic/claude-sonnet-5"
// becomes "claude-sonnet-5". It is only a starting point -- the field stays
// editable, because the alias is the owner's own vocabulary and Eggy should
// not be the one deciding what they call a model.
function aliasFor(modelId: string): string {
  const tail = modelId.split("/").pop() ?? modelId;
  return tail.toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "");
}

export function modelDraftForRow(row: string[]) {
  return { alias: row[0] ?? "", provider: row[1] ?? "", model: row[2] ?? "", reasoningEfforts: row[3] ?? "" };
}

export function ModelRowActions({
  alias,
  onEdit,
  onRemove,
  removing = false,
}: {
  alias: string;
  onEdit: () => void;
  onRemove: () => void;
  removing?: boolean;
}) {
  return (
    <div className="flex justify-end gap-1">
      <Button type="button" variant="ghost" size="sm" aria-label={`Edit ${alias}`} onClick={onEdit}>Edit</Button>
      <Button type="button" variant="ghost" size="sm" aria-label={`Remove ${alias}`} onClick={onRemove} disabled={removing} className="text-destructive hover:bg-destructive/10 hover:text-destructive">
        {removing ? "Removing..." : "Remove"}
      </Button>
    </div>
  );
}

export function ModelsCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save, load } = useConfigSection("models", onSessionExpired);
  const [alias, setAlias] = useState("");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [reasoningEfforts, setReasoningEfforts] = useState("");
  const [editingAlias, setEditingAlias] = useState<string | null>(null);
  const [removingAlias, setRemovingAlias] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [restartRequired, setRestartRequired] = useState(false);
  const form = useRef<HTMLDetailsElement | null>(null);

  // The browse state is deliberately separate from the form's: browsing is a
  // way to fill the form in, so picking a row must not be the same event as
  // submitting one.
  const [catalog, setCatalog] = useState<CommandResult | null>(null);
  const [browsing, setBrowsing] = useState(false);
  const [browseError, setBrowseError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  // Providers that opted in to discovery ride along on the section itself.
  const browsable = useMemo(() => result?.lines ?? [], [result]);
  const [browseProvider, setBrowseProvider] = useState("");
  useEffect(() => {
    if (!browseProvider && browsable.length > 0) setBrowseProvider(browsable[0]);
  }, [browsable, browseProvider]);

  const matches = useMemo(() => {
    const rows = catalog?.table_rows ?? [];
    const needle = filter.trim().toLowerCase();
    if (!needle) return rows;
    return rows.filter((row) => row.some((cell) => cell.toLowerCase().includes(needle)));
  }, [catalog, filter]);

  async function handleBrowse() {
    if (!browseProvider) return;
    setBrowsing(true);
    setBrowseError(null);
    try {
      setCatalog(await discoverModels(browseProvider));
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setCatalog(null);
      setBrowseError(err instanceof Error ? err.message : "Could not reach the provider");
    } finally {
      setBrowsing(false);
    }
  }

  function choose(modelId: string) {
    setProvider(browseProvider);
    setModel(modelId);
    if (!alias) setAlias(aliasFor(modelId));
  }

  function resetForm() {
    setAlias("");
    setProvider("");
    setModel("");
    setReasoningEfforts("");
    setEditingAlias(null);
  }

  function startEdit(row: string[]) {
    const draft = modelDraftForRow(row);
    setAlias(draft.alias);
    setProvider(draft.provider);
    setModel(draft.model);
    setReasoningEfforts(draft.reasoningEfforts);
    setEditingAlias(draft.alias);
    setActionError(null);
    if (form.current) form.current.open = true;
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    const saved = await save({ alias, provider, model, reasoning_efforts: reasoningEfforts });
    if (!saved) return;
    resetForm();
    setRestartRequired(true);
  }

  async function handleRemove(aliasToRemove: string) {
    if (!window.confirm(`Remove ${aliasToRemove}? It remains available until Eggy restarts.`)) return;
    setRemovingAlias(aliasToRemove);
    setActionError(null);
    try {
      await removeModelAlias(aliasToRemove);
      if (editingAlias === aliasToRemove) resetForm();
      setRestartRequired(true);
      load();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setActionError(err instanceof Error ? err.message : "Could not remove model");
    } finally {
      setRemovingAlias(null);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Models</CardTitle>
        <CardDescription>
          Aliases that map a short name onto a provider's model. Only aliases listed here can be selected — browsing a
          provider shows what it offers, it does not enable anything.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable
          headers={result?.table_headers}
          rows={result?.table_rows}
          empty="No models configured yet."
          renderRowAction={(row) => (
            <ModelRowActions
              alias={row[0]}
              onEdit={() => startEdit(row)}
              onRemove={() => handleRemove(row[0])}
              removing={removingAlias === row[0]}
            />
          )}
        />

        {browsable.length > 0 && (
          <div className="flex flex-col gap-3 rounded-md border border-border p-3">
            <p className="text-sm font-medium">Browse a provider</p>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_auto]">
              <Select
                value={browseProvider}
                onChange={(e) => {
                  setBrowseProvider(e.target.value);
                  setCatalog(null);
                }}
                aria-label="Provider to browse"
              >
                {browsable.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </Select>
              <Button type="button" variant="secondary" onClick={handleBrowse} disabled={browsing}>
                {browsing ? "Loading..." : "Browse models"}
              </Button>
            </div>

            {catalog && (
              <>
                <Input
                  placeholder="Filter models"
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  aria-label="Filter models"
                />
                {/* Capped height rather than paging: OpenRouter returns several
                    hundred entries, and a scrolling list keeps the filter box
                    and the form it fills both on screen. */}
                <ul className="max-h-64 overflow-y-auto rounded-md border border-border">
                  {matches.map((row) => (
                    <li key={row[0]}>
                      <button
                        type="button"
                        onClick={() => choose(row[0])}
                        className="flex w-full flex-col items-start gap-0.5 border-b border-border px-3 py-2 text-left last:border-b-0 hover:bg-muted"
                      >
                        <span className="font-mono text-xs">{row[0]}</span>
                        {(row[1] || row[2]) && (
                          <span className="text-xs text-muted-foreground">
                            {row[1]}
                            {row[1] && row[2] ? " · " : ""}
                            {row[2] ? `${Number(row[2]).toLocaleString()} ctx` : ""}
                          </span>
                        )}
                      </button>
                    </li>
                  ))}
                  {matches.length === 0 && (
                    <li className="px-3 py-2 text-sm text-muted-foreground">No model matches that filter.</li>
                  )}
                </ul>
                <p className="text-xs text-muted-foreground">
                  {matches.length} shown. Pick one to fill the form below, then add it as an alias.
                </p>
              </>
            )}
            {browseError && (
              <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
                {browseError}
              </p>
            )}
          </div>
        )}

        <details ref={form} className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">{editingAlias ? `Edit ${editingAlias}` : "Add model alias"}</summary>
          <form onSubmit={handleSubmit} className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Input placeholder="alias" value={alias} onChange={(e) => setAlias(e.target.value)} readOnly={editingAlias !== null} required />
            <Input placeholder="provider" value={provider} onChange={(e) => setProvider(e.target.value)} required />
            <Input placeholder="model" value={model} onChange={(e) => setModel(e.target.value)} required className="sm:col-span-2 font-mono" />
            <details className="sm:col-span-2">
              <summary className="cursor-pointer text-sm text-muted-foreground">Advanced options</summary>
              <Input
                placeholder="reasoning_efforts (comma-separated, optional)"
                value={reasoningEfforts}
                onChange={(e) => setReasoningEfforts(e.target.value)}
                className="mt-3"
              />
            </details>
            <div className="flex flex-wrap gap-2 sm:col-span-2">
              <Button type="submit" disabled={saving}>{saving ? "Saving..." : editingAlias ? "Update model" : "Save model"}</Button>
              {editingAlias && <Button type="button" variant="ghost" onClick={resetForm}>Cancel</Button>}
            </div>
          </form>
        </details>
        {restartRequired && (
          <p className="rounded-md border border-primary/30 bg-primary/10 px-3 py-2 text-sm" role="status">
            Restart Eggy before using these model changes in chat.
          </p>
        )}
        {result?.detail && <p className="text-xs text-muted-foreground">{result.detail}</p>}
        {(actionError || error) && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {actionError || error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
