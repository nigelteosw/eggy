import { useEffect, useState } from "react";
import { SessionExpiredError, deleteAgentPAT, getAgentPAT, setAgentPAT, type AgentPAT } from "./api";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD, FIELD_LABEL, PRIMARY_BUTTON, SECONDARY_BUTTON } from "./components/ui/form";
import { Input } from "./components/ui/input";

// The presentational half. The token field is never prefilled: a saved value
// does not come back to the browser, so an empty box is the only honest state.
export function GitHubTokenView({
  status,
  draft,
  saving,
  error,
  onDraftChange,
  onSave,
  onDisconnect,
}: {
  status: AgentPAT;
  draft: string;
  saving: boolean;
  error: string | null;
  onDraftChange: (token: string) => void;
  onSave: () => void;
  onDisconnect: () => void;
}) {
  const access = Object.entries(status.repositories ?? {})
    .map(([name, push]) => `${name}: ${push ? "push access" : "no push access"}`)
    .join(", ");
  return (
    <section className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4">
      <h3 className="text-sm font-medium text-foreground">GitHub token for Agent Runner</h3>
      <p className={FIELD_LABEL}>
        A fine-grained token with Contents and Pull requests read and write, for the repositories you allow. Only you see it.
      </p>
      {status.connected ? (
        <p className="text-sm text-neutral-700" role="status">
          Connected as {status.login}. {access}
        </p>
      ) : (
        <p className="text-sm text-neutral-700" role="status">
          Not connected.
        </p>
      )}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          onSave();
        }}
        className="flex flex-col gap-2"
      >
        <label className="flex flex-col gap-1.5 text-[11.5px] text-neutral-700">
          {status.connected ? "Replace token" : "Token"}
          <Input
            className={FIELD}
            type="password"
            autoComplete="off"
            value={draft}
            onChange={(event) => onDraftChange(event.target.value)}
            aria-label="GitHub token"
          />
        </label>
        <div className="flex gap-2">
          <button type="submit" disabled={saving || draft.trim() === ""} className={PRIMARY_BUTTON}>
            {saving ? "Verifying..." : "Save token"}
          </button>
          {status.connected && (
            <button type="button" disabled={saving} onClick={onDisconnect} className={SECONDARY_BUTTON}>
              Disconnect
            </button>
          )}
        </div>
      </form>
      {error && <ErrorBanner>{error}</ErrorBanner>}
    </section>
  );
}

// GitHubTokenField is rendered only when the runner is on. When it is off the
// token route does not exist, and the field renders nothing at all.
export function GitHubTokenField({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [status, setStatus] = useState<AgentPAT | null>(null);
  const [absent, setAbsent] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    getAgentPAT()
      .then(setStatus)
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
        } else {
          setAbsent(true);
        }
      });
  }, [onSessionExpired]);

  async function run(action: () => Promise<AgentPAT>) {
    setSaving(true);
    setError(null);
    try {
      setStatus(await action());
      setDraft("");
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
      } else {
        setError(err instanceof Error ? err.message : "Could not save the token");
      }
    } finally {
      setSaving(false);
    }
  }

  if (absent || !status) {
    return null;
  }
  return (
    <GitHubTokenView
      status={status}
      draft={draft}
      saving={saving}
      error={error}
      onDraftChange={setDraft}
      onSave={() => run(() => setAgentPAT(draft.trim()))}
      onDisconnect={() => run(() => deleteAgentPAT())}
    />
  );
}
