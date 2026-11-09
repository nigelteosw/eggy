import { useEffect, useState } from "react";
import { SessionExpiredError, getRawConfig, saveRawConfig } from "../lib/api";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { errorMessage } from "../lib/utils";
import { CodeEditor } from "../components/ui/code-editor";

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
        setRejection(errorMessage(err, "Could not read config.yaml"));
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
      setRejection(errorMessage(err, "Eggy refused the config"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="mb-2">
      <CardHeader title="config.yaml" className="mb-3" description="Everything the forms above cover, plus the settings they do not. Saved only if Eggy can load it." />
      <details className="rounded-card bg-neutral-100 p-4">
        <summary className="cursor-pointer text-small font-medium">Edit config.yaml</summary>
        <div className="mt-3 flex flex-col gap-3">
          <CodeEditor
            language="yaml"
            label="config.yaml"
            value={config}
            onChange={(next) => {
              setConfig(next);
              setSaved(false);
            }}
            disabled={loading}
            minHeight="26rem"
          />
          <div>
            <button
              type="button"
              onClick={handleSave}
              disabled={loading || saving}
              className="min-h-10 whitespace-nowrap rounded-control bg-primary px-4 text-small font-semibold text-primary-foreground transition-opacity hover:opacity-90 disabled:pointer-events-none disabled:opacity-50"
            >
              {saving ? "Checking..." : "Validate and save"}
            </button>
          </div>
        </div>
      </details>
      {rejection && (
        <ErrorBanner preformatted className="mt-3">
          {rejection}
          {"\n\nThe stored config is unchanged."}
        </ErrorBanner>
      )}
      {saved && (
        <p className="mt-3 rounded-control bg-neutral-100 px-3 py-2 text-small text-neutral-700" role="status">
          Saved. Restart Eggy for it to take effect — the button below, or /restart in chat.
        </p>
      )}
    </div>
  );
}
