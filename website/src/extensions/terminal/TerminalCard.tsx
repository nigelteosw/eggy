import { useState } from "react";
import { useConfigSection } from "../../settings/useConfigSection";
import { CardHeader } from "../../components/ui/card-header";
import { ErrorBanner } from "../../components/ui/error-banner";
import { PRIMARY_BUTTON } from "../../components/ui/form";
import { SaveBar } from "../../components/ui/save-bar";
import { Switch } from "../../components/ui/switch";

export const TERMINAL_COPY =
  "Terminals in the panel, for a shell, Claude Code or Codex. They run as a sandboxed user that can reach the internet over HTTPS but not Eggy's data, and they cannot push. Each person's terminals are their own. They keep running when you close the page or Eggy restarts, and stop when Eggy is redeployed.";

export function TerminalSettingsView({
  enabled,
  running,
  saving,
  error,
  onEnabledChange,
  onSave,
}: {
  enabled: boolean;
  running: boolean;
  saving: boolean;
  error: string | null;
  onEnabledChange: (enabled: boolean) => void;
  onSave: () => void;
}) {
  return (
    <section className="flex flex-col gap-3">
      <CardHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span>Terminal</span>
            <span role="status" aria-label="Terminal extension status" className="rounded-full bg-neutral-100 px-2 py-0.5 text-caption font-medium text-neutral-700">
              {running ? "Running" : "Not running"}
            </span>
          </span>
        }
        description={TERMINAL_COPY}
      />
      <form
        onSubmit={(event) => {
          event.preventDefault();
          onSave();
        }}
        className="flex flex-col gap-3 rounded-card bg-neutral-100 p-4"
      >
        <Switch checked={enabled} onCheckedChange={onEnabledChange} label="Enable terminals" />
        <p className="text-caption text-neutral-700">It needs the same sandbox check as the Agent Runner's bash. If that check fails, the terminal stays off and the server log says why.</p>
        <div>
          <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
            {saving ? "Saving..." : "Save"}
          </button>
        </div>
        <SaveBar label="Save terminal" />
      </form>
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </section>
  );
}

export function TerminalCard({ onSessionExpired, running }: { onSessionExpired: () => void; running: boolean }) {
  const { result, error, saving, save } = useConfigSection("terminal", onSessionExpired);
  const [draft, setDraft] = useState<boolean | null>(null);
  const saved = (result?.fields ?? []).find((field) => field.label === "enabled")?.value === "true";
  return (
    <TerminalSettingsView
      enabled={draft ?? saved}
      running={running}
      saving={saving}
      error={error}
      onEnabledChange={setDraft}
      onSave={async () => {
        if (await save({ enabled: String(draft ?? saved) })) setDraft(null);
      }}
    />
  );
}
