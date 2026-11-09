import { useEffect, useState } from "react";
import { getRawConfig, getStartupFailure, saveRawConfig, SessionExpiredError } from "../lib/api";
import { Button } from "../components/ui/button";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { errorMessage } from "../lib/utils";
import { CodeEditor } from "../components/ui/code-editor";

// Shown when eggyd could not start. There is no chat and no settings panel
// behind this screen -- the agent is not running -- so it offers the one thing
// that can bring Eggy back: the startup error, and the config.yaml that caused
// it. On a container deployment this is the only way in: config.yaml lives on
// a volume that only eggyd can reach.
//
// A save that Eggy accepts restarts it, which is why the reload is delayed
// rather than immediate: the new process needs a moment to bind the port.
export function SafeModePage({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [failure, setFailure] = useState<string | null>(null);
  const [config, setConfig] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [rejection, setRejection] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    Promise.all([getStartupFailure(), getRawConfig()])
      .then(([startup, body]) => {
        setFailure([startup.title, startup.detail].filter(Boolean).join("\n\n"));
        setConfig(body);
      })
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setFailure(errorMessage(err, "Eggy did not start."));
      })
      .finally(() => setLoading(false));
  }, [onSessionExpired]);

  async function handleSave() {
    setSaving(true);
    setRejection(null);
    try {
      await saveRawConfig(config);
      setSaved(true);
      window.setTimeout(() => window.location.reload(), 3000);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setRejection(errorMessage(err, "Eggy refused the config"));
      setSaving(false);
    }
  }

  return (
    <div className="app-canvas min-h-screen px-4 py-10">
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-6">
        <div className="flex items-center gap-3">
          <div className="flex h-12 w-12 items-center justify-center rounded-card bg-tomato-soft text-display">🥚</div>
          <div>
            <h1 className="text-title font-semibold tracking-tight">Eggy is in safe mode</h1>
            <p className="text-small text-neutral-700">Startup failed, so the agent is not running.</p>
          </div>
        </div>

        <section className="flex flex-col gap-3">
          <CardHeader title="Why it did not start" description="Fix the config below and save. Eggy retries startup as soon as it loads." />
          <pre className="overflow-x-auto whitespace-pre-wrap rounded-card bg-tomato-soft px-4 py-3 text-small text-tomato">
            {loading ? "Loading..." : failure}
          </pre>
        </section>

        <section className="flex flex-col gap-3">
          <CardHeader title="config.yaml" description="Saved only if it loads, so a second bad config cannot lock you out." />
          <CodeEditor language="yaml" label="config.yaml" value={config} onChange={setConfig} disabled={loading || saved} minHeight="26rem" />
          <div>
            <Button type="button" onClick={handleSave} disabled={loading || saving || saved} className="rounded-control">
              {saved ? "Restarting..." : saving ? "Checking..." : "Validate and save"}
            </Button>
          </div>
          {rejection && (
            <ErrorBanner preformatted>
              {rejection}
              {"\n\nThe stored config is unchanged."}
            </ErrorBanner>
          )}
          {saved && (
            <p className="rounded-card bg-accent-100 px-4 py-3 text-small text-accent-900" role="status">
              Config saved. Eggy is starting up again — this page reloads in a moment.
            </p>
          )}
        </section>
      </div>
    </div>
  );
}
