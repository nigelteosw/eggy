import { useCallback, useEffect, useState } from "react";
import { SessionExpiredError, clearDiscordToken, getDiscord, setDiscord, type DiscordView } from "./api";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { Switch } from "./components/ui/switch";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD_COMPACT, FIELD_LABEL, PRIMARY_BUTTON, SECONDARY_BUTTON } from "./components/ui/form";
import { SummaryRows } from "./components/ui/summary-rows";
import { errorMessage } from "./lib/utils";

// The Discord card is where the bot is added: the token is pasted here and
// sealed on the server, never written to config.yaml or the deployment
// environment, so an owner can add a bot without touching the host. Each
// person then links their own Discord from the Accounts card.

export function describeDiscord(view: DiscordView): string[] {
  const token = !view.bot_token_set ? "not set" : view.bot_token_source === "environment" ? "from environment" : "stored";
  const state = !view.enabled ? "disabled" : view.running ? "running" : "enabled (restart to start)";
  return [state, view.application_id || "—", token];
}

export function DiscordCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [view, setView] = useState<DiscordView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [applicationId, setApplicationId] = useState("");
  const [botToken, setBotToken] = useState("");

  const load = useCallback(() => {
    getDiscord()
      .then((loaded) => {
        setView(loaded);
        setEnabled(loaded.enabled);
        setApplicationId(loaded.application_id);
      })
      .catch((err) => {
        if (err instanceof SessionExpiredError) return onSessionExpired();
        setError(errorMessage(err, "Failed to load"));
      });
  }, [onSessionExpired]);

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
      setBotToken("");
      load();
    } catch (err) {
      if (err instanceof SessionExpiredError) return onSessionExpired();
      setError(errorMessage(err, "Failed to save"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="mb-2">
      <CardHeader title="Discord" className="mb-3" description="Talk to Eggy in a private Discord DM. Add the bot here; each person links their own Discord from Accounts." />

      {view && <SummaryRows headers={["State", "Application ID", "Bot token"]} row={describeDiscord(view)} />}

      <details className="rounded-2xl bg-neutral-100 p-4">
        <summary className="cursor-pointer text-[12.5px] font-medium">Configure Discord</summary>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            run(() => setDiscord({ enabled, application_id: applicationId, bot_token: botToken }));
          }}
          className="mt-3 flex flex-col gap-4"
        >
          <Switch checked={enabled} onCheckedChange={setEnabled} label={enabled ? "Discord on" : "Discord off"} />
          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="discord-token" className={FIELD_LABEL}>
                Bot token
              </Label>
              <Input
                id="discord-token"
                type="password"
                autoComplete="off"
                placeholder={view?.bot_token_set ? "Set — paste to replace" : "Paste the bot token"}
                value={botToken}
                onChange={(e) => setBotToken(e.target.value)}
                className={FIELD_COMPACT}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="discord-application" className={FIELD_LABEL}>
                Application ID (optional)
              </Label>
              <Input id="discord-application" inputMode="numeric" placeholder="From the Developer Portal" value={applicationId} onChange={(e) => setApplicationId(e.target.value)} className={FIELD_COMPACT} />
            </div>
          </div>
          <p className="text-xs text-neutral-700">
            Create a bot in the Discord Developer Portal, copy its token, and invite it to any server you share with it (DMs need no
            server permissions). The token is stored encrypted on the server and never shown again. Message content intent is not required.
          </p>
          <div className="flex flex-wrap gap-2 pt-1">
            <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
              {saving ? "Saving..." : "Save Discord"}
            </button>
            {view?.bot_token_source === "stored" && (
              <button type="button" disabled={saving} onClick={() => run(clearDiscordToken)} className={SECONDARY_BUTTON}>
                Remove stored token
              </button>
            )}
          </div>
        </form>
      </details>

      {notice && <p className="mt-2 text-xs text-neutral-700">{notice}</p>}
      {error && <ErrorBanner className="mt-3">{error}</ErrorBanner>}
    </div>
  );
}
