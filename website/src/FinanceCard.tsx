import { useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { PRIMARY_BUTTON } from "./components/ui/form";
import { Select } from "./components/ui/select";
import { Switch } from "./components/ui/switch";

// The presentational half, so the restart rule can be tested without a server.
//
// savedEnabled is what config.yaml says; running is what this process started
// with (from the session's feature list). A restart is needed exactly when the
// two differ, and an unsaved switch position is neither, so it claims nothing.
export function FinanceSettingsView({
  enabled,
  savedEnabled,
  currency,
  currencies,
  running,
  saving,
  error,
  onEnabledChange,
  onCurrencyChange,
  onSave,
}: {
  enabled: boolean;
  savedEnabled: boolean;
  currency: string;
  currencies: string[];
  running: boolean;
  saving: boolean;
  error: string | null;
  onEnabledChange: (enabled: boolean) => void;
  onCurrencyChange: (currency: string) => void;
  onSave: () => void;
}) {
  const needsRestart = savedEnabled !== running;
  return (
    <section className="flex flex-col gap-3">
      <CardHeader
        title="Finance"
        description="Log spending by telling Eggy, or by sending it a receipt photo, and see it in a Finance tab. Entries are private to each person's account. Turning this off hides the tab and the tool; entries are kept."
      />
      <form
        onSubmit={(event) => {
          event.preventDefault();
          onSave();
        }}
        className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4"
      >
        <Switch checked={enabled} onCheckedChange={onEnabledChange} label="Enable finance" />
        <label className="flex max-w-xs flex-col gap-1.5 text-[11.5px] text-neutral-700">
          Default currency
          <Select value={currency} onChange={(event) => onCurrencyChange(event.target.value)} aria-label="Default currency">
            {currencies.map((code) => (
              <option key={code} value={code}>
                {code}
              </option>
            ))}
          </Select>
        </label>
        <p className="text-xs text-neutral-700">Entries can still use any listed currency; this is what an entry takes when none is said.</p>
        <div>
          <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
            {saving ? "Saving..." : "Save"}
          </button>
        </div>
      </form>
      {needsRestart && (
        <p className="rounded-2xl bg-neutral-100 px-3.5 py-2.5 text-sm text-neutral-700" role="status">
          Restart to apply: finance is {running ? "on" : "off"} now and will be {savedEnabled ? "on" : "off"} after a restart.
        </p>
      )}
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </section>
  );
}

export function FinanceCard({ onSessionExpired, running }: { onSessionExpired: () => void; running: boolean }) {
  const { result, error, saving, save } = useConfigSection("finance", onSessionExpired);
  const [draftEnabled, setDraftEnabled] = useState<boolean | null>(null);
  const [draftCurrency, setDraftCurrency] = useState<string | null>(null);

  const fields = Object.fromEntries((result?.fields ?? []).map((field) => [field.label, field.value]));
  const savedEnabled = fields.enabled === "true";
  const currencies = (fields.currencies ?? "").split(",").filter(Boolean);

  async function handleSave() {
    const saved = await save({
      enabled: String(draftEnabled ?? savedEnabled),
      currency: draftCurrency ?? fields.currency ?? "",
    });
    if (saved) {
      setDraftEnabled(null);
      setDraftCurrency(null);
    }
  }

  return (
    <FinanceSettingsView
      enabled={draftEnabled ?? savedEnabled}
      savedEnabled={savedEnabled}
      currency={draftCurrency ?? fields.currency ?? ""}
      currencies={currencies}
      running={running}
      saving={saving}
      error={error}
      onEnabledChange={setDraftEnabled}
      onCurrencyChange={setDraftCurrency}
      onSave={handleSave}
    />
  );
}
