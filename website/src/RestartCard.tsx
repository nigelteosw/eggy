import { useState } from "react";
import { SessionExpiredError, restartEggy } from "./api";
import { Button } from "./components/ui/button";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { errorMessage } from "./lib/utils";

// Every save in this panel ends with "restart Eggy for this to take effect",
// because adapters are built once at startup. This is that restart, and it is
// here rather than on each card for the same reason: one button applies
// whatever the file now says, whichever form wrote it.
//
// It is not a redeploy. eggyd supervises the daemon, so the process stays up
// and builds a new Eggy from config.yaml -- and refuses if that config would
// not load, which is why a button is safe to offer at all.
export function RestartCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [restarting, setRestarting] = useState(false);
  const [restarted, setRestarted] = useState(false);
  const [rejection, setRejection] = useState<string | null>(null);

  async function handleRestart() {
    if (!window.confirm("Restart Eggy to apply config.yaml?\n\nAnything running finishes first. This panel reconnects in a few seconds.")) {
      return;
    }
    setRestarting(true);
    setRejection(null);
    try {
      await restartEggy();
      setRestarted(true);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      // The reason a restart was refused is the config that would not have
      // started, which is what the owner edits against.
      setRejection(errorMessage(err, "Eggy refused to restart"));
    } finally {
      setRestarting(false);
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <CardHeader
        title="Restart"
        description={
          <>
            Rebuild Eggy around config.yaml as it now stands. Nothing is redeployed, and durable state is kept. A config
            Eggy cannot load is refused here rather than applied.
          </>
        }
      />
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-3.5">
          <Button type="button" variant="secondary" onClick={handleRestart} disabled={restarting}>
            {restarting ? "Restarting..." : "Restart Eggy"}
          </Button>
        </div>
        {rejection && (
          <ErrorBanner preformatted>
            {rejection}
            {"\n\nEggy is still running on the config it started with."}
          </ErrorBanner>
        )}
        {restarted && (
          <p className="flex flex-wrap items-center gap-2 rounded-2xl bg-neutral-100 px-3.5 py-2.5 text-sm text-neutral-700" role="status">
            Restarting. In-flight turns finish first, so give it a few seconds.
            <Button type="button" variant="ghost" size="sm" onClick={() => window.location.reload()}>
              Reload panel
            </Button>
          </p>
        )}
      </div>
    </section>
  );
}
