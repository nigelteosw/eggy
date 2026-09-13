import { useEffect, useState, type FormEvent } from "react";
import {
  completeSetup,
  consumeSetupFragment,
  exchangeSetupToken,
  getMode,
  validateSetup,
  type SetupInput,
  type SetupValidation,
} from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";

const defaults: SetupInput = {
  account_id: "you",
  google_email: "",
  public_base_url: typeof window === "undefined" ? "" : window.location.origin,
  login_client_id: "",
  login_client_secret_env: "EGGY_GOOGLE_LOGIN_CLIENT_SECRET",
  provider_name: "deepseek",
  provider_base_url: "https://api.deepseek.com",
  provider_api_key_env: "DEEPSEEK_API_KEY",
  model_alias: "deepseek-pro",
  model_id: "deepseek-v4-pro",
  telegram_enabled: false,
};

type Field = { name: keyof SetupInput; label: string; hint?: string; type?: string };

const sections: { title: string; description: string; fields: Field[] }[] = [
  {
    title: "Account",
    description: "Create the first person allowed to sign in. The account ID becomes its durable owner key.",
    fields: [
      { name: "account_id", label: "Account ID", hint: "Letters, digits, dots, underscores, or hyphens." },
      { name: "google_email", label: "Google email", type: "email" },
      { name: "public_base_url", label: "Public URL", type: "url" },
    ],
  },
  {
    title: "Sign-in",
    description: "Use a Google Web OAuth client. Enter the secret's environment variable name, never the secret.",
    fields: [
      { name: "login_client_id", label: "Google Web client ID" },
      { name: "login_client_secret_env", label: "Client secret variable" },
    ],
  },
  {
    title: "Model",
    description: "Configure the first OpenAI-compatible model connection.",
    fields: [
      { name: "provider_name", label: "Provider name" },
      { name: "provider_base_url", label: "Provider base URL", type: "url" },
      { name: "provider_api_key_env", label: "API key variable" },
      { name: "model_alias", label: "Model alias" },
      { name: "model_id", label: "Provider model ID" },
    ],
  },
];

function TelegramSection({ input, update, completed }: { input: SetupInput; update: (name: keyof SetupInput, value: string | boolean) => void; completed: boolean }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Telegram (optional)</CardTitle>
        <CardDescription>
          Enable Eggy's Telegram channel. This requires TELEGRAM_BOT_TOKEN and TELEGRAM_WEBHOOK_SECRET in the deployment
          environment; pairing a chat happens later, from the accounts settings.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <label className="flex items-center gap-2 text-sm" htmlFor="telegram_enabled">
          <input
            id="telegram_enabled"
            name="telegram_enabled"
            type="checkbox"
            checked={input.telegram_enabled}
            disabled={completed}
            onChange={(event) => update("telegram_enabled", event.target.checked)}
          />
          Enable Telegram
        </label>
      </CardContent>
    </Card>
  );
}

export function SetupPage() {
  const [input, setInput] = useState(defaults);
  const [authorized, setAuthorized] = useState(typeof window === "undefined");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [validation, setValidation] = useState<SetupValidation | null>(null);
  const [completed, setCompleted] = useState(false);

  useEffect(() => {
    consumeSetupFragment(window.location, window.history, exchangeSetupToken)
      .then(() => setAuthorized(true))
      .catch((reason) => setError(reason instanceof Error ? reason.message : "Could not authorize setup."));
  }, []);

  useEffect(() => {
    if (!completed) return;
    const poll = window.setInterval(() => {
      getMode().then((probe) => {
        if (probe.mode === "normal") window.location.reload();
        if (probe.mode === "safe") {
          window.clearInterval(poll);
          setError("Configuration was saved, but Eggy entered safe mode. Reload to repair it.");
        }
      }).catch(() => {});
    }, 1000);
    return () => window.clearInterval(poll);
  }, [completed]);

  function update(name: keyof SetupInput, value: string | boolean) {
    setInput((current) => ({ ...current, [name]: value }));
    setValidation(null);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const readiness = await validateSetup(input);
      setValidation(readiness);
      if (Object.values(readiness.variables).some((present) => !present)) return;
      await completeSetup(input);
      setCompleted(true);
    } catch (reason) {
      const detail = reason as Error & { validation?: SetupValidation };
      if (detail.validation) setValidation(detail.validation);
      setError(detail.message || "Could not complete setup.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="app-canvas min-h-screen px-4 py-8 sm:py-12">
      <form className="mx-auto flex w-full max-w-3xl flex-col gap-5" onSubmit={submit}>
        <header>
          <p className="text-sm font-medium text-primary">First-run setup</p>
          <h1 className="mt-1 text-2xl font-semibold tracking-tight">Configure Eggy</h1>
          <p className="mt-2 text-sm text-muted-foreground">Settings are written to config.yaml. Credential values stay in your deployment environment.</p>
        </header>

        {!authorized && !error && <p className="rounded-md border border-border bg-card p-3 text-sm">Authorizing this setup link…</p>}
        {sections.map((section) => (
          <Card key={section.title}>
            <CardHeader>
              <CardTitle>{section.title}</CardTitle>
              <CardDescription>{section.description}</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-4 sm:grid-cols-2">
              {section.fields.map((field) => (
                <div className={field.name.endsWith("url") ? "sm:col-span-2" : ""} key={field.name}>
                  <Label htmlFor={field.name}>{field.label}</Label>
                  <Input
                    className="mt-2"
                    id={field.name}
                    name={field.name}
                    type={field.type ?? "text"}
                    required
                    value={String(input[field.name])}
                    onChange={(event) => update(field.name, event.target.value)}
                    disabled={completed}
                  />
                  {field.hint && <p className="mt-1 text-xs text-muted-foreground">{field.hint}</p>}
                </div>
              ))}
            </CardContent>
          </Card>
        ))}

        <TelegramSection input={input} update={update} completed={completed} />

        {validation && (
          <Card>
            <CardHeader><CardTitle>Deployment variables</CardTitle></CardHeader>
            <CardContent className="space-y-2 text-sm">
              {Object.entries(validation.variables).map(([name, present]) => (
                <p key={name} className={present ? "text-muted-foreground" : "text-destructive"}>
                  <span className="font-mono">{name}</span>: {present ? "present" : "missing"}
                </p>
              ))}
              {Object.values(validation.variables).some((present) => !present) && (
                <p className="pt-2 text-muted-foreground">Provision missing variables in the deployment environment, then restart and use the newly printed setup URL.</p>
              )}
            </CardContent>
          </Card>
        )}
        {error && <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>}
        {completed ? (
          <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">Configuration saved. Waiting for Eggy to start…</p>
        ) : (
          <div><Button type="submit" disabled={!authorized || busy}>{busy ? "Checking…" : "Validate and start"}</Button></div>
        )}
      </form>
    </main>
  );
}
