import { useEffect, useState } from "react";
import { SessionExpiredError, getRawConfig, saveRawConfig } from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";

export function AdvancedCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [config, setConfig] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [rejection, setRejection] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    getRawConfig()
      .then(setConfig)
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setRejection(err instanceof Error ? err.message : "Could not read config.yaml");
      })
      .finally(() => setLoading(false));
  }, [onSessionExpired]);

  async function handleSave() {
    setSaving(true);
    setRejection(null);
    setSaved(false);
    try {
      await saveRawConfig(config);
      setSaved(true);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setRejection(err instanceof Error ? err.message : "Eggy refused the config");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>config.yaml</CardTitle>
        <CardDescription>
          Everything the forms above cover, plus the settings they do not. Saved only if Eggy can load it.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <details className="rounded-md border p-3">
          <summary className="cursor-pointer text-sm font-medium">Edit config.yaml</summary>
          <div className="mt-4 flex flex-col gap-3">
            <textarea
              spellCheck={false}
              value={config}
              onChange={(event) => {
                setConfig(event.target.value);
                setSaved(false);
              }}
              disabled={loading}
              className="min-h-[26rem] w-full whitespace-pre rounded-md border bg-background px-3 py-2 font-mono text-[13px] leading-relaxed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            />
            <div><Button type="button" onClick={handleSave} disabled={loading || saving}>{saving ? "Checking..." : "Validate and save"}</Button></div>
          </div>
        </details>
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
            Saved. Restart Eggy for it to take effect — the button below, or /restart in chat.
          </p>
        )}
      </CardContent>
    </Card>
  );
}
