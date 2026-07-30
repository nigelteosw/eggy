import { useEffect, useState } from "react";
import { getRawConfig, getStartupFailure, saveRawConfig, SessionExpiredError } from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";

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
        setFailure(err instanceof Error ? err.message : "Eggy did not start.");
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
      setRejection(err instanceof Error ? err.message : "Eggy refused the config");
      setSaving(false);
    }
  }

  return (
    <div className="min-h-screen bg-background px-4 py-10">
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-5">
        <div className="flex items-center gap-3">
          <div className="flex h-11 w-11 items-center justify-center rounded-2xl bg-destructive/10 text-2xl">🥚</div>
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Eggy is in safe mode</h1>
            <p className="text-sm text-muted-foreground">Startup failed, so the agent is not running.</p>
          </div>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Why it did not start</CardTitle>
            <CardDescription>Fix the config below and save. Eggy retries startup as soon as it loads.</CardDescription>
          </CardHeader>
          <CardContent>
            <pre className="overflow-x-auto whitespace-pre-wrap rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {loading ? "Loading..." : failure}
            </pre>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>config.yaml</CardTitle>
            <CardDescription>Saved only if it loads, so a second bad config cannot lock you out.</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <textarea
              spellCheck={false}
              value={config}
              onChange={(event) => setConfig(event.target.value)}
              disabled={loading || saved}
              className="min-h-[26rem] w-full whitespace-pre rounded-md border border-border bg-background px-3 py-2 font-mono text-[13px] leading-relaxed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            />
            <div>
              <Button type="button" onClick={handleSave} disabled={loading || saving || saved}>
                {saved ? "Restarting..." : saving ? "Checking..." : "Validate and save"}
              </Button>
            </div>
            {rejection && (
              <pre
                className="overflow-x-auto whitespace-pre-wrap rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
                role="alert"
              >
                {rejection}
                {"\n\nThe stored config is unchanged."}
              </pre>
            )}
            {saved && (
              <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">
                Config saved. Eggy is starting up again — this page reloads in a moment.
              </p>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
