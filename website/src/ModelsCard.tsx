import { useEffect, useMemo, useRef, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { CommandResult, SessionExpiredError, discoverModels, removeModelAlias } from "./api";
import { cn, errorMessage } from "./lib/utils";
import { Button } from "./components/ui/button";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";
import { Select } from "./components/ui/select";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD } from "./components/ui/form";

// aliasFor suggests a short name from a model ID: "anthropic/claude-sonnet-5"
// becomes "claude-sonnet-5". It is only a starting point -- the field stays
// editable, because the alias is the owner's own vocabulary and Eggy should
// not be the one deciding what they call a model.
function aliasFor(modelId: string): string {
  const tail = modelId.split("/").pop() ?? modelId;
  return tail.toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "");
}

// Routing is the OpenRouter provider-routing preference on an alias. It is
// spelled here the way the form holds it -- comma-separated slugs and
// strings -- and only becomes a structured block once the server decodes the
// openrouter_* fields.
export type Routing = { order: string; only: string; ignore: string; allowFallbacks: string; sort: string };

export const EMPTY_ROUTING: Routing = { order: "", only: "", ignore: "", allowFallbacks: "", sort: "" };

// routingFromCell parses the Routing column, which the server renders as
// space-separated key:value pairs ("order:anthropic,bedrock sort:price").
// Provider slugs contain no spaces, so the split is unambiguous.
export function routingFromCell(cell: string): Routing {
  const routing = { ...EMPTY_ROUTING };
  for (const pair of cell.split(/\s+/)) {
    const colon = pair.indexOf(":");
    if (colon < 0) continue;
    const key = pair.slice(0, colon);
    const value = pair.slice(colon + 1);
    if (key === "order" || key === "only" || key === "ignore" || key === "sort") routing[key] = value;
    else if (key === "allow_fallbacks") routing.allowFallbacks = value;
  }
  return routing;
}

// openRouterProvidersOf reads the provider names the server marked as
// OpenRouter off the models section.
export function openRouterProvidersOf(result: CommandResult | null): string[] {
  const value = result?.fields?.find((field) => field.label === "openrouter_providers")?.value ?? "";
  return value.split(",").map((name) => name.trim()).filter(Boolean);
}

// catalogEffortsFor reads what a provider's catalog says about one model's
// reasoning efforts: the comma-separated list, "" for a model the catalog
// lists without efforts, and null for a model the catalog does not list at
// all (so nothing is known and the owner may still need to type them).
export function catalogEffortsFor(catalog: CommandResult | null, model: string): string | null {
  const wanted = model.trim().replace(/^~/, "");
  if (!wanted) return null;
  const row = catalog?.table_rows?.find((candidate) => candidate[0] === wanted);
  return row ? row[3] ?? "" : null;
}

export function modelDraftForRow(row: string[]) {
  return {
    alias: row[0] ?? "",
    provider: row[1] ?? "",
    model: row[2] ?? "",
    reasoningEfforts: row[3] ?? "",
    routing: routingFromCell(row[4] ?? ""),
  };
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
      <Button type="button" variant="ghost" size="sm" aria-label={`Remove ${alias}`} onClick={onRemove} disabled={removing} className="text-eg-red-ink hover:bg-eg-red-tint hover:text-eg-red-ink">
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
  // catalogEfforts is what the provider itself reports for the model in the
  // form; null when the provider was not asked or does not list the model.
  // overrideEfforts opens the free-text field on top of a catalog answer.
  const [catalogEfforts, setCatalogEfforts] = useState<string | null>(null);
  const [overrideEfforts, setOverrideEfforts] = useState(false);
  const [routing, setRouting] = useState<Routing>(EMPTY_ROUTING);
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
  // One catalog per provider per page: browsing and the model-field lookup
  // share it, so typing a model after browsing costs no second fetch.
  const catalogs = useRef(new Map<string, CommandResult>());

  // Providers that opted in to discovery ride along on the section itself,
  // as do the ones that are OpenRouter, which is what gates the routing
  // fields: they mean nothing anywhere else and the server refuses them.
  const browsable = useMemo(() => result?.lines ?? [], [result]);
  const openRouterProviders = useMemo(() => openRouterProvidersOf(result), [result]);
  const routable = openRouterProviders.includes(provider.trim());
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

  async function catalogFor(name: string): Promise<CommandResult> {
    const cached = catalogs.current.get(name);
    if (cached) return cached;
    const fetched = await discoverModels(name);
    catalogs.current.set(name, fetched);
    return fetched;
  }

  async function handleBrowse() {
    if (!browseProvider) return;
    setBrowsing(true);
    setBrowseError(null);
    try {
      setCatalog(await catalogFor(browseProvider));
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setCatalog(null);
      setBrowseError(errorMessage(err, "Could not reach the provider"));
    } finally {
      setBrowsing(false);
    }
  }

  function choose(row: string[]) {
    setProvider(browseProvider);
    setModel(row[0]);
    if (!alias) setAlias(aliasFor(row[0]));
    applyCatalogEfforts(row[3] ?? "");
  }

  // applyCatalogEfforts takes the provider's answer as the alias's efforts.
  // The provider knows its own models better than a typed guess, so this
  // does overwrite, unless the owner has explicitly opened the override.
  function applyCatalogEfforts(efforts: string) {
    setCatalogEfforts(efforts);
    if (!overrideEfforts) setReasoningEfforts(efforts);
  }

  // lookupEfforts asks the provider about the model once the owner is done
  // typing it. Only providers that opted in to discovery are asked; a
  // provider that cannot list its models leaves the field to the owner.
  async function lookupEfforts() {
    const name = provider.trim();
    if (!browsable.includes(name) || !model.trim()) return;
    try {
      const efforts = catalogEffortsFor(await catalogFor(name), model);
      if (efforts === null) setCatalogEfforts(null);
      else applyCatalogEfforts(efforts);
    } catch (err) {
      if (err instanceof SessionExpiredError) onSessionExpired();
      // Any other failure just leaves the field editable, as it was.
    }
  }

  function resetForm() {
    setAlias("");
    setProvider("");
    setModel("");
    setReasoningEfforts("");
    setCatalogEfforts(null);
    setOverrideEfforts(false);
    setRouting(EMPTY_ROUTING);
    setEditingAlias(null);
  }

  function startEdit(row: string[]) {
    const draft = modelDraftForRow(row);
    setAlias(draft.alias);
    setProvider(draft.provider);
    setModel(draft.model);
    setReasoningEfforts(draft.reasoningEfforts);
    setCatalogEfforts(null);
    setOverrideEfforts(false);
    setRouting(draft.routing);
    setEditingAlias(draft.alias);
    setActionError(null);
    if (form.current) form.current.open = true;
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    const saved = await save({
      alias,
      provider,
      model,
      reasoning_efforts: reasoningEfforts,
      // Routing is sent only for an OpenRouter provider, so moving an alias
      // to another provider drops its old routing instead of failing on it.
      ...(routable
        ? {
            openrouter_order: routing.order,
            openrouter_only: routing.only,
            openrouter_ignore: routing.ignore,
            openrouter_allow_fallbacks: routing.allowFallbacks,
            openrouter_sort: routing.sort,
          }
        : {}),
    });
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
      setActionError(errorMessage(err, "Could not remove model"));
    } finally {
      setRemovingAlias(null);
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <CardHeader
        title="Models"
        description={
          <>
            Aliases that map a short name onto a provider's model. Only aliases listed here can be selected — browsing a
            provider shows what it offers, it does not enable anything.
          </>
        }
      />
      <div className="flex flex-col gap-3">
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
          <div className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4">
            <p className="text-[12.5px] font-medium">Browse a provider</p>
            <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-[1fr_auto]">
              <Select
                value={browseProvider}
                onChange={(e) => {
                  setBrowseProvider(e.target.value);
                  setCatalog(null);
                }}
                aria-label="Provider to browse"
                className="bg-background"
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
                  className={FIELD}
                />
                {/* Capped height rather than paging: OpenRouter returns several
                    hundred entries, and a scrolling list keeps the filter box
                    and the form it fills both on screen. */}
                <ul className="scrollbar-slim max-h-64 overflow-y-auto rounded-xl bg-background">
                  {matches.map((row) => (
                    <li key={row[0]} className="shadow-[inset_0_1px_0_hsl(var(--neutral-200))] first:shadow-none">
                      <button
                        type="button"
                        onClick={() => choose(row)}
                        className="flex w-full flex-col items-start gap-0.5 px-3 py-2 text-left hover:bg-neutral-100"
                      >
                        <span className="font-mono text-xs [overflow-wrap:anywhere]">{row[0]}</span>
                        {(row[1] || row[2] || row[3]) && (
                          <span className="text-xs text-neutral-700">
                            {[row[1], row[2] ? `${Number(row[2]).toLocaleString()} ctx` : "", row[3] ? `efforts: ${row[3]}` : ""]
                              .filter(Boolean)
                              .join(" · ")}
                          </span>
                        )}
                      </button>
                    </li>
                  ))}
                  {matches.length === 0 && (
                    <li className="px-3 py-2 text-sm text-neutral-700">No model matches that filter.</li>
                  )}
                </ul>
                <p className="text-xs text-neutral-700">
                  {matches.length} shown. Pick one to fill the form below, then add it as an alias.
                </p>
              </>
            )}
            {browseError && (
              <ErrorBanner>
                {browseError}
              </ErrorBanner>
            )}
          </div>
        )}

        <details ref={form} className="rounded-2xl bg-neutral-100 p-4">
          <summary className="cursor-pointer text-[12.5px] font-medium">{editingAlias ? `Edit ${editingAlias}` : "Add model alias"}</summary>
          <form onSubmit={handleSubmit} className="mt-3 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            <Input placeholder="alias" value={alias} onChange={(e) => setAlias(e.target.value)} readOnly={editingAlias !== null} required className={FIELD} />
            <Input
              placeholder="provider"
              value={provider}
              onChange={(e) => {
                setProvider(e.target.value);
                setCatalogEfforts(null);
              }}
              onBlur={lookupEfforts}
              required
              className={FIELD}
            />
            <Input
              placeholder="model"
              value={model}
              onChange={(e) => {
                setModel(e.target.value);
                setCatalogEfforts(null);
              }}
              onBlur={lookupEfforts}
              required
              className={cn(FIELD, "sm:col-span-2 font-mono")}
            />
            {catalogEfforts !== null && (
              <p className="text-xs text-neutral-700 sm:col-span-2" role="status">
                {catalogEfforts ? `Reasoning efforts: ${catalogEfforts}` : "No reasoning efforts"} (from {provider.trim()})
              </p>
            )}
            <details className="sm:col-span-2">
              <summary className="cursor-pointer text-[12.5px] text-neutral-700">Advanced options</summary>
              {/* The efforts field is typed only when the provider gave no
                  answer, or the owner insists: a catalog answer is the
                  provider's own and rarely worth second-guessing. */}
              {catalogEfforts !== null && !overrideEfforts ? (
                <Button type="button" variant="ghost" size="sm" className="mt-3" onClick={() => setOverrideEfforts(true)}>
                  Override reasoning efforts
                </Button>
              ) : (
                <Input
                  placeholder="reasoning_efforts (comma-separated, optional)"
                  aria-label="Reasoning efforts"
                  value={reasoningEfforts}
                  onChange={(e) => setReasoningEfforts(e.target.value)}
                  className={cn(FIELD, "mt-3")}
                />
              )}
              {routable && (
                <>
                  <p className="mt-3 text-[12.5px] font-medium">OpenRouter routing</p>
                  <p className="mt-1 text-xs text-neutral-700">
                    Which upstream vendors may serve this alias. Slugs are OpenRouter's (anthropic, amazon-bedrock, deepinfra…).
                  </p>
                  <div className="mt-2 grid grid-cols-1 gap-2.5 sm:grid-cols-2">
                    <Input placeholder="order (try these first, comma-separated)" aria-label="OpenRouter order" value={routing.order} onChange={(e) => setRouting({ ...routing, order: e.target.value })} className={FIELD} />
                    <Input placeholder="only (allow just these)" aria-label="OpenRouter only" value={routing.only} onChange={(e) => setRouting({ ...routing, only: e.target.value })} className={FIELD} />
                    <Input placeholder="ignore (never these)" aria-label="OpenRouter ignore" value={routing.ignore} onChange={(e) => setRouting({ ...routing, ignore: e.target.value })} className={FIELD} />
                    <Select aria-label="OpenRouter sort" value={routing.sort} onChange={(e) => setRouting({ ...routing, sort: e.target.value })} className="bg-background">
                      <option value="">sort: default</option>
                      <option value="price">sort: price</option>
                      <option value="throughput">sort: throughput</option>
                      <option value="latency">sort: latency</option>
                    </Select>
                    <Select aria-label="OpenRouter fallbacks" value={routing.allowFallbacks} onChange={(e) => setRouting({ ...routing, allowFallbacks: e.target.value })} className="bg-background">
                      <option value="">fallbacks: default (allowed)</option>
                      <option value="true">fallbacks: allowed</option>
                      <option value="false">fallbacks: never</option>
                    </Select>
                  </div>
                </>
              )}
            </details>
            <div className="flex flex-wrap gap-2 sm:col-span-2">
              <Button type="submit" disabled={saving}>{saving ? "Saving..." : editingAlias ? "Update model" : "Save model"}</Button>
              {editingAlias && <Button type="button" variant="ghost" onClick={resetForm}>Cancel</Button>}
            </div>
          </form>
        </details>
        {restartRequired && (
          <p className="rounded-2xl bg-accent-100 px-3.5 py-2.5 text-sm text-accent-900" role="status">
            Restart Eggy before using these model changes in chat.
          </p>
        )}
        {result?.detail && <p className="mx-0.5 text-[12.5px] text-neutral-700">{result.detail}</p>}
        {(actionError || error) && (
          <ErrorBanner>
            {actionError || error}
          </ErrorBanner>
        )}
      </div>
    </section>
  );
}
