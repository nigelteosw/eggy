import { useCallback, useEffect, useState } from "react";
import { SessionExpiredError, getAgent, setAgentEffort, setAgentModel, setAgentThinking, setApprovalMode, type AgentSelection } from "./api";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { Label } from "./components/ui/label";
import { Switch } from "./components/ui/switch";
import { FIELD_LABEL } from "./components/ui/form";
import { errorMessage } from "./lib/utils";

// PersonalSettingsCard is the signed-in person's own runtime settings: the
// model and effort behind /model, thinking visibility, and the approval
// mode behind /mode. Every control here reads and writes the same
// per-account state the chat commands do, so a choice made in Telegram is
// what the panel shows and vice versa -- and none of it is anyone else's.
// Shared deployment settings (providers, connections, tracing) live under
// their own heading and are deliberately not mixed in here.

const selectClass =
  "h-[42px] rounded-xl border border-neutral-200 bg-background px-3.5 text-[13px] text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30";

export const APPROVAL_MODES: { value: string; label: string; hint: string }[] = [
  { value: "strict", label: "Strict", hint: "Every tool call asks you first." },
  { value: "normal", label: "Normal", hint: "Only actions that write or reach outside ask; reads run." },
  { value: "auto", label: "Auto", hint: "Nothing asks. Your choice alone; it is never the default." },
];

export function PersonalSettingsCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [selection, setSelection] = useState<AgentSelection | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const fail = useCallback(
    (err: unknown, fallback: string) => {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, fallback));
    },
    [onSessionExpired],
  );

  useEffect(() => {
    getAgent()
      .then(setSelection)
      .catch((err) => fail(err, "Could not load your settings"));
  }, [fail]);

  async function apply(action: () => Promise<AgentSelection>) {
    setBusy(true);
    setError(null);
    try {
      setSelection(await action());
    } catch (err) {
      fail(err, "Could not save");
    } finally {
      setBusy(false);
    }
  }

  async function chooseMode(mode: string) {
    setBusy(true);
    setError(null);
    try {
      await setApprovalMode(mode);
      setSelection(await getAgent());
    } catch (err) {
      fail(err, "Could not change the approval mode");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <CardHeader
        title="My settings — applies to your Telegram and web sessions"
        description="These are yours alone. Nobody else's choices change them, and yours change nobody else's. The same settings answer /model and /mode in Telegram."
      />
      {error && <ErrorBanner>{error}</ErrorBanner>}
      {selection && (
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="personal-model" className={FIELD_LABEL}>Model</Label>
            <select
              id="personal-model"
              aria-label="Model"
              value={selection.model}
              disabled={busy}
              onChange={(event) => apply(() => setAgentModel(event.target.value))}
              className={selectClass}
            >
              {selection.models.map((alias) => (
                <option key={alias} value={alias}>{alias}</option>
              ))}
            </select>
            <button type="button" disabled={busy} onClick={() => apply(() => setAgentModel("default"))} className="self-start text-xs text-neutral-700 underline-offset-4 hover:underline">
              Use the deployment default
            </button>
          </div>
          {selection.efforts.length > 0 && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="personal-effort" className={FIELD_LABEL}>Reasoning effort</Label>
              <select
                id="personal-effort"
                aria-label="Reasoning effort"
                value={selection.effort}
                disabled={busy}
                onChange={(event) => apply(() => setAgentEffort(event.target.value))}
                className={selectClass}
              >
                {selection.efforts.map((effort) => (
                  <option key={effort} value={effort}>{effort}</option>
                ))}
              </select>
            </div>
          )}
          <div className="flex flex-col gap-1 rounded-2xl bg-neutral-100 p-4">
            <Switch
              label="Show thinking"
              checked={selection.show_thinking !== false}
              disabled={busy}
              onCheckedChange={(checked) => apply(() => setAgentThinking(checked))}
            />
            <p className="text-xs text-neutral-700">Deliver the model&apos;s reasoning as a separate message before its reply.</p>
          </div>
          <fieldset className="flex flex-col gap-2">
            <legend className={FIELD_LABEL}>Approval mode</legend>
            {APPROVAL_MODES.map((mode) => (
              <label key={mode.value} className="flex cursor-pointer items-start gap-3 rounded-2xl bg-neutral-100 p-3.5 text-sm">
                <input
                  type="radio"
                  name="personal-approval-mode"
                  value={mode.value}
                  checked={selection.approval_mode === mode.value}
                  disabled={busy}
                  onChange={() => chooseMode(mode.value)}
                  className="mt-1"
                />
                <span>
                  <span className="font-medium">{mode.label}</span>
                  <span className="block text-xs text-neutral-700">{mode.hint}</span>
                </span>
              </label>
            ))}
          </fieldset>
        </div>
      )}
    </div>
  );
}
