import { useEffect, useState } from "react";
import { SessionExpiredError, getAgent, setAgentHeartbeat } from "../lib/api";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { Switch } from "../components/ui/switch";
import { errorMessage } from "../lib/utils";

// The person's own half of check-ins: whether Eggy checks in on them at all.
// It sits beside the watch list and the cadence, so check-ins are one place,
// and it is the same switch /heartbeat flips in Telegram.
export function CheckinsToggleCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [on, setOn] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    getAgent()
      .then((selection) => setOn(selection.heartbeat === true))
      .catch((err) => (err instanceof SessionExpiredError ? onSessionExpired() : setError(errorMessage(err, "Could not load your check-ins"))));
  }, [onSessionExpired]);

  async function change(next: boolean) {
    setBusy(true);
    setError(null);
    try {
      setOn((await setAgentHeartbeat(next)).heartbeat === true);
    } catch (err) {
      if (err instanceof SessionExpiredError) return onSessionExpired();
      setError(errorMessage(err, "Could not change your check-ins"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <CardHeader title="Your check-ins" description="When they're on, Eggy looks at your watch list and messages you on Telegram only when something needs you. Off until you turn them on." />
      {on !== null && (
        <div className="rounded-card bg-neutral-100 px-4 py-1">
          <Switch label="Check in on me" checked={on} disabled={busy} onCheckedChange={change} />
        </div>
      )}
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </div>
  );
}
