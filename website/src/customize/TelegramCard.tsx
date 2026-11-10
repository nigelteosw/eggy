import { useCallback, useEffect, useState } from "react";
import { SessionExpiredError, getAccounts, type AccountsView } from "../lib/api";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { TelegramEnableControl } from "../settings/AccountsCard";
import { errorMessage } from "../lib/utils";

// Telegram as a channel: whether this Eggy answers there at all. Linking a
// person's own Telegram stays on their row under People, because it binds one
// sender to one account.
export function TelegramCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [view, setView] = useState<AccountsView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(() => {
    getAccounts()
      .then(setView)
      .catch((err) => (err instanceof SessionExpiredError ? onSessionExpired() : setError(errorMessage(err, "Could not load Telegram"))));
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <div className="flex flex-col gap-3">
      <CardHeader
        title="Telegram"
        description="Talk to Eggy from Telegram. Once it's on, each person links their own Telegram from Settings → People with a single-use link; nobody can link on someone else's behalf."
      />
      <div className="rounded-card bg-neutral-100 p-4">
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
          <p className="mt-2 text-caption text-neutral-700">Linking is unavailable until Eggy can discover the bot's username. Check the Telegram credentials and restart.</p>
        )}
      </div>
      {notice && <p className="text-caption text-neutral-700">{notice}</p>}
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </div>
  );
}
