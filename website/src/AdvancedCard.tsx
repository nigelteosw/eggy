import { useEffect, useState } from "react";
import { SessionExpiredError, getRawConfig, saveRawConfig } from "./api";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { errorMessage } from "./lib/utils";

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
      <details className="rounded-2xl bg-neutral-100 p-4">
        <summary className="cursor-pointer text-[12.5px] font-medium">Edit config.yaml</summary>
        <div className="mt-3 flex flex-col gap-3">
          <textarea
            spellCheck={false}
            value={config}
            onChange={(event) => {
              setConfig(event.target.value);
              setSaved(false);
            }}
            disabled={loading}
            className="min-h-[26rem] w-full whitespace-pre rounded-2xl bg-background px-4 py-3.5 font-mono text-[12.5px] leading-relaxed text-foreground shadow-[inset_0_0_0_1px_hsl(var(--neutral-200))] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30"
          />
          <div>
            <button
              type="button"
              onClick={handleSave}
              disabled={loading || saving}
              className="min-h-10 whitespace-nowrap rounded-xl bg-primary px-4 text-[13.5px] font-semibold text-primary-foreground transition-opacity hover:opacity-90 disabled:pointer-events-none disabled:opacity-50"
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
        <p className="mt-3 rounded-xl bg-neutral-100 px-3 py-2 text-sm text-neutral-700" role="status">
          Saved. Restart Eggy for it to take effect — the button below, or /restart in chat.
        </p>
      )}
    </div>
  );
}
