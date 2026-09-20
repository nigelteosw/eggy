import { useCallback, useEffect, useState } from "react";
import {
  AccountInput,
  AccountRow,
  AccountsView,
  PendingAccountError,
  SessionExpiredError,
  addAccount,
  convertToAccounts,
  editAccount,
  getAccounts,
  removeAccount,
  revokeAccountSessions,
  setAccountPassword,
  setExpectedGoogleEmail,
  createTelegramPairing,
  unlinkTelegram,
  setTelegramEnabled,
  createDiscordLink,
  unlinkDiscord,
  type TelegramPairing,
  type DiscordLink,
} from "./api";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { cn, errorMessage } from "./lib/utils";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD_COMPACT, FIELD_LABEL, PRIMARY_BUTTON } from "./components/ui/form";

// The People card is the whole of who-may-use-Eggy, operated from here and
// nowhere else: the list, each person's password and chat links, and the
// address Eggy's own Google connection must belong to. Membership writes are
// config mutations on the server; passwords go to the credential store and
// never come back. Every trusted person can do everything here: there are
// no roles.

const ghostButtonClass =
  "min-h-10 whitespace-nowrap rounded-xl px-4 text-[13.5px] font-medium text-neutral-700 transition-colors hover:bg-neutral-200 disabled:pointer-events-none disabled:opacity-50";
const destructiveButtonClass =
  "min-h-10 whitespace-nowrap rounded-xl bg-eg-red-tint px-4 text-[13.5px] font-semibold text-eg-red-ink transition-opacity hover:opacity-90 disabled:pointer-events-none disabled:opacity-50";
const textActionClass =
  "min-h-8 whitespace-nowrap rounded-lg px-2.5 text-[12.5px] text-foreground transition-colors hover:bg-neutral-100 disabled:pointer-events-none disabled:opacity-50";
const destructiveTextActionClass =
  "min-h-8 whitespace-nowrap rounded-lg px-2.5 text-[12.5px] text-eg-red-ink transition-colors hover:bg-eg-red-tint disabled:pointer-events-none disabled:opacity-50";
const panelClass = "rounded-2xl bg-neutral-100 p-4";

export function TelegramEnableControl({ enabled, onChanged, onError }: { enabled: boolean; onChanged: (message: string) => void; onError: (message: string) => void }) {
  const [busy, setBusy] = useState(false);
  async function toggle() {
    setBusy(true);
    try {
      await setTelegramEnabled(!enabled);
      onChanged(enabled ? "Telegram disabled. Restart Eggy to apply." : "Telegram enabled. Restart Eggy to apply, then each person links their own Telegram from their row above.");
    } catch (err) {
      onError(errorMessage(err, "Could not change Telegram enablement"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="flex items-center justify-between gap-3 text-sm">
      <span>Telegram is {enabled ? "enabled" : "disabled"}.</span>
      <button type="button" disabled={busy} onClick={toggle} className={ghostButtonClass}>
        {busy ? "Saving…" : enabled ? "Disable Telegram" : "Enable Telegram"}
      </button>
    </div>
  );
}

// inviteText is what the person who added an account sends to the person it
// belongs to. It names every step between "added" and "chatting on
// Telegram", so nobody has to reconstruct the flow from the docs. The
// password is deliberately not here: it is handed over some other way.
export function inviteText(account: AccountRow, telegramEnabled: boolean, origin: string): string {
  const lines = [
    `You've been added to Eggy as "${account.id}".`,
    `1. Open ${origin} and sign in with the username "${account.id}" and the password you were given.`,
  ];
  if (telegramEnabled) {
    lines.push('2. In Settings → People, click "Link Telegram", open the link, and tap Start in Telegram.');
    lines.push("After that you can message Eggy on Telegram or in the web panel, and send /web there for a one-tap sign-in link.");
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
    <button type="button" onClick={copy} className={textActionClass}>
      {copied ? "Copied" : label}
    </button>
  );
}

// OnboardingSteps is the per-person checklist: where they are between being
// added and being reachable on Telegram, and what (and who) moves them to
// the next step. The person themself is the only one who can sign in or
// link Telegram, so for everyone else the panel can only say what to send
// them.
export function OnboardingSteps({ account, telegramEnabled, onDismiss }: { account: AccountRow; telegramEnabled: boolean; onDismiss?: () => void }) {
  const origin = typeof window === "undefined" ? "" : window.location.origin;
  const ready = account.password_state !== "pending";
  const steps: { label: string; done: boolean; hint: string }[] = [
    { label: "Added to Eggy", done: true, hint: "" },
    {
      label: account.password_state === "environment" ? "Signs in with the deployment's credentials" : "Has a password",
      done: ready,
      hint: ready ? "" : "Their password was not stored. Set it from their row.",
    },
    {
      label: "Signs in",
      done: account.signed_in,
      hint: account.signed_in ? "" : ready ? `Send them ${origin} and their password.` : "",
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
    <div className={cn(panelClass, "flex flex-col gap-3")} role="status" aria-label={`Getting ${account.id} started`}>
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm font-medium">{complete ? `${account.id} is all set` : `Getting ${account.id} started`}</p>
        {onDismiss && (
          <button type="button" onClick={onDismiss} className={textActionClass}>
            Dismiss
          </button>
        )}
      </div>
      <ol className="flex flex-col gap-2 text-sm">
        {steps.map((step, index) => (
          <li key={index} className="flex gap-3">
            <span
              aria-hidden
              className={cn(
                "mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-xs",
                step.done ? "bg-primary text-primary-foreground" : "text-neutral-700 shadow-[inset_0_0_0_1px_hsl(var(--neutral-300))]",
              )}
            >
              {step.done ? "✓" : index + 1}
            </span>
            <div className="min-w-0">
              <p className={step.done ? "text-neutral-700 line-through" : ""}>{step.label}</p>
              {step.hint && <p className="text-xs text-neutral-700">{step.hint}</p>}
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
    catch (err) { onError(errorMessage(err, "Could not start Telegram pairing")); }
    finally { setBusy(false); }
  }
  async function unlink() {
    setBusy(true);
    try { await unlinkTelegram(account.id); setPairing(null); window.location.reload(); }
    catch (err) { onError(errorMessage(err, "Could not unlink Telegram")); }
    finally { setBusy(false); }
  }
  if (account.telegram_user_id) {
    return (
      <button type="button" disabled={busy} onClick={unlink} className={textActionClass}>
        Unlink Telegram
      </button>
    );
  }
  if (pairing) {
    return (
      <div className="flex w-full flex-col gap-2 rounded-2xl bg-accent-100 p-3 text-sm text-accent-900">
        <p className="font-medium">Finish in Telegram</p>
        <ol className="list-decimal pl-5">
          <li>Open the link below on the device where you use Telegram.</li>
          <li>Tap <strong>Start</strong> in the chat that opens.</li>
          <li>Come back here — your row updates when it&apos;s done.</li>
        </ol>
        <div className="flex flex-wrap items-center gap-2">
          <a href={pairing.url} target="_blank" rel="noreferrer" className={cn(PRIMARY_BUTTON, "inline-flex items-center")}>
            Open Telegram
          </a>
          <CopyButton text={pairing.url} label="Copy link" />
          <button type="button" onClick={() => window.location.reload()} className={textActionClass}>
            I&apos;ve done this
          </button>
        </div>
        <p className="text-xs">
          Single-use, expires at {new Date(pairing.expires_at).toLocaleTimeString()}. Don&apos;t forward it: whoever opens it first claims your account&apos;s Telegram.
        </p>
      </div>
    );
  }
  return (
    <button
      type="button"
      disabled={!available || busy}
      onClick={link}
      title={available ? undefined : "Pairing is unavailable until Eggy can discover the bot username. Check Telegram credentials and restart."}
      className={textActionClass}
    >
      {busy ? "Creating link…" : "Link Telegram"}
    </button>
  );
}

// DiscordLinkControl is the person's own Discord, inline in their row. There
// is no deep link into a DM, so the person copies a single-use command and
// sends it to the bot; the token is the whole proof.
export function DiscordLinkControl({ account, available, onError }: { account: AccountRow; available: boolean; onError: (message: string) => void }) {
  const [link, setLink] = useState<DiscordLink | null>(null);
  const [busy, setBusy] = useState(false);
  if (!account.self) return null;
  async function start() {
    setBusy(true);
    try { setLink(await createDiscordLink(account.id)); }
    catch (err) { onError(errorMessage(err, "Could not start Discord linking")); }
    finally { setBusy(false); }
  }
  async function unlink() {
    setBusy(true);
    try { await unlinkDiscord(account.id); setLink(null); window.location.reload(); }
    catch (err) { onError(errorMessage(err, "Could not unlink Discord")); }
    finally { setBusy(false); }
  }
  if (account.discord_user_id) {
    return (
      <button type="button" disabled={busy} onClick={unlink} className={textActionClass}>
        Unlink Discord
      </button>
    );
  }
  if (link) {
    return (
      <div className="flex w-full flex-col gap-2 rounded-2xl bg-accent-100 p-3 text-sm text-accent-900">
        <p className="font-medium">Finish in Discord</p>
        <ol className="list-decimal pl-5">
          <li>Open a direct message with the Eggy bot.</li>
          <li>Send it the command below, exactly as shown.</li>
          <li>Come back here — your row updates when it&apos;s done.</li>
        </ol>
        <code className="break-all rounded-lg bg-background px-2 py-1 text-xs">{link.command}</code>
        <div className="flex flex-wrap items-center gap-2">
          <CopyButton text={link.command} label="Copy command" />
          {link.dm_url && (
            <a href={link.dm_url} target="_blank" rel="noreferrer" className={cn(PRIMARY_BUTTON, "inline-flex items-center")}>
              Open Discord
            </a>
          )}
          <button type="button" onClick={() => window.location.reload()} className={textActionClass}>
            I&apos;ve done this
          </button>
        </div>
        <p className="text-xs">
          Single-use, expires at {new Date(link.expires_at).toLocaleTimeString()}. Don&apos;t share it: whoever sends it first claims your account&apos;s Discord.
        </p>
      </div>
    );
  }
  return (
    <button
      type="button"
      disabled={!available || busy}
      onClick={start}
      title={available ? undefined : "Linking is unavailable until Discord is enabled and Eggy restarted."}
      className={textActionClass}
    >
      {busy ? "Creating token…" : "Link Discord"}
    </button>
  );
}

function Avatar({ id, self }: { id: string; self: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-[13px] font-semibold",
        self ? "bg-accent-100 text-accent-900" : "bg-neutral-100 text-neutral-700",
      )}
    >
      {(id[0] ?? "?").toUpperCase()}
    </span>
  );
}

// passwordLabel is the one-line credential state a row shows.
export function passwordLabel(account: AccountRow): string {
  switch (account.password_state) {
    case "environment":
      return "Managed in deployment environment";
    case "set":
      return "Password set";
    default:
      return "Password pending";
  }
}

export function AccountsList({
  accounts,
  telegramEnabled,
  pairingAvailable,
  discordEnabled = false,
  discordLinkingAvailable = false,
  onEdit,
  onRemove,
  onPassword,
  onRevoke,
  onShowSteps,
  onError,
}: {
  accounts: AccountRow[];
  telegramEnabled: boolean;
  pairingAvailable: boolean;
  discordEnabled?: boolean;
  discordLinkingAvailable?: boolean;
  onEdit: (account: AccountRow) => void;
  onRemove: (account: AccountRow) => void;
  onPassword: (account: AccountRow) => void;
  onRevoke: (account: AccountRow) => void;
  onShowSteps: (account: AccountRow) => void;
  onError: (message: string) => void;
}) {
  if (accounts.length === 0) {
    return <p className="text-sm text-neutral-700">No accounts yet. Add the first one below.</p>;
  }
  return (
    <ul className="flex flex-col">
      {accounts.map((account) => {
        const status = [passwordLabel(account), account.signed_in ? "Signed in" : "Not signed in"];
        if (telegramEnabled) status.push(account.telegram_user_id ? `Telegram ${account.telegram_user_id}` : "Telegram not linked");
        if (telegramEnabled && account.web_link_available) status.push("/web available");
        if (discordEnabled) status.push(account.discord_user_id ? `Discord ${account.discord_user_id}` : "Discord not linked");
        const pending = account.password_state === "pending" || !account.signed_in || (telegramEnabled && !account.telegram_user_id);
        const passwordAction =
          account.password_state === "environment" ? null : account.password_state === "pending" ? "Retry password setup" : account.self ? "Change password" : "Reset password";
        return (
          <li key={account.id} className="flex flex-wrap items-center gap-x-4 gap-y-3 px-1 py-3.5 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]">
            <Avatar id={account.id} self={account.self} />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">
                {account.id}
                {account.self && <span className="ml-1 text-neutral-700">(you)</span>}
              </p>
              <p className="mt-0.5 text-xs tabular-nums text-neutral-700">{status.join(" · ")}</p>
            </div>
            <div className="flex flex-wrap items-center gap-1">
              {pending && !account.self && (
                <button type="button" onClick={() => onShowSteps(account)} aria-label={`Getting started for ${account.id}`} className={textActionClass}>
                  Getting started
                </button>
              )}
              {telegramEnabled && <TelegramLinkControl account={account} available={pairingAvailable} onError={onError} />}
              {discordEnabled && <DiscordLinkControl account={account} available={discordLinkingAvailable} onError={onError} />}
              <button type="button" onClick={() => onEdit(account)} aria-label={`Edit ${account.id}`} className={textActionClass}>
                Edit
              </button>
              {passwordAction && (
                <button type="button" onClick={() => onPassword(account)} aria-label={`${passwordAction} for ${account.id}`} className={textActionClass}>
                  {passwordAction}
                </button>
              )}
              <button type="button" onClick={() => onRevoke(account)} aria-label={`Revoke sessions for ${account.id}`} className={textActionClass}>
                Revoke sessions
              </button>
              {!account.self && (
                <button type="button" onClick={() => onRemove(account)} aria-label={`Remove ${account.id}`} className={destructiveTextActionClass}>
                  Remove
                </button>
              )}
            </div>
          </li>
        );
      })}
    </ul>
  );
}

// PasswordForm sets or changes one person's password. Your own change asks
// for the current password and ends every session you have, this one
// included; another person's reset needs no current password and ends
// theirs. Fields are cleared on submit either way: a password never lingers
// in a form after it has been sent, and never appears in an invite.
export function PasswordForm({ account, saving, onSubmit, onCancel }: { account: AccountRow; saving: boolean; onSubmit: (password: string, current?: string) => Promise<boolean>; onCancel: () => void }) {
  const [password, setPassword] = useState("");
  const [current, setCurrent] = useState("");
  const title = account.password_state === "pending" ? `Set a password for ${account.id}` : account.self ? "Change your password" : `Reset ${account.id}'s password`;
  return (
    <form
      onSubmit={async (event) => {
        event.preventDefault();
        const next = password;
        const previous = current;
        setPassword("");
        setCurrent("");
        await onSubmit(next, account.self ? previous : undefined);
      }}
      className={cn(panelClass, "flex flex-col gap-3")}
      aria-label={title}
    >
      <p className="text-[12.5px] font-medium">{title}</p>
      {account.self ? (
        <p className="text-xs text-neutral-700">You will be signed out everywhere and sign in again with the new password.</p>
      ) : (
        <p className="text-xs text-neutral-700">They are signed out everywhere and any unused Telegram sign-in link stops working. Hand the new password over yourself; it is never shown again.</p>
      )}
      {account.self && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="current-password" className={FIELD_LABEL}>Current password</Label>
          <Input id="current-password" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required className={FIELD_COMPACT} />
        </div>
      )}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="new-password" className={FIELD_LABEL}>New password (at least 12 characters)</Label>
        <Input id="new-password" type="password" autoComplete="new-password" minLength={12} value={password} onChange={(e) => setPassword(e.target.value)} required className={FIELD_COMPACT} />
      </div>
      <div className="flex gap-2">
        <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
          {saving ? "Saving..." : account.self ? "Change password" : "Set password"}
        </button>
        <button type="button" onClick={onCancel} className={ghostButtonClass}>
          Cancel
        </button>
      </div>
    </form>
  );
}

// RevokeConfirm says what revocation does before it does it: every session
// and unused sign-in link of the account ends; the password stays. It is
// the one credential control the environment-managed account has here.
export function RevokeConfirm({ account, self, onConfirm, onCancel }: { account: string; self: boolean; onConfirm: () => void; onCancel: () => void }) {
  return (
    <div className="flex flex-col gap-3 rounded-2xl bg-eg-red-tint p-3.5" role="alertdialog" aria-label={`Revoke sessions for ${account}`}>
      <p className="text-sm text-eg-red-ink">
        Revoke every session of <strong>{account}</strong>? {self ? "You" : "They"} will be signed out everywhere and any unused
        Telegram sign-in link stops working. The password does not change.
      </p>
      <div className="flex gap-2">
        <button type="button" onClick={onConfirm} className={destructiveButtonClass}>
          Revoke sessions
        </button>
        <button type="button" onClick={onCancel} className={ghostButtonClass}>
          Cancel
        </button>
      </div>
    </div>
  );
}

function RemoveConfirm({ account, onConfirm, onCancel }: { account: string; onConfirm: () => void; onCancel: () => void }) {
  return (
    <div className="flex flex-col gap-3 rounded-2xl bg-eg-red-tint p-3.5" role="alertdialog" aria-label={`Remove ${account}`}>
      <p className="text-sm text-eg-red-ink">
        Remove <strong>{account}</strong>? They are signed out immediately and can no longer sign in. Their private
        conversations and memory stay in the database, and this username is never reissued.
      </p>
      <div className="flex gap-2">
        <button type="button" onClick={onConfirm} className={destructiveButtonClass}>
          Remove
        </button>
        <button type="button" onClick={onCancel} className={ghostButtonClass}>
          Cancel
        </button>
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
  onSubmit: (input: AccountInput & { password: string }) => void;
  onCancel?: () => void;
  saving: boolean;
}) {
  const [id, setId] = useState(initial?.id ?? "");
  const [password, setPassword] = useState("");
  const [telegram, setTelegram] = useState(initial?.telegram_user_id ? String(initial.telegram_user_id) : "");
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        const next = password;
        setPassword("");
        onSubmit({ id, password: next, telegram_user_id: telegram.trim() === "" ? 0 : Number(telegram) });
      }}
      className="flex flex-col gap-3"
    >
      <div className={"grid gap-3 " + (initial ? "" : "sm:grid-cols-2")}>
        {!initial && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-id" className={FIELD_LABEL}>Username</Label>
            <Input id="account-id" value={id} onChange={(e) => setId(e.target.value)} placeholder="short name, e.g. nigel" autoCapitalize="none" required className={FIELD_COMPACT} />
            <p className="text-xs text-neutral-700">Immutable: it cannot be renamed later, and a removed username is never reissued.</p>
          </div>
        )}
        {!initial && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-password" className={FIELD_LABEL}>Password (at least 12 characters)</Label>
            <Input id="account-password" type="password" autoComplete="new-password" minLength={12} value={password} onChange={(e) => setPassword(e.target.value)} required className={FIELD_COMPACT} />
          </div>
        )}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="account-telegram" className={FIELD_LABEL}>Telegram user ID (optional)</Label>
          <Input id="account-telegram" inputMode="numeric" value={telegram} onChange={(e) => setTelegram(e.target.value)} placeholder="numeric, never a username" className={FIELD_COMPACT} />
        </div>
      </div>
      <div className="flex gap-2">
        <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
          {saving ? "Saving..." : initial ? "Save account" : "Add account"}
        </button>
        {onCancel && (
          <button type="button" onClick={onCancel} className={ghostButtonClass}>
            Cancel
          </button>
        )}
      </div>
    </form>
  );
}

// ConvertForm is how a single-owner deployment becomes an accounts deployment
// from the panel: the people, which account the existing history belongs
// to, and which one keeps signing in with the deployment's credentials, in
// one write. The legacy owner is proposed for both, since that is almost
// always who is filling this in. Everyone else's password is set from the
// list afterwards.
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
    { id: legacyTelegramId ? "owner" : legacyOwner || "owner", telegram_user_id: legacyTelegramId ?? 0 },
  ]);
  const [migrationOwner, setMigrationOwner] = useState(accounts[0].id);
  const [passwordAccount, setPasswordAccount] = useState(accounts[0].id);
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
      await convertToAccounts({ accounts, migration_owner_id: migrationOwner, password_account_id: passwordAccount });
      onConverted();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Conversion failed"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4">
      <p className="text-sm text-neutral-700">
        This Eggy has one owner. Converting gives each person their own account with a username and password. The
        existing conversations and memory go to the migration owner; everyone else starts empty. One account keeps
        signing in with the deployment&apos;s EGGY_UI_USER_EMAIL / EGGY_UI_PASSWORD; set the others&apos; passwords
        from the list afterwards.
      </p>
      <fieldset className={cn(panelClass, "flex flex-col gap-3")}>
        <legend className="text-[12.5px] font-medium">People</legend>
        {accounts.map((account, index) => (
          <div
            key={index}
            className={cn("grid gap-2 sm:grid-cols-2", index > 0 && "pt-3 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]")}
          >
            <Input aria-label={`Account ${index + 1} ID`} value={account.id} onChange={(e) => update(index, { id: e.target.value })} placeholder="username" required className={FIELD_COMPACT} />
            <Input
              aria-label={`Account ${index + 1} Telegram`}
              inputMode="numeric"
              value={account.telegram_user_id ? String(account.telegram_user_id) : ""}
              onChange={(e) => update(index, { telegram_user_id: e.target.value.trim() === "" ? 0 : Number(e.target.value) })}
              placeholder="telegram user id (optional)"
              className={FIELD_COMPACT}
            />
          </div>
        ))}
        <button type="button" onClick={() => setAccounts((current) => [...current, { id: "", telegram_user_id: 0 }])} className={cn(ghostButtonClass, "self-start")}>
          Add another person
        </button>
      </fieldset>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="convert-migration-owner" className={FIELD_LABEL}>Migration owner (receives the existing history)</Label>
        <select
          id="convert-migration-owner"
          value={migrationOwner}
          onChange={(e) => setMigrationOwner(e.target.value)}
          className="h-[42px] rounded-xl border border-neutral-200 bg-background px-3.5 text-[13px] text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30"
        >
          {accounts.map((account, index) => (
            <option key={index} value={account.id}>
              {account.id || `(account ${index + 1})`}
            </option>
          ))}
        </select>
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="convert-password-account" className={FIELD_LABEL}>Environment login (signs in with EGGY_UI_USER_EMAIL / EGGY_UI_PASSWORD)</Label>
        <select
          id="convert-password-account"
          value={passwordAccount}
          onChange={(e) => setPasswordAccount(e.target.value)}
          className="h-[42px] rounded-xl border border-neutral-200 bg-background px-3.5 text-[13px] text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30"
        >
          {accounts.map((account, index) => (
            <option key={index} value={account.id}>
              {account.id || `(account ${index + 1})`}
            </option>
          ))}
        </select>
      </div>
      {error && (
        <ErrorBanner>
          {error}
        </ErrorBanner>
      )}
      <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
        {saving ? "Converting..." : "Convert to accounts"}
      </button>
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
  const [changingPassword, setChangingPassword] = useState<AccountRow | null>(null);
  const [revoking, setRevoking] = useState<AccountRow | null>(null);
  // steps is whose onboarding checklist is open: set when an account is
  // added, or when someone clicks "Getting started" on a row. It is keyed by
  // id so a reload keeps showing the same person's current progress.
  const [steps, setSteps] = useState<string | null>(null);
  const [expected, setExpected] = useState("");
  const [seeded, setSeeded] = useState(false);

  const load = useCallback(() => {
    getAccounts()
      .then((loaded) => {
        setView(loaded);
        if (!seeded) {
          setExpected(loaded.expected_email);
          setSeeded(true);
        }
      })
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(errorMessage(err, "Failed to load accounts"));
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
      setError(errorMessage(err, "Request failed"));
      return false;
    } finally {
      setSaving(false);
    }
  }

  function show(which: "edit" | "remove" | "password" | "revoke" | "steps", account: AccountRow) {
    setEditing(which === "edit" ? account : null);
    setRemoving(which === "remove" ? account : null);
    setChangingPassword(which === "password" ? account : null);
    setRevoking(which === "revoke" ? account : null);
    setSteps(which === "steps" ? account.id : null);
  }

  const self = view?.accounts.find((account) => account.self);
  const stepsAccount = steps ? view?.accounts.find((account) => account.id === steps) : undefined;
  // A signed-in person whose Telegram is not linked yet is mid-onboarding,
  // and the checklist for their own account is the thing to show first.
  const selfNeedsTelegram = !!view?.telegram_enabled && !!self && !self.telegram_user_id;

  return (
    <div className="flex flex-col gap-5">
      <CardHeader
        title="People — trusted users who can administer this deployment"
        description={
          <>
            Each person signs in with their own username and password and has private conversations, memory,
            schedules and settings. There are no roles: everyone here can change everything, including each other&apos;s
            passwords.
          </>
        }
      />
      {error && (
        <ErrorBanner>
          {error}
        </ErrorBanner>
      )}
      {notice && <p className="rounded-xl bg-neutral-100 px-3 py-2 text-sm">{notice}</p>}

      {view && !view.account_mode ? (
        <ConvertForm
          legacyOwner={view.legacy_owner ?? ""}
          legacyTelegramId={view.legacy_telegram_id}
          onConverted={() => {
            setNotice("Converted. Restart Eggy, then set each new person's password from the list.");
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
            discordEnabled={view?.discord_enabled ?? false}
            discordLinkingAvailable={view?.discord_linking_available ?? false}
            onEdit={(account) => show("edit", account)}
            onRemove={(account) => show("remove", account)}
            onPassword={(account) => show("password", account)}
            onRevoke={(account) => show("revoke", account)}
            onShowSteps={(account) => show("steps", account)}
            onError={setError}
          />
          {view?.environment_alias && (
            <p className="text-xs text-neutral-700">
              <strong>{view.password_account_id}</strong> signs in as <code>{view.environment_alias}</code> with the password set
              in the deployment environment (EGGY_UI_PASSWORD). Changing it means changing the variable and restarting.
            </p>
          )}
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
          {changingPassword && (
            <PasswordForm
              key={changingPassword.id}
              account={changingPassword}
              saving={saving}
              onCancel={() => setChangingPassword(null)}
              onSubmit={async (password, current) => {
                const ok = await run(() => setAccountPassword(changingPassword.id, password, current));
                if (ok) {
                  setChangingPassword(null);
                  // Your own change ended your session; the next request
                  // finds it gone and returns you to the login page.
                  if (changingPassword.self) onSessionExpired();
                }
                return ok;
              }}
            />
          )}
          {revoking && (
            <RevokeConfirm
              account={revoking.id}
              self={revoking.self}
              onConfirm={async () => {
                if (await run(() => revokeAccountSessions(revoking.id))) {
                  setRevoking(null);
                  if (revoking.self) onSessionExpired();
                }
              }}
              onCancel={() => setRevoking(null)}
            />
          )}
          <div className={cn(panelClass, "flex flex-col gap-3")}>
            <p className="text-[12.5px] font-medium">{editing ? `Edit ${editing.id}` : "Add an account"}</p>
            {!editing && (
              <p className="text-xs text-neutral-700">
                Choose their username and a password, and hand the password over yourself. You&apos;ll get an invite to
                send them; the password is never in it.
              </p>
            )}
            {editing ? (
              <AccountForm
                key={editing.id}
                initial={editing}
                saving={saving}
                onCancel={() => setEditing(null)}
                onSubmit={async (input) => {
                  if (await run(() => editAccount(editing.id, { telegram_user_id: input.telegram_user_id }))) setEditing(null);
                }}
              />
            ) : (
              <AccountForm
                key={view?.accounts.length ?? 0}
                saving={saving}
                onSubmit={async (input) => {
                  setSaving(true);
                  setError(null);
                  setNotice(null);
                  try {
                    const result = await addAccount(input);
                    setNotice([result.title, result.detail].filter(Boolean).join(" "));
                    setSteps(input.id);
                  } catch (err) {
                    if (err instanceof SessionExpiredError) {
                      onSessionExpired();
                      return;
                    }
                    if (err instanceof PendingAccountError) {
                      // Membership exists, the password does not: the row
                      // is listed as pending with "Retry password setup".
                      setError(err.message);
                      setSteps(input.id);
                    } else {
                      setError(errorMessage(err, "Request failed"));
                    }
                  } finally {
                    setSaving(false);
                    load();
                  }
                }}
              />
            )}
          </div>
        </>
      )}

      <details className="rounded-2xl bg-neutral-100 p-4">
        <summary className="cursor-pointer text-[12.5px] font-medium">Telegram</summary>
        <div className="mt-3 flex flex-col gap-3">
          <p className="text-xs text-neutral-700">
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
            <p className="text-xs text-neutral-700">
              Pairing is unavailable until Eggy can discover the bot username. Check Telegram credentials and restart.
            </p>
          )}
        </div>
      </details>

      {view?.discord_enabled && (
        <p className="text-xs text-neutral-700">
          Discord is enabled: each person links their own Discord from their row above by sending the bot a single-use
          command in a DM. The bot itself is set up under Settings → Connections.
        </p>
      )}

      <details className="rounded-2xl bg-neutral-100 p-4">
        <summary className="cursor-pointer text-[12.5px] font-medium">Expected Google account</summary>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            run(() => setExpectedGoogleEmail(expected));
          }}
          className="mt-3 flex flex-col gap-3"
        >
          <p className="text-xs text-neutral-700">
            Eggy&apos;s own Google Workspace user. Only this account can be connected under Connections; anyone
            accidentally authorizing their personal account is refused.
          </p>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="expected-email" className={FIELD_LABEL}>Email</Label>
            <Input id="expected-email" type="email" value={expected} onChange={(e) => setExpected(e.target.value)} placeholder="eggy@yourdomain" className={FIELD_COMPACT} />
          </div>
          <button type="submit" disabled={saving} className={cn(PRIMARY_BUTTON, "self-start")}>
            {saving ? "Saving..." : "Save expected account"}
          </button>
        </form>
      </details>
    </div>
  );
}
