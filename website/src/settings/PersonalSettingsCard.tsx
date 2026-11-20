import { Select } from "../components/ui/select";
import { useCallback, useEffect, useState } from "react";
import { SessionExpiredError, getAgent, setAgentEffort, setAgentModel, setAgentThinking, setApprovalMode, type AgentSelection } from "../lib/api";
import { CardHeader } from "../components/ui/card-header";
import { ErrorBanner } from "../components/ui/error-banner";
import { Label } from "../components/ui/label";
import { Switch } from "../components/ui/switch";
import { FIELD_LABEL } from "../components/ui/form";
import { errorMessage } from "../lib/utils";
import { GitHubTokenField } from "./GitHubTokenField";

// PersonalSettingsCard is the signed-in person's own runtime settings: the
// model and effort behind /model, thinking visibility, and the approval
// mode behind /mode. Every control here reads and writes the same
// per-account state the chat commands do, so a choice made in Telegram is
// what the panel shows and vice versa -- and none of it is anyone else's.
// Shared deployment settings (providers, connections, tracing) live under
// their own heading and are deliberately not mixed in here.


// chip is how the composer names the mode in force: the mode's name alone
// ("Read") said nothing about what it does to someone who had not opened the
// menu, so the chip says the behaviour instead.
export const APPROVAL_MODES: { value: string; label: string; chip: string; hint: string }[] = [
  { value: "auto", label: "Auto", chip: "Never asks", hint: "I don't ask. I read, change and send things for you on my own." },
  { value: "normal", label: "Read", chip: "Asks before changes", hint: "I read and look things up on my own, and ask before I change or send anything." },
  { value: "strict", label: "Manual", chip: "Asks every time", hint: "I ask before every tool I use, even to read or look something up." },
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
      fail(err, "Could not change when Eggy asks");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <CardHeader
        title="Your model and when Eggy asks"
        description="These are yours alone; nobody else's choices change them. They're the same settings /model and /mode change in Telegram."
      />
      {error && <ErrorBanner>{error}</ErrorBanner>}
      {selection && (
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            {/* The way back to the default sits on the label's line, beside
                what it resets, rather than under the field where a finger's
                44 px row left it floating. */}
            <div className="flex items-center justify-between gap-3">
              <Label htmlFor="personal-model" className={FIELD_LABEL}>Model</Label>
              <button type="button" disabled={busy} onClick={() => apply(() => setAgentModel("default"))} className="text-caption text-neutral-700 underline-offset-4 hover:underline">
                Use the deployment default
              </button>
            </div>
            <Select
              id="personal-model"
              aria-label="Model"
              value={selection.model}
              disabled={busy}
              onChange={(event) => apply(() => setAgentModel(event.target.value))}
            >
              {selection.models.map((alias) => (
                <option key={alias} value={alias}>{alias}</option>
              ))}
            </Select>
          </div>
          {selection.efforts.length > 0 && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="personal-effort" className={FIELD_LABEL}>Reasoning effort</Label>
              <Select
                id="personal-effort"
                aria-label="Reasoning effort"
                value={selection.effort}
                disabled={busy}
                onChange={(event) => apply(() => setAgentEffort(event.target.value))}
              >
                {selection.efforts.map((effort) => (
                  <option key={effort} value={effort}>{effort}</option>
                ))}
              </Select>
            </div>
          )}
          <div className="flex flex-col gap-1 rounded-card bg-neutral-100 p-4">
            <Switch
              label="Show thinking"
              checked={selection.show_thinking !== false}
              disabled={busy}
              onCheckedChange={(checked) => apply(() => setAgentThinking(checked))}
            />
            <p className="text-caption text-neutral-700">Deliver the model&apos;s reasoning as a separate message before its reply.</p>
          </div>
          <fieldset className="flex flex-col gap-2">
            <legend className={FIELD_LABEL}>When Eggy asks</legend>
            {APPROVAL_MODES.map((mode) => (
              <label key={mode.value} className="flex cursor-pointer items-start gap-3 rounded-card bg-neutral-100 p-3.5 text-small">
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
                  <span className="block text-caption text-neutral-700">{selection.approval_summaries?.[mode.value] ?? mode.hint}</span>
                </span>
              </label>
            ))}
          </fieldset>
        </div>
      )}
      <GitHubTokenField onSessionExpired={onSessionExpired} />
    </div>
  );
}
