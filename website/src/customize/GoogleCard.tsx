import { SharedLabel } from "../components/ui/shared-label";
import { Field } from "../components/ui/field";
import { SaveBar } from "../components/ui/save-bar";
import { useEffect, useState } from "react";
import { useConfigSection } from "../settings/useConfigSection";
import type { CommandResult } from "../lib/api";
import { cn } from "../lib/utils";
import { Button } from "../components/ui/button";
import { DataTable } from "../components/ui/data-table";
import { Input } from "../components/ui/input";
import { Switch } from "../components/ui/switch";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { FIELD } from "../components/ui/form";

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
export function GoogleIdentity({
  connected,
  expected,
  connectUrl = "",
}: {
  connected: string;
  expected: string;
  connectUrl?: string;
}) {
  const account = expected || "Eggy's own Google account";
  if (!connected || connected === "not connected") {
    if (connectUrl) {
      return (
        <div className="flex flex-wrap items-center gap-3">
          <Button asChild>
            <a href={connectUrl}>Connect Google</a>
          </Button>
          <p className="text-small text-neutral-700">Sign in as {account}, not your own account.</p>
        </div>
      );
    }
    return (
      <p className="mx-0.5 text-small text-neutral-700">
        Not connected.{expected ? ` Connect as ${expected} with /google login in chat.` : ""}
      </p>
    );
  }
  const mismatch = expected !== "" && connected !== expected && connected !== "unverified";
  return (
    <div className={`rounded-card px-[18px] py-3.5 text-small ${mismatch ? "bg-tomato-soft" : "bg-accent-100"}`}>
      <p className={`flex flex-wrap items-center gap-2.5 ${mismatch ? "text-tomato" : "text-accent-900"}`}>
        Connected as <strong>{connected === "unverified" ? "an unverified account" : connected}</strong>
        <SharedLabel className="bg-background" />
      </p>
      {mismatch && (
        <p className="mt-1.5 text-tomato" role="alert">
          This is not Eggy&apos;s account: expected {expected}. Disconnect and reconnect signed in as {expected}.
        </p>
      )}
      {connected === "unverified" && expected && (
        <p className="mt-1.5 text-accent-900">Its identity has not been verified yet; tools will verify it against {expected} on first use.</p>
      )}
      {connectUrl && (
        <p className="mt-1.5">
          <a href={connectUrl} className="text-caption underline">
            Reconnect as {account}
          </a>
        </p>
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
  const [clientType, setClientType] = useState("desktop");
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
    if (field(result, "client_type")) setClientType(field(result, "client_type"));
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
      client_type: clientType,
      products: products.join(","),
      require_approval_mode: useDefaults ? "default" : "custom",
      require_approval: useDefaults ? "" : gated.join(","),
    });
  }

  return (
    <section className="flex flex-col gap-3">
      <CardHeader
        title="Google Workspace"
        description={
          <>
            One grant across every product checked, connected as Eggy&apos;s own Google account and shared with every Eggy
            user.
          </>
        }
      />
      <div className="flex flex-col gap-3">
        <DataTable headers={result?.table_headers?.slice(0, 4)} rows={result?.table_rows?.map((row) => row.slice(0, 4))} empty="Google is not configured yet." />
        {result?.table_rows?.[0] && (
          <GoogleIdentity
            connected={result.table_rows[0][CONNECTED_AS] ?? ""}
            expected={result.table_rows[0][EXPECTED_EMAIL] ?? ""}
            connectUrl={field(result, "connect_url")}
          />
        )}
        <details className="rounded-card bg-neutral-100 p-4">
          <summary className="cursor-pointer text-small font-medium">Configure Google Workspace</summary>
          <form onSubmit={handleSubmit} className="mt-3 flex flex-col gap-3">
            <Field label="OAuth client ID" configKey="google.client_id">
              <Input placeholder="xxxx.apps.googleusercontent.com" value={clientId} onChange={(e) => setClientId(e.target.value)} required className={FIELD} />
            </Field>
            <fieldset className="flex flex-col gap-2 rounded-card bg-background p-3.5">
              <legend className="px-0.5 pb-2.5 text-small font-medium">OAuth client type</legend>
              {(
                [
                  ["web", "Web application", "Connect from this panel and Google brings you back. Register the redirect URI below on the client."],
                  ["desktop", "Desktop app", "Nothing to register. Run /google login in chat and paste back the page that fails to load."],
                ] as const
              ).map(([value, label, help]) => (
                <label key={value} className="flex items-start gap-2 text-small">
                  <input
                    type="radio"
                    name="google-client-type"
                    value={value}
                    checked={clientType === value}
                    onChange={() => setClientType(value)}
                    className="mt-1 h-4 w-4 accent-primary"
                  />
                  <span>
                    <span className="font-medium">{label}</span>
                    <span className="block text-caption text-neutral-700">{help}</span>
                  </span>
                </label>
              ))}
              {clientType === "web" && field(result, "callback_url") && (
                <p className="text-caption text-neutral-700">
                  Authorized redirect URI: <code className="select-all break-all">{field(result, "callback_url")}</code>
                </p>
              )}
            </fieldset>
            <fieldset className="rounded-card bg-background p-3.5">
              <legend className="px-0.5 pb-2.5 text-small font-medium">Products</legend>
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
                        "min-h-[34px] rounded-full px-3.5 text-small font-medium transition-colors",
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
              <summary className="cursor-pointer text-small text-neutral-700">Advanced options</summary>
              <div className="mt-3 flex flex-col gap-3">
                <Field label="Client secret, read from the environment variable" configKey="google.client_secret_env">
                  <Input placeholder="GOOGLE_CLIENT_SECRET" value={clientSecretEnv} onChange={(e) => setClientSecretEnv(e.target.value)} className={FIELD} />
                </Field>
                <div className="flex flex-col gap-3 rounded-card bg-background p-3.5">
                  <Switch checked={useDefaults} onCheckedChange={chooseDefaults} label="Ask before anything that writes" />
                  <p className="text-caption text-neutral-700">
                    New write actions are gated automatically. Turn this off to choose action by action.
                  </p>
                  {!useDefaults &&
                    products.map((product) => {
                      const actions = list(result, `actions.${product}`);
                      if (actions.length === 0) return null;
                      return (
                        <fieldset key={product} className="flex flex-wrap gap-x-3 gap-y-2">
                          <legend className="pb-1 text-small font-medium">{product}</legend>
                          {actions.map((action) => {
                            const entry = `${product}.${action}`;
                            const writes = list(result, `mutations.${product}`).includes(action);
                            return (
                              <label key={entry} className="flex items-center gap-1.5 text-small">
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
                    <p className="text-caption text-tomato">Nothing is checked, so Google writes will run without asking.</p>
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
            <SaveBar label="Save Google" />
          </form>
        </details>
        <p className="mx-0.5 text-caption leading-relaxed text-neutral-700">
          The client secret itself is never stored here — name the environment variable that holds it. After saving,
          restart Eggy, then connect: with the Connect button for a Web client, or <code>/google login</code> in chat.
        </p>
        {result?.detail && <p className="mx-0.5 text-caption text-neutral-700">{result.detail}</p>}
        {error && (
          <ErrorBanner>
            {error}
          </ErrorBanner>
        )}
      </div>
    </section>
  );
}
