import { useEffect, useState } from "react";
import { useConfigSection } from "./useConfigSection";
import type { CommandResult } from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";

// The products the adapter knows. A product left unchecked has no tool at all,
// so this list is the whole of what Google can do here.
const PRODUCTS = ["calendar", "gmail", "drive", "docs", "sheets", "contacts"] as const;

// Column positions in the single row /api/config/google returns. One row,
// because there is one grant covering every product.
const STATE = 0;
const CLIENT_ID = 1;
const SECRET_ENV = 2;
const PRODUCT_LIST = 3;

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
    <Card>
      <CardHeader>
        <CardTitle>Google Workspace</CardTitle>
        <CardDescription>
          One grant across every product checked. The OAuth client must be a <strong>Desktop app</strong> client — a Web
          application client cannot authorize this way.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable headers={result?.table_headers} rows={result?.table_rows} empty="Google is not configured yet." />
        <details className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">Configure Google Workspace</summary>
          <form onSubmit={handleSubmit} className="mt-4 flex flex-col gap-3">
            <Input
              placeholder="client_id (xxxx.apps.googleusercontent.com)"
              value={clientId}
              onChange={(e) => setClientId(e.target.value)}
              required
            />
            <fieldset className="flex flex-wrap gap-3">
              <legend className="pb-2 text-sm text-muted-foreground">Products</legend>
              {PRODUCTS.map((product) => (
                <label key={product} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={products.includes(product)}
                    onChange={() => toggleProduct(product)}
                    className="h-4 w-4"
                  />
                  {product}
                </label>
              ))}
            </fieldset>
            <details>
              <summary className="cursor-pointer text-sm text-muted-foreground">Advanced options</summary>
              <div className="mt-3 flex flex-col gap-3">
                <Input
                  placeholder="client_secret_env"
                  value={clientSecretEnv}
                  onChange={(e) => setClientSecretEnv(e.target.value)}
                />
                <div className="flex flex-col gap-3 rounded-md border border-border px-3 py-3">
                  <Switch checked={useDefaults} onCheckedChange={chooseDefaults} label="Ask before anything that writes" />
                  <p className="text-xs text-muted-foreground">
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
                                  className="h-4 w-4"
                                />
                                <span className={writes ? "" : "text-muted-foreground"}>{action}</span>
                              </label>
                            );
                          })}
                        </fieldset>
                      );
                    })}
                  {!useDefaults && gated.length === 0 && (
                    <p className="text-xs text-destructive">Nothing is checked, so Google writes will run without asking.</p>
                  )}
                </div>
              </div>
            </details>
            <Switch checked={enabled} onCheckedChange={setEnabled} label="Enabled" />
            <Button type="submit" disabled={saving}>
              {saving ? "Saving..." : "Save Google Workspace"}
            </Button>
          </form>
        </details>
        <p className="text-xs text-muted-foreground">
          The client secret itself is never stored here — name the environment variable that holds it. After saving,
          restart Eggy and run <code>/google login</code> in chat to authorize.
        </p>
        {result?.detail && <p className="text-xs text-muted-foreground">{result.detail}</p>}
        {error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
