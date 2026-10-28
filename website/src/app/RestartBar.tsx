import { useState } from "react";
import { SessionExpiredError, restartEggy } from "../lib/api";
import { useConfirm } from "../components/ui/confirm";

// The strip under the top of the work area when config.yaml has changed since
// Eggy started. It names the one thing that is waiting and offers the one
// action that applies it, so an owner who edited three settings sees one
// restart instead of three "after restart" notes. After the restart the page
// reloads, which is the honest signal that the new settings are the ones in use.
export function RestartBar({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [confirm, confirmDialog] = useConfirm();
  const [restarting, setRestarting] = useState(false);

  async function restart() {
    if (!(await confirm({ title: "Restart Eggy now?", body: "Anything running finishes first. This page reconnects in a few seconds.", confirmLabel: "Restart" }))) return;
    setRestarting(true);
    try {
      await restartEggy();
      window.setTimeout(() => window.location.reload(), 4000);
    } catch (err) {
      setRestarting(false);
      if (err instanceof SessionExpiredError) onSessionExpired();
    }
  }

  return (
    <div role="status" className="flex flex-wrap items-center gap-3 bg-eg-ask px-4 py-2.5 text-sm text-eg-ask-ink sm:px-8">
      <span className="min-w-0 flex-1">Changes to your settings take effect after a restart.</span>
      <button
        type="button"
        onClick={restart}
        disabled={restarting}
        className="min-h-9 rounded-xl bg-primary px-3.5 text-sm font-semibold text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-60"
      >
        {restarting ? "Restarting…" : "Restart now"}
      </button>
      {confirmDialog}
    </div>
  );
}
