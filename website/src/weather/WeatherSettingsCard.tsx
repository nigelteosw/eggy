import { useCallback, useEffect, useState } from "react";
import { SessionExpiredError, clearWeatherKey, getWeatherSettings, setWeatherSettings, type WeatherSettings } from "../lib/api";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { FIELD_COMPACT, FIELD_LABEL, PRIMARY_BUTTON, SECONDARY_BUTTON } from "../components/ui/form";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { SaveBar } from "../components/ui/save-bar";
import { Select } from "../components/ui/select";
import { errorMessage } from "../lib/utils";

// Where Home's weather comes from. The area is shared by everyone; the API key
// is sealed on the server and never shown again. Both apply on the next
// refresh -- nothing here waits for a restart.
export function WeatherSettingsCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [view, setView] = useState<WeatherSettings | null>(null);
  const [area, setArea] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    getWeatherSettings()
      .then((loaded) => {
        setView(loaded);
        setArea(loaded.area);
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
      setApiKey("");
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
      <CardHeader
        title="Weather"
        className="mb-3"
        description="Live Singapore weather on Home, and for Eggy when it plans your day, from data.gov.sg. Everyone sees the same area. An API key is optional; it raises how often Eggy may ask."
      />
      <form
        onSubmit={(event) => {
          event.preventDefault();
          run(() => setWeatherSettings({ area, api_key: apiKey }));
        }}
        className="flex flex-col gap-4 rounded-card bg-neutral-100 p-4"
      >
        <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="weather-area" className={FIELD_LABEL}>
              Area
            </Label>
            <Select id="weather-area" value={area} onChange={(event) => setArea(event.target.value)} disabled={!view}>
              {(view?.areas ?? []).map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="weather-key" className={FIELD_LABEL}>
              data.gov.sg API key (optional)
            </Label>
            <Input
              id="weather-key"
              type="password"
              autoComplete="off"
              disabled={view !== null && !view.key_storable}
              placeholder={view?.key_set ? "Set — paste to replace" : "Paste an API key"}
              value={apiKey}
              onChange={(event) => setApiKey(event.target.value)}
              className={FIELD_COMPACT}
            />
          </div>
        </div>
        <p className="text-caption text-neutral-700">
          {view && !view.key_storable
            ? "Storing a key needs EGGY_ENCRYPTION_KEY on the server. Weather still works without one, at a lower rate limit."
            : "Get a key by signing in at data.gov.sg. It is stored encrypted on the server and never shown again. Changes apply on the next refresh."}
        </p>
        <div className="flex flex-wrap gap-2">
          <button type="submit" disabled={saving || !view} className={PRIMARY_BUTTON}>
            {saving ? "Saving..." : "Save weather"}
          </button>
          {view?.key_set && (
            <button type="button" disabled={saving} onClick={() => run(clearWeatherKey)} className={SECONDARY_BUTTON}>
              Remove API key
            </button>
          )}
        </div>
        <SaveBar label="Save weather" />
      </form>
      {notice && <p className="mt-2 text-caption text-neutral-700">{notice}</p>}
      {error && <ErrorBanner className="mt-3">{error}</ErrorBanner>}
    </div>
  );
}
