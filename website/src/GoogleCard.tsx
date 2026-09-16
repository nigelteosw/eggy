import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import type { CommandResult } from "./api";
import { cn } from "./lib/utils";
import { Button } from "./components/ui/button";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";

const FIELD = "rounded-xl border-0 bg-background shadow-[inset_0_0_0_1px_hsl(var(--neutral-200))]";

// The products the adapter knows. A product left unchecked has no tool at all,
// so this list is the whole of what Google can do here.
const PRODUCTS = ["calendar", "gmail", "drive", "docs", "sheets", "contacts"] as const;

// Column positions in the single row /api/config/google returns. One row,
// because there is one grant covering every product.
const STATE = 0;
const CLIENT_ID = 1;
const SECRET_ENV = 2;
const PRODUCT_LIST = 3;
const CONNECTED_AS = 4;
const EXPECTED_EMAIL = 5;

// GoogleIdentity shows whose account the one shared grant is, beside whose
// it should be. The grant is every Eggy user's, and the line says so: a
// mailbox connected here is not a private one.
export function GoogleIdentity({ connected, expected }: { connected: string; expected: string }) {
  if (!connected || connected === "not connected") {
    return (
      <p className="mx-0.5 text-sm text-neutral-700">
        Not connected.{expected ? ` Connect as ${expected} with /google login in chat.` : ""}
      </p>
    );
  }
  const mismatch = expected !== "" && connected !== expected && connected !== "unverified";
  return (
    <div className={`rounded-2xl px-[18px] py-3.5 text-sm ${mismatch ? "bg-eg-red-tint" : "bg-accent-100"}`}>
      <p className={`flex flex-wrap items-center gap-2.5 ${mismatch ? "text-eg-red-ink" : "text-accent-900"}`}>
        Connected as <strong>{connected === "unverified" ? "an unverified account" : connected}</strong>
        <span className="rounded-md bg-background px-2 py-0.5 text-xs text-neutral-700">Shared with all Eggy users</span>
      </p>
      {mismatch && (
        <p className="mt-1.5 text-eg-red-ink" role="alert">
          This is not Eggy&apos;s account: expected {expected}. Disconnect and reconnect signed in as {expected}.
        </p>
      )}
      {connected === "unverified" && expected && (
        <p className="mt-1.5 text-accent-900">Its identity has not been verified yet; tools will verify it against {expected} on first use.</p>
      )}
    </div>
  );
}

function field(result: CommandResult | null, label: string): string {
  return result?.fields?.find((entry) => entry.label === label)?.value ?? "";
}

function list(result: CommandResult | null, label: string): string[] {
  return field(result, label).split(",").filter(Boolean);
}

export function GoogleCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const { result, error, saving, save } = useConfigSection("google", onSessionExpired);
  const [clientId, setClientId] = useState("");
  const [clientSecretEnv, setClientSecretEnv] = useState("GOOGLE_CLIENT_SECRET");
  const [products, setProducts] = useState<string[]>(["calendar"]);
  const [enabled, setEnabled] = useState(true);
  // Defaults on means no list is stored at all, which is not the same as a
  // list that happens to match the defaults: with no list, an action added by
  // a later version is gated automatically instead of shipping unguarded.
  const [useDefaults, setUseDefaults] = useState(true);
  const [gated, setGated] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);

  // Seed the form from what is stored, once. This is an edit surface for one
  // existing section rather than an add form, so starting empty would invite
  // an owner to blank a client id by saving a product change.
  useEffect(() => {
    const row = result?.table_rows?.[0];
    if (!row || loaded) return;
    setEnabled(row[STATE] === "enabled");
    if (row[CLIENT_ID]) setClientId(row[CLIENT_ID]);
    if (row[SECRET_ENV]) setClientSecretEnv(row[SECRET_ENV]);
    if (row[PRODUCT_LIST]) setProducts(row[PRODUCT_LIST].split(", ").filter(Boolean));
    const custom = field(result, "require_approval_mode") === "custom";
    setUseDefaults(!custom);
    setGated(list(result, "require_approval"));
    setLoaded(true);
  }, [result, loaded]);

  function toggleProduct(product: string) {
    setProducts((current) =>
      current.includes(product) ? current.filter((name) => name !== product) : [...current, product],
    );
  }

  function toggleGated(entry: string) {
    setGated((current) => (current.includes(entry) ? current.filter((name) => name !== entry) : [...current, entry]));
  }

  // Turning defaults off pre-selects what the defaults already were, so the
  // owner starts from today's behaviour and narrows it rather than from an
  // empty grid that would gate nothing if they saved it.
  function chooseDefaults(next: boolean) {
    setUseDefaults(next);
    if (!next && gated.length === 0) {
      setGated(products.flatMap((product) => list(result, `mutations.${product}`).map((a) => `${product}.${a}`)));
    }
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    await save({
      enabled: enabled ? "true" : "false",
      client_id: clientId,
      client_secret_env: clientSecretEnv,
      products: products.join(","),
      require_approval_mode: useDefaults ? "default" : "custom",
      require_approval: useDefaults ? "" : gated.join(","),
    });
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="mx-0.5">
        <h3 className="text-[15px] font-semibold tracking-tight">Google Workspace</h3>
        <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">
          One grant across every product checked. The OAuth client must be a <strong>Desktop app</strong> client — a Web
          application client cannot authorize this way.
        </p>
      </div>
      <div className="flex flex-col gap-3">
        <DataTable headers={result?.table_headers?.slice(0, 4)} rows={result?.table_rows?.map((row) => row.slice(0, 4))} empty="Google is not configured yet." />
        {result?.table_rows?.[0] && (
          <GoogleIdentity connected={result.table_rows[0][CONNECTED_AS] ?? ""} expected={result.table_rows[0][EXPECTED_EMAIL] ?? ""} />
        )}
        <details className="rounded-2xl bg-neutral-100 p-4">
          <summary className="cursor-pointer text-[12.5px] font-medium">Configure Google Workspace</summary>
          <form onSubmit={handleSubmit} className="mt-3 flex flex-col gap-3">
            <Input
              placeholder="client_id (xxxx.apps.googleusercontent.com)"
              value={clientId}
              onChange={(e) => setClientId(e.target.value)}
              required
              className={FIELD}
            />
            <fieldset className="rounded-2xl bg-background p-3.5">
              <legend className="px-0.5 pb-2.5 text-[12.5px] font-medium">Products</legend>
              <div className="flex flex-wrap gap-1.5">
                {PRODUCTS.map((product) => {
                  const active = products.includes(product);
                  return (
                    <button
                      key={product}
                      type="button"
                      role="checkbox"
                      aria-checked={active}
                      onClick={() => toggleProduct(product)}
                      className={cn(
                        "min-h-[34px] rounded-full px-3.5 text-[12.5px] font-medium transition-colors",
                        active ? "bg-primary text-primary-foreground" : "bg-neutral-100 text-foreground hover:bg-neutral-200",
                      )}
                    >
                      {product}
                    </button>
                  );
                })}
              </div>
            </fieldset>
            <details>
              <summary className="cursor-pointer text-[12.5px] text-neutral-700">Advanced options</summary>
              <div className="mt-3 flex flex-col gap-3">
                <Input
                  placeholder="client_secret_env"
                  value={clientSecretEnv}
                  onChange={(e) => setClientSecretEnv(e.target.value)}
                  className={FIELD}
                />
                <div className="flex flex-col gap-3 rounded-2xl bg-background p-3.5">
                  <Switch checked={useDefaults} onCheckedChange={chooseDefaults} label="Ask before anything that writes" />
                  <p className="text-xs text-neutral-700">
                    New write actions are gated automatically. Turn this off to choose action by action.
                  </p>
                  {!useDefaults &&
                    products.map((product) => {
                      const actions = list(result, `actions.${product}`);
                      if (actions.length === 0) return null;
                      return (
                        <fieldset key={product} className="flex flex-wrap gap-x-3 gap-y-2">
                          <legend className="pb-1 text-sm font-medium">{product}</legend>
                          {actions.map((action) => {
                            const entry = `${product}.${action}`;
                            const writes = list(result, `mutations.${product}`).includes(action);
                            return (
                              <label key={entry} className="flex items-center gap-1.5 text-sm">
                                <input
                                  type="checkbox"
                                  checked={gated.includes(entry) || gated.includes(`${product}.*`)}
                                  disabled={gated.includes(`${product}.*`)}
                                  onChange={() => toggleGated(entry)}
                                  className="h-4 w-4 accent-primary"
                                />
                                <span className={writes ? "" : "text-neutral-700"}>{action}</span>
                              </label>
                            );
                          })}
                        </fieldset>
                      );
                    })}
                  {!useDefaults && gated.length === 0 && (
                    <p className="text-xs text-eg-red-ink">Nothing is checked, so Google writes will run without asking.</p>
                  )}
                </div>
              </div>
            </details>
            <Switch checked={enabled} onCheckedChange={setEnabled} label="Enabled" />
            <div>
              <Button type="submit" disabled={saving}>
                {saving ? "Saving..." : "Save Google Workspace"}
              </Button>
            </div>
          </form>
        </details>
        <p className="mx-0.5 text-xs leading-relaxed text-neutral-700">
          The client secret itself is never stored here — name the environment variable that holds it. After saving,
          restart Eggy and run <code>/google login</code> in chat to authorize.
        </p>
        {result?.detail && <p className="mx-0.5 text-xs text-neutral-700">{result.detail}</p>}
        {error && (
          <p className="rounded-2xl bg-eg-red-tint px-3.5 py-2.5 text-sm text-eg-red-ink" role="alert">
            {error}
          </p>
        )}
      </div>
    </section>
  );
}
