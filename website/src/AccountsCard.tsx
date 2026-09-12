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

export function AccountsList({
  accounts,
  onEdit,
  onRemove,
  onReset,
}: {
  accounts: AccountRow[];
  onEdit: (account: AccountRow) => void;
  onRemove: (account: AccountRow) => void;
  onReset: (account: AccountRow) => void;
}) {
  if (accounts.length === 0) {
    return <p className="text-sm text-muted-foreground">No accounts yet.</p>;
  }
  return (
    <ul className="flex flex-col divide-y rounded-md border">
      {accounts.map((account) => (
        <li key={account.id} className="flex flex-col gap-2 p-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">
              {account.id}
              {account.self && <span className="ml-2 text-xs text-muted-foreground">(you)</span>}
            </p>
            <p className="truncate text-xs text-muted-foreground">{account.email}</p>
            <p className="mt-1 flex flex-wrap gap-2 text-xs">
              <span className={account.enrolled ? "text-foreground" : "text-muted-foreground"}>
                {account.enrolled ? "Enrolled" : "Not enrolled"}
              </span>
              <span className={account.signed_in ? "text-foreground" : "text-muted-foreground"}>
                {account.signed_in ? "Signed in" : "Not signed in"}
              </span>
              {account.telegram_user_id ? <span className="text-muted-foreground">Telegram {account.telegram_user_id}</span> : null}
            </p>
          </div>
          <div className="flex shrink-0 flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => onEdit(account)} aria-label={`Edit ${account.id}`}>
              Edit
            </Button>
            {account.enrolled && (
              <Button type="button" variant="outline" size="sm" onClick={() => onReset(account)} aria-label={`Reset binding for ${account.id}`}>
                Reset binding
              </Button>
            )}
            {!account.self && (
              <Button type="button" variant="outline" size="sm" onClick={() => onRemove(account)} aria-label={`Remove ${account.id}`}>
                Remove
              </Button>
            )}
          </div>
        </li>
      ))}
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
  const [telegram, setTelegram] = useState(initial?.telegram_user_id ? String(initial.telegram_user_id) : "");
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit({ id, email, telegram_user_id: telegram.trim() === "" ? 0 : Number(telegram) });
      }}
      className="flex flex-col gap-3"
    >
      {!initial && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="account-id">Account ID</Label>
          <Input id="account-id" value={id} onChange={(e) => setId(e.target.value)} placeholder="short name, e.g. nigel" required />
        </div>
      )}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="account-email">Google email</Label>
        <Input id="account-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        {initial?.enrolled && (
          <p className="text-xs text-muted-foreground">This account has enrolled. Reset its binding before changing the address.</p>
        )}
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="account-telegram">Telegram user ID (optional)</Label>
        <Input id="account-telegram" inputMode="numeric" value={telegram} onChange={(e) => setTelegram(e.target.value)} placeholder="numeric sender ID" />
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

  return (
    <Card>
      <CardHeader>
        <CardTitle>Accounts</CardTitle>
        <CardDescription>
          Who can use this Eggy. Each person signs in with their own Google account and has private conversations and
          memory; everyone can change these settings.
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
            <AccountsList
              accounts={view?.accounts ?? []}
              onEdit={(account) => {
                setEditing(account);
                setRemoving(null);
                setResetting(null);
              }}
              onRemove={(account) => {
                setRemoving(account);
                setEditing(null);
                setResetting(null);
              }}
              onReset={(account) => {
                setResetting(account);
                setEditing(null);
                setRemoving(null);
              }}
            />
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
            {editing ? (
              <details open className="rounded-md border p-3">
                <summary className="cursor-pointer text-sm font-medium">Edit {editing.id}</summary>
                <div className="mt-3">
                  <AccountForm
                    key={editing.id}
                    initial={editing}
                    saving={saving}
                    onCancel={() => setEditing(null)}
                    onSubmit={async (input) => {
                      if (await run(() => editAccount(editing.id, { email: input.email, telegram_user_id: input.telegram_user_id }))) setEditing(null);
                    }}
                  />
                </div>
              </details>
            ) : (
              <details className="rounded-md border p-3">
                <summary className="cursor-pointer text-sm font-medium">Add an account</summary>
                <div className="mt-3">
                  <AccountForm saving={saving} onSubmit={(input) => run(() => addAccount(input))} />
                </div>
              </details>
            )}
          </>
        )}

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
