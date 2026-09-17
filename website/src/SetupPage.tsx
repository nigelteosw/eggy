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
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { Switch } from "./components/ui/switch";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD } from "./components/ui/form";
import { errorMessage } from "./lib/utils";

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
    <section className="flex flex-col gap-3">
      <CardHeader
        title="Telegram (optional)"
        description={
          <>
            Enable Eggy's Telegram channel. This requires TELEGRAM_BOT_TOKEN and TELEGRAM_WEBHOOK_SECRET in the deployment
            environment; pairing a chat happens later, from the accounts settings.
          </>
        }
      />
      <Switch
        checked={input.telegram_enabled}
        onCheckedChange={(checked) => update("telegram_enabled", checked)}
        disabled={completed}
        label="Enable Telegram"
      />
    </section>
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
      .catch((reason) => setError(errorMessage(reason, "Could not authorize setup.")));
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
      <form className="mx-auto flex w-full max-w-3xl flex-col gap-6" onSubmit={submit}>
        <header>
          <p className="text-sm font-medium text-accent-700">First-run setup</p>
          <h1 className="mt-1 text-2xl font-semibold tracking-tight">Configure Eggy</h1>
          <p className="mt-2 text-sm text-neutral-700">Settings are written to config.yaml. Credential values stay in your deployment environment.</p>
        </header>

        {!authorized && !error && <p className="rounded-2xl bg-neutral-100 p-3.5 text-sm text-neutral-700">Authorizing this setup link…</p>}
        {sections.map((section) => (
          <section key={section.title} className="flex flex-col gap-3">
            <CardHeader
              title={section.title}
              description={section.description}
            />
            <div className="grid gap-3 rounded-2xl bg-neutral-100 p-4 sm:grid-cols-2">
              {section.fields.map((field) => (
                <div className={field.name.endsWith("url") ? "sm:col-span-2" : ""} key={field.name}>
                  <Label htmlFor={field.name}>{field.label}</Label>
                  <Input
                    className={`mt-2 ${FIELD}`}
                    id={field.name}
                    name={field.name}
                    type={field.type ?? "text"}
                    required
                    value={String(input[field.name])}
                    onChange={(event) => update(field.name, event.target.value)}
                    disabled={completed}
                  />
                  {field.hint && <p className="mt-1 text-xs text-neutral-700">{field.hint}</p>}
                </div>
              ))}
            </div>
          </section>
        ))}

        <TelegramSection input={input} update={update} completed={completed} />

        {validation && (
          <section className="flex flex-col gap-3">
            <CardHeader title="Deployment variables" />
            <div className="flex flex-col gap-2 rounded-2xl bg-neutral-100 p-4 text-sm">
              {Object.entries(validation.variables).map(([name, present]) => (
                <p key={name} className={present ? "text-neutral-700" : "text-eg-red-ink"}>
                  <span className="font-mono">{name}</span>: {present ? "present" : "missing"}
                </p>
              ))}
              {Object.values(validation.variables).some((present) => !present) && (
                <p className="pt-2 text-neutral-700">Provision missing variables in the deployment environment, then restart and use the newly printed setup URL.</p>
              )}
            </div>
          </section>
        )}
        {error && <ErrorBanner>{error}</ErrorBanner>}
        {completed ? (
          <p className="rounded-2xl bg-accent-100 px-3.5 py-2.5 text-sm text-accent-900" role="status">Configuration saved. Waiting for Eggy to start…</p>
        ) : (
          <div><Button type="submit" disabled={!authorized || busy} className="rounded-xl">{busy ? "Checking…" : "Validate and start"}</Button></div>
        )}
      </form>
    </main>
  );
}
