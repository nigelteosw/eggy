import { useCallback, useEffect, useState } from "react";
import {
  AccountInput,
  AccountRow,
  AccountsView,
  SessionExpiredError,
  addAccount,
  convertToAccounts,
  editAccount,
  getAccounts,
  removeAccount,
  resetAccountBinding,
  setExpectedGoogleEmail,
  setLoginClient,
  createTelegramPairing,
  unlinkTelegram,
  setTelegramEnabled,
  type TelegramPairing,
} from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";

// The accounts card is the whole of who-may-use-Eggy, operated from here and
// nowhere else: the list, the sign-in client, and the address Eggy's own
// Google connection must belong to. Every write is a config mutation on the
// server, so what this card can do is exactly what config allows, and a
// change that config refuses comes back as the refusal's own words.

function describe(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

export function TelegramEnableControl({ enabled, onChanged, onError }: { enabled: boolean; onChanged: (message: string) => void; onError: (message: string) => void }) {
  const [busy, setBusy] = useState(false);
  async function toggle() {
    setBusy(true);
    try {
      await setTelegramEnabled(!enabled);
      onChanged(enabled ? "Telegram disabled. Restart Eggy to apply." : "Telegram enabled. Restart Eggy to apply, then each person links their own Telegram from their row above.");
    } catch (err) {
      onError(describe(err, "Could not change Telegram enablement"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="flex items-center justify-between gap-3 text-sm">
      <span>Telegram is {enabled ? "enabled" : "disabled"}.</span>
      <Button type="button" variant="outline" size="sm" disabled={busy} onClick={toggle}>
        {busy ? "Saving…" : enabled ? "Disable Telegram" : "Enable Telegram"}
      </Button>
    </div>
  );
}

// inviteText is what the person who added an account sends to the person it
// belongs to. It names every step between "added" and "chatting on
// Telegram", so nobody has to reconstruct the flow from the docs.
export function inviteText(account: AccountRow, telegramEnabled: boolean, origin: string): string {
  const lines = [
    `You've been added to Eggy as "${account.id}".`,
    `1. Open ${origin} and sign in with Google as ${account.email}.`,
  ];
  if (telegramEnabled) {
    lines.push('2. In Settings → Accounts, click "Link Telegram", open the link, and tap Start in Telegram.');
    lines.push("After that you can message Eggy on Telegram or in the web panel.");
  } else {
    lines.push("After that you can message Eggy in the web panel.");
  }
  return lines.join("\n");
}

function CopyButton({ text, label = "Copy invite" }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      window.prompt("Copy this invite", text);
    }
  }
  return (
    <Button type="button" variant="outline" size="sm" onClick={copy}>
      {copied ? "Copied" : label}
    </Button>
  );
}

// OnboardingSteps is the per-person checklist: where they are between being
// added and being reachable on Telegram, and what (and who) moves them to
// the next step. The person themself is the only one who can sign in or
// link Telegram, so for everyone else the panel can only say what to send
// them.
export function OnboardingSteps({ account, telegramEnabled, onDismiss }: { account: AccountRow; telegramEnabled: boolean; onDismiss?: () => void }) {
  const origin = typeof window === "undefined" ? "" : window.location.origin;
  const steps: { label: string; done: boolean; hint: string }[] = [
    { label: "Added to Eggy", done: true, hint: "" },
    {
      label: `Signs in with Google as ${account.email}`,
      done: account.enrolled,
      hint: account.enrolled ? "" : `Send them ${origin}. Their first Google sign-in enrolls them.`,
    },
  ];
  if (telegramEnabled) {
    steps.push({
      label: "Links their Telegram",
      done: !!account.telegram_user_id,
      hint: account.telegram_user_id ? "" : account.self ? "Click Link Telegram in your row above." : "Once signed in, they click Link Telegram in their row and tap Start in Telegram.",
    });
  }
  const complete = steps.every((step) => step.done);
  return (
    <div className="flex flex-col gap-3 rounded-md border bg-muted/40 p-4" role="status" aria-label={`Getting ${account.id} started`}>
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm font-medium">{complete ? `${account.id} is all set` : `Getting ${account.id} started`}</p>
        {onDismiss && (
          <Button type="button" variant="ghost" size="sm" onClick={onDismiss}>
            Dismiss
          </Button>
        )}
      </div>
      <ol className="flex flex-col gap-2 text-sm">
        {steps.map((step, index) => (
          <li key={index} className="flex gap-3">
            <span
              aria-hidden
              className={
                "mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-xs " +
                (step.done ? "bg-primary text-primary-foreground" : "border border-input text-muted-foreground")
              }
            >
              {step.done ? "✓" : index + 1}
            </span>
            <div className="min-w-0">
              <p className={step.done ? "text-muted-foreground line-through" : ""}>{step.label}</p>
              {step.hint && <p className="text-xs text-muted-foreground">{step.hint}</p>}
            </div>
          </li>
        ))}
      </ol>
      {!complete && !account.self && <CopyButton text={inviteText(account, telegramEnabled, origin)} />}
    </div>
  );
}

// TelegramLinkControl is the person's own Telegram, inline in their row.
// Only the signed-in person can pair or unpair, so the control renders for
// the self row and nowhere else.
export function TelegramLinkControl({ account, available, onError }: { account: AccountRow; available: boolean; onError: (message: string) => void }) {
  const [pairing, setPairing] = useState<TelegramPairing | null>(null);
  const [busy, setBusy] = useState(false);
  if (!account.self) return null;
  async function link() {
    setBusy(true);
    try { setPairing(await createTelegramPairing(account.id)); }
    catch (err) { onError(describe(err, "Could not start Telegram pairing")); }
    finally { setBusy(false); }
  }
  async function unlink() {
    setBusy(true);
    try { await unlinkTelegram(account.id); setPairing(null); window.location.reload(); }
    catch (err) { onError(describe(err, "Could not unlink Telegram")); }
    finally { setBusy(false); }
  }
  if (account.telegram_user_id) {
    return (
      <Button type="button" variant="link" size="sm" disabled={busy} onClick={unlink}>
        Unlink Telegram
      </Button>
    );
  }
  if (pairing) {
    return (
      <div className="flex flex-col gap-2 rounded-md border border-primary/30 bg-primary/5 p-3 text-sm w-full">
        <p className="font-medium">Finish in Telegram</p>
        <ol className="list-decimal pl-5 text-muted-foreground">
          <li>Open the link below on the device where you use Telegram.</li>
          <li>Tap <strong>Start</strong> in the chat that opens.</li>
          <li>Come back here — your row updates when it&apos;s done.</li>
        </ol>
        <div className="flex flex-wrap items-center gap-2">
          <Button asChild size="sm"><a href={pairing.url} target="_blank" rel="noreferrer">Open Telegram</a></Button>
          <CopyButton text={pairing.url} label="Copy link" />
          <Button type="button" variant="ghost" size="sm" onClick={() => window.location.reload()}>I&apos;ve done this</Button>
        </div>
        <p className="text-xs text-muted-foreground">
          Single-use, expires at {new Date(pairing.expires_at).toLocaleTimeString()}. Don&apos;t forward it: whoever opens it first claims your account&apos;s Telegram.
        </p>
      </div>
    );
  }
  return (
    <Button
      type="button"
      variant="link"
      size="sm"
      disabled={!available || busy}
      onClick={link}
      title={available ? undefined : "Pairing is unavailable until Eggy can discover the bot username. Check Telegram credentials and restart."}
    >
      {busy ? "Creating link…" : "Link Telegram"}
    </Button>
  );
}

function Avatar({ id, self }: { id: string; self: boolean }) {
  return (
    <span
      aria-hidden
      className={
        "flex h-10 w-10 shrink-0 items-center justify-center rounded-full text-sm font-semibold " +
        (self ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground")
      }
    >
      {(id[0] ?? "?").toUpperCase()}
    </span>
  );
}

export function AccountsList({
  accounts,
  telegramEnabled,
  pairingAvailable,
  onEdit,
  onRemove,
  onReset,
  onShowSteps,
  onError,
}: {
  accounts: AccountRow[];
  telegramEnabled: boolean;
  pairingAvailable: boolean;
  onEdit: (account: AccountRow) => void;
  onRemove: (account: AccountRow) => void;
  onReset: (account: AccountRow) => void;
  onShowSteps: (account: AccountRow) => void;
  onError: (message: string) => void;
}) {
  if (accounts.length === 0) {
    return <p className="text-sm text-muted-foreground">No accounts yet. Add the first one below.</p>;
  }
  return (
    <ul className="flex flex-col divide-y border-y">
      {accounts.map((account) => {
        const status = [
          account.enrolled ? "Enrolled" : "Not enrolled",
          account.signed_in ? "Signed in" : "Not signed in",
        ];
        if (telegramEnabled) status.push(account.telegram_user_id ? `Telegram ${account.telegram_user_id}` : "Telegram not linked");
        const pending = !account.enrolled || (telegramEnabled && !account.telegram_user_id);
        return (
          <li key={account.id} className="flex flex-wrap items-center gap-x-4 gap-y-3 py-4">
            <Avatar id={account.id} self={account.self} />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">
                {account.id}
                {account.self && <span className="ml-1 text-muted-foreground">(you)</span>}
              </p>
              <p className="truncate text-sm text-muted-foreground">{account.email}</p>
              <p className="mt-1 text-xs text-muted-foreground">{status.join(" · ")}</p>
            </div>
            <div className="flex flex-wrap items-center gap-1">
              {pending && !account.self && (
                <Button type="button" variant="link" size="sm" onClick={() => onShowSteps(account)} aria-label={`Getting started for ${account.id}`}>
                  Getting started
                </Button>
              )}
              {telegramEnabled && <TelegramLinkControl account={account} available={pairingAvailable} onError={onError} />}
              <Button type="button" variant="ghost" size="sm" onClick={() => onEdit(account)} aria-label={`Edit ${account.id}`}>
                Edit
              </Button>
              {account.enrolled && (
                <Button type="button" variant="ghost" size="sm" onClick={() => onReset(account)} aria-label={`Reset binding for ${account.id}`}>
                  Reset binding
                </Button>
              )}
              {!account.self && (
                <Button type="button" variant="link" size="sm" onClick={() => onRemove(account)} aria-label={`Remove ${account.id}`}>
                  Remove
                </Button>
              )}
            </div>
          </li>
        );
      })}
    </ul>
  );
}

// ResetBindingConfirm says what a reset does before it does it: the person
// is signed out everywhere and enrolls again with the configured address the
// next time they sign in. That is the consequence, and it is the reason a
// changed address needs this step rather than silently re-enrolling someone.
export function ResetBindingConfirm({ account, onConfirm, onCancel }: { account: string; onConfirm: () => void; onCancel: () => void }) {
  return (
    <div className="flex flex-col gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-3" role="alertdialog" aria-label={`Reset binding for ${account}`}>
      <p className="text-sm">
        Reset <strong>{account}</strong>&apos;s Google binding? They will be signed out everywhere and must enroll again with
        the Google account configured for them. Do this after changing their address, or if they lost access to the old
        Google account.
      </p>
      <div className="flex gap-2">
        <Button type="button" size="sm" onClick={onConfirm}>
          Reset binding
        </Button>
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

function RemoveConfirm({ account, onConfirm, onCancel }: { account: string; onConfirm: () => void; onCancel: () => void }) {
  return (
    <div className="flex flex-col gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-3" role="alertdialog" aria-label={`Remove ${account}`}>
      <p className="text-sm">
        Remove <strong>{account}</strong>? They are signed out immediately and can no longer sign in. Their private
        conversations and memory stay in the database.
      </p>
      <div className="flex gap-2">
        <Button type="button" size="sm" onClick={onConfirm}>
          Remove
        </Button>
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

function AccountForm({
  initial,
  onSubmit,
  onCancel,
  saving,
}: {
  initial?: AccountRow;
  onSubmit: (input: AccountInput) => void;
  onCancel?: () => void;
  saving: boolean;
}) {
  const [id, setId] = useState(initial?.id ?? "");
  const [email, setEmail] = useState(initial?.email ?? "");
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
		onSubmit({ id, email, telegram_user_id: initial?.telegram_user_id ?? 0 });
      }}
      className="flex flex-col gap-3"
    >
      <div className={"grid gap-3 " + (initial ? "" : "sm:grid-cols-2")}>
        {!initial && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-id">Account ID</Label>
            <Input id="account-id" value={id} onChange={(e) => setId(e.target.value)} placeholder="short name, e.g. nigel" required />
          </div>
        )}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="account-email">Google email</Label>
          <Input id="account-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="lauren@example.com" required />
          {initial?.enrolled && (
            <p className="text-xs text-muted-foreground">This account has enrolled. Reset its binding before changing the address.</p>
          )}
        </div>
      </div>
      <div className="flex gap-2">
        <Button type="submit" disabled={saving}>
          {saving ? "Saving..." : initial ? "Save account" : "Add account"}
        </Button>
        {onCancel && (
          <Button type="button" variant="outline" onClick={onCancel}>
            Cancel
          </Button>
        )}
      </div>
    </form>
  );
}

// ConvertForm is how a single-owner deployment becomes an accounts deployment
// from the panel: the people, the sign-in client, and which account the
// existing history belongs to, in one write. The legacy owner is proposed as
// the first account and as the migration owner, since that is almost always
// who is filling this in.
export function ConvertForm({
  legacyOwner,
  legacyTelegramId,
  onConverted,
  onSessionExpired,
}: {
  legacyOwner: string;
  legacyTelegramId?: number;
  onConverted: () => void;
  onSessionExpired: () => void;
}) {
  const [accounts, setAccounts] = useState<AccountInput[]>([
    { id: legacyTelegramId ? "owner" : legacyOwner || "owner", email: "", telegram_user_id: legacyTelegramId ?? 0 },
  ]);
  const [clientId, setClientId] = useState("");
  const [secretEnv, setSecretEnv] = useState("EGGY_GOOGLE_LOGIN_CLIENT_SECRET");
  const [migrationOwner, setMigrationOwner] = useState(accounts[0].id);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  function update(index: number, patch: Partial<AccountInput>) {
    setAccounts((current) => current.map((account, i) => (i === index ? { ...account, ...patch } : account)));
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await convertToAccounts({ accounts, login_client_id: clientId, login_client_secret_env: secretEnv, migration_owner_id: migrationOwner });
      onConverted();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(describe(err, "Conversion failed"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        This Eggy has one owner signing in with a password. Converting gives each person their own account, signed in
        with Google. The existing conversations and memory go to the migration owner; everyone else starts empty.
      </p>
      <fieldset className="flex flex-col gap-3">
        <legend className="text-sm font-medium">People</legend>
        {accounts.map((account, index) => (
          <div key={index} className="grid gap-2 rounded-md border p-3 sm:grid-cols-3">
            <Input aria-label={`Account ${index + 1} ID`} value={account.id} onChange={(e) => update(index, { id: e.target.value })} placeholder="id" required />
            <Input aria-label={`Account ${index + 1} email`} type="email" value={account.email} onChange={(e) => update(index, { email: e.target.value })} placeholder="google email" required />
            <Input
              aria-label={`Account ${index + 1} Telegram`}
              inputMode="numeric"
              value={account.telegram_user_id ? String(account.telegram_user_id) : ""}
              onChange={(e) => update(index, { telegram_user_id: e.target.value.trim() === "" ? 0 : Number(e.target.value) })}
              placeholder="telegram user id (optional)"
            />
          </div>
        ))}
        <Button type="button" variant="outline" size="sm" onClick={() => setAccounts((current) => [...current, { id: "", email: "", telegram_user_id: 0 }])}>
          Add another person
        </Button>
      </fieldset>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="convert-migration-owner">Migration owner (receives the existing history)</Label>
        <select
          id="convert-migration-owner"
          value={migrationOwner}
          onChange={(e) => setMigrationOwner(e.target.value)}
          className="h-11 rounded-md border border-input bg-card px-3 text-sm"
        >
          {accounts.map((account, index) => (
            <option key={index} value={account.id}>
              {account.id || `(account ${index + 1})`}
            </option>
          ))}
        </select>
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="convert-client-id">Google sign-in client ID (Web application client)</Label>
        <Input id="convert-client-id" value={clientId} onChange={(e) => setClientId(e.target.value)} required />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="convert-secret-env">client_secret_env (name of the variable holding the client secret)</Label>
        <Input id="convert-secret-env" value={secretEnv} onChange={(e) => setSecretEnv(e.target.value)} required />
      </div>
      {error && (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      <Button type="submit" disabled={saving}>
        {saving ? "Converting..." : "Convert to accounts"}
      </Button>
    </form>
  );
}

export function AccountsCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [view, setView] = useState<AccountsView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<AccountRow | null>(null);
  const [removing, setRemoving] = useState<AccountRow | null>(null);
  const [resetting, setResetting] = useState<AccountRow | null>(null);
  // steps is whose onboarding checklist is open: set when an account is
  // added, or when someone clicks "Getting started" on a row. It is keyed by
  // id so a reload keeps showing the same person's current progress.
  const [steps, setSteps] = useState<string | null>(null);
  const [clientId, setClientId] = useState("");
  const [secretEnv, setSecretEnv] = useState("");
  const [expected, setExpected] = useState("");
  const [seeded, setSeeded] = useState(false);

  const load = useCallback(() => {
    getAccounts()
      .then((loaded) => {
        setView(loaded);
        if (!seeded) {
          setClientId(loaded.login_client_id);
          setSecretEnv(loaded.login_client_secret_env || "EGGY_GOOGLE_LOGIN_CLIENT_SECRET");
          setExpected(loaded.expected_email);
          setSeeded(true);
        }
      })
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(describe(err, "Failed to load accounts"));
      });
  }, [onSessionExpired, seeded]);

  useEffect(() => {
    load();
  }, [load]);

  async function run(action: () => Promise<{ title?: string; detail?: string }>) {
    setSaving(true);
    setError(null);
    setNotice(null);
    try {
      const result = await action();
      setNotice([result.title, result.detail].filter(Boolean).join(" "));
      load();
      return true;
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return false;
      }
      setError(describe(err, "Request failed"));
      return false;
    } finally {
      setSaving(false);
    }
  }

  function show(which: "edit" | "remove" | "reset" | "steps", account: AccountRow) {
    setEditing(which === "edit" ? account : null);
    setRemoving(which === "remove" ? account : null);
    setResetting(which === "reset" ? account : null);
    setSteps(which === "steps" ? account.id : null);
  }

  const self = view?.accounts.find((account) => account.self);
  const stepsAccount = steps ? view?.accounts.find((account) => account.id === steps) : undefined;
  // A signed-in person whose Telegram is not linked yet is mid-onboarding,
  // and the checklist for their own account is the thing to show first.
  const selfNeedsTelegram = !!view?.telegram_enabled && !!self && !self.telegram_user_id;

  return (
    <Card>
      <CardHeader>
        <CardTitle>People</CardTitle>
        <CardDescription>
          Each person signs in with their own Google account and has private conversations and memory; everyone can
          change these settings.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        {error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
        {notice && <p className="rounded-md bg-muted px-3 py-2 text-sm">{notice}</p>}

        {view && !view.account_mode ? (
          <ConvertForm
            legacyOwner={view.legacy_owner ?? ""}
            legacyTelegramId={view.legacy_telegram_id}
            onConverted={() => {
              setNotice("Converted. Restart Eggy; from then on everyone signs in with Google.");
              load();
            }}
            onSessionExpired={onSessionExpired}
          />
        ) : (
          <>
            {selfNeedsTelegram && !stepsAccount && self && (
              <OnboardingSteps account={self} telegramEnabled={view!.telegram_enabled} />
            )}
            <AccountsList
              accounts={view?.accounts ?? []}
              telegramEnabled={view?.telegram_enabled ?? false}
              pairingAvailable={view?.telegram_pairing_available ?? false}
              onEdit={(account) => show("edit", account)}
              onRemove={(account) => show("remove", account)}
              onReset={(account) => show("reset", account)}
              onShowSteps={(account) => show("steps", account)}
              onError={setError}
            />
            {stepsAccount && view && (
              <OnboardingSteps account={stepsAccount} telegramEnabled={view.telegram_enabled} onDismiss={() => setSteps(null)} />
            )}
            {removing && (
              <RemoveConfirm
                account={removing.id}
                onConfirm={async () => {
                  if (await run(() => removeAccount(removing.id))) setRemoving(null);
                }}
                onCancel={() => setRemoving(null)}
              />
            )}
            {resetting && (
              <ResetBindingConfirm
                account={resetting.id}
                onConfirm={async () => {
                  if (await run(() => resetAccountBinding(resetting.id))) setResetting(null);
                }}
                onCancel={() => setResetting(null)}
              />
            )}
            <div className="flex flex-col gap-3 rounded-lg bg-muted/40 p-4">
              <p className="text-sm font-medium">{editing ? `Edit ${editing.id}` : "Add an account"}</p>
              {!editing && (
                <p className="text-xs text-muted-foreground">
                  Adding someone reserves their place; they enroll the first time they sign in with this Google
                  address. You&apos;ll get an invite to send them.
                </p>
              )}
              {editing ? (
                <AccountForm
                  key={editing.id}
                  initial={editing}
                  saving={saving}
                  onCancel={() => setEditing(null)}
                  onSubmit={async (input) => {
                    if (await run(() => editAccount(editing.id, { email: input.email, telegram_user_id: input.telegram_user_id }))) setEditing(null);
                  }}
                />
              ) : (
                <AccountForm
                  key={view?.accounts.length ?? 0}
                  saving={saving}
                  onSubmit={async (input) => {
                    if (await run(() => addAccount(input))) setSteps(input.id);
                  }}
                />
              )}
            </div>
          </>
        )}

        <details className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">Telegram</summary>
          <div className="mt-3 flex flex-col gap-3">
            <p className="text-xs text-muted-foreground">
              With Telegram enabled, each person links their own Telegram from their row above: a single-use link,
              opened in Telegram, binds that sender to their account. Nobody can link on someone else&apos;s behalf.
            </p>
            {view && (
              <TelegramEnableControl
                enabled={view.telegram_enabled}
                onChanged={(message) => {
                  setNotice(message);
                  load();
                }}
                onError={setError}
              />
            )}
            {view?.telegram_enabled && !view.telegram_pairing_available && (
              <p className="text-xs text-muted-foreground">
                Pairing is unavailable until Eggy can discover the bot username. Check Telegram credentials and restart.
              </p>
            )}
          </div>
        </details>

        <details className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">Google sign-in client</summary>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              run(() => setLoginClient(clientId, secretEnv));
            }}
            className="mt-3 flex flex-col gap-3"
          >
            <p className="text-xs text-muted-foreground">
              The <strong>Web application</strong> OAuth client people sign in with. Its redirect URI is this panel&apos;s
              address plus <code>/auth/google/callback</code>. The secret is read from the named environment variable and
              is never shown here.
            </p>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="login-client-id">Client ID</Label>
              <Input id="login-client-id" value={clientId} onChange={(e) => setClientId(e.target.value)} required />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="login-secret-env">client_secret_env</Label>
              <Input id="login-secret-env" value={secretEnv} onChange={(e) => setSecretEnv(e.target.value)} required />
            </div>
            <Button type="submit" disabled={saving} className="self-start">
              {saving ? "Saving..." : "Save sign-in client"}
            </Button>
          </form>
        </details>

        <details className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">Expected Google account</summary>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              run(() => setExpectedGoogleEmail(expected));
            }}
            className="mt-3 flex flex-col gap-3"
          >
            <p className="text-xs text-muted-foreground">
              Eggy&apos;s own Google Workspace user. Only this account can be connected under Connections; anyone
              accidentally authorizing their personal account is refused.
            </p>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="expected-email">Email</Label>
              <Input id="expected-email" type="email" value={expected} onChange={(e) => setExpected(e.target.value)} placeholder="eggy@yourdomain" />
            </div>
            <Button type="submit" disabled={saving} className="self-start">
              {saving ? "Saving..." : "Save expected account"}
            </Button>
          </form>
        </details>
      </CardContent>
    </Card>
  );
}
