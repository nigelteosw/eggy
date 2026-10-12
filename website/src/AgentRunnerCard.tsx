import { useState } from "react";
import { useConfigSection } from "./useConfigSection";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD_LABEL, PRIMARY_BUTTON, FIELD } from "./components/ui/form";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";
import { AGENT_SKILLS, EXTENSION_COPY, needsRestart, skillsFromField, skillsToField } from "./lib/agent-runner";

// The presentational half, so the restart rule and the copy can be tested
// without a server. savedEnabled is what config.yaml says; running is what this
// process started with. An unsaved switch position claims nothing.
export function AgentRunnerSettingsView({
  enabled,
  savedEnabled,
  model,
  skills,
  running,
  saving,
  error,
  onEnabledChange,
  onModelChange,
  onSkillChange,
  onSave,
}: {
  enabled: boolean;
  savedEnabled: boolean;
  model: string;
  skills: Record<string, boolean>;
  running: boolean;
  saving: boolean;
  error: string | null;
  onEnabledChange: (enabled: boolean) => void;
  onModelChange: (model: string) => void;
  onSkillChange: (skill: string, on: boolean) => void;
  onSave: () => void;
}) {
  const restart = needsRestart(savedEnabled, running);
  return (
    <section className="flex flex-col gap-3">
      <CardHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span>Agent Runner</span>
            <span role="status" aria-label="Agent Runner status" className="rounded-full bg-neutral-100 px-2 py-0.5 text-[11px] font-medium text-neutral-700">
              {running ? "Running" : "Not running"}
            </span>
          </span>
        }
        description={`Eggy can change repositories you allow and open a pull request for review. ${EXTENSION_COPY}`}
      />
      <form
        onSubmit={(event) => {
          event.preventDefault();
          onSave();
        }}
        className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4"
      >
        <Switch checked={enabled} onCheckedChange={onEnabledChange} label="Enable Agent Runner" />
        <label className="flex max-w-md flex-col gap-1.5 text-[11.5px] text-neutral-700">
          Coding model
          <Input
            className={FIELD}
            value={model}
            onChange={(event) => onModelChange(event.target.value)}
            aria-label="Coding model"
            placeholder="a models: alias from config.yaml"
          />
        </label>
        <p className={FIELD_LABEL}>Coding runs always use this model, whatever you chose for chat.</p>
        <fieldset className="flex flex-col gap-2">
          <legend className={FIELD_LABEL}>Skills for coding runs</legend>
          {AGENT_SKILLS.map((skill) => (
            <Switch key={skill} checked={skills[skill] ?? true} onCheckedChange={(on) => onSkillChange(skill, on)} label={skill} />
          ))}
        </fieldset>
        <div>
          <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
            {saving ? "Saving..." : "Save"}
          </button>
        </div>
      </form>
      {restart && (
        <p className="rounded-2xl bg-neutral-100 px-3.5 py-2.5 text-sm text-neutral-700" role="status">
          Restart to apply: Agent Runner is {running ? "on" : "off"} now and will be {savedEnabled ? "on" : "off"} after a restart.
        </p>
      )}
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </section>
  );
}

export function AgentRunnerCard({ onSessionExpired, running }: { onSessionExpired: () => void; running: boolean }) {
  const { result, error, saving, save } = useConfigSection("agent_runner", onSessionExpired);
  const [draftEnabled, setDraftEnabled] = useState<boolean | null>(null);
  const [draftModel, setDraftModel] = useState<string | null>(null);
  const [draftSkills, setDraftSkills] = useState<Record<string, boolean> | null>(null);

  const fields = Object.fromEntries((result?.fields ?? []).map((field) => [field.label, field.value]));
  const savedEnabled = fields.enabled === "true";
  const savedSkills = skillsFromField(fields.skills ?? "");
  const skills = draftSkills ?? savedSkills;

  async function handleSave() {
    const saved = await save({
      enabled: String(draftEnabled ?? savedEnabled),
      model: draftModel ?? fields.model ?? "",
      skills: skillsToField(skills),
    });
    if (saved) {
      setDraftEnabled(null);
      setDraftModel(null);
      setDraftSkills(null);
    }
  }

  return (
    <AgentRunnerSettingsView
      enabled={draftEnabled ?? savedEnabled}
      savedEnabled={savedEnabled}
      model={draftModel ?? fields.model ?? ""}
      skills={skills}
      running={running}
      saving={saving}
      error={error}
      onEnabledChange={setDraftEnabled}
      onModelChange={setDraftModel}
      onSkillChange={(skill, on) => setDraftSkills({ ...skills, [skill]: on })}
      onSave={handleSave}
    />
  );
}
