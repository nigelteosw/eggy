import { useEffect, useRef, useState } from "react";
import { AgentSelection, SessionExpiredError, getAgent, setAgentEffort, setAgentModel, setApprovalMode } from "./api";
import { ArrowUpIcon, ChevronDownIcon } from "./components/ui/icons";
import { cn } from "./lib/utils";

// The three approval modes as the composer says them: a short label for the
// chip, and the full sentence as its tooltip. The sentences are the ones
// internal/commands.ModeMessage sends to Telegram, so the two surfaces do not
// describe the same setting differently.
const APPROVAL_LABELS: Record<string, string> = {
  strict: "Ask always",
  normal: "Ask to write",
  auto: "Full access",
};

const APPROVAL_TITLES: Record<string, string> = {
  strict: "Strict mode. Every tool call asks first, reading included.",
  normal: "Normal mode. Reading runs freely; anything that writes asks first.",
  auto: "Auto mode. Nothing asks — tool calls that change things now run unapproved.",
};

const APPROVAL_MODES = ["strict", "normal", "auto"];

function SettingSelect({
  value,
  options,
  labelFor,
  onChange,
  title,
  name,
  placeholder,
  busy,
}: {
  value: string;
  options: string[];
  labelFor?: (option: string) => string;
  onChange: (next: string) => void;
  title?: string;
  name: string;
  placeholder?: string;
  busy?: boolean;
}) {
  const label = value ? (labelFor ? labelFor(value) : value) : (placeholder ?? "");
  const pickable = options.length > 1 || (!value && options.length > 0);
  return (
    <div
      title={title}
      className={cn(
        "relative flex min-h-11 min-w-0 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground transition-colors",
        pickable && "cursor-pointer hover:bg-muted hover:text-foreground",
        busy && "opacity-60",
      )}
    >
      <span>{name}</span>
      <span className={cn("max-w-[8rem] truncate font-medium", value ? "text-foreground" : "text-muted-foreground")}>{label}</span>
      {pickable && <ChevronDownIcon className="h-3 w-3 shrink-0" />}
      {pickable && (
        <select
          aria-label={name}
          value={value}
          disabled={busy}
          onChange={(event) => onChange(event.target.value)}
          className="absolute inset-0 cursor-pointer opacity-0"
        >
          {!value && (
            <option value="" disabled>
              {placeholder ?? ""}
            </option>
          )}
          {options.map((option) => (
            <option key={option} value={option}>
              {labelFor ? labelFor(option) : option}
            </option>
          ))}
        </select>
      )}
    </div>
  );
}
export function Composer({
  onSend,
  onSessionExpired,
}: {
  onSend: (text: string) => void;
  onSessionExpired: () => void;
}) {
  const [draft, setDraft] = useState("");
  const [agent, setAgent] = useState<AgentSelection | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const field = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    getAgent()
      .then(setAgent)
      .catch((err) => {
        if (err instanceof SessionExpiredError) onSessionExpired();
      });
  }, [onSessionExpired]);

  async function change(write: () => Promise<AgentSelection>) {
    setBusy(true);
    setNote(null);
    try {
      setAgent(await write());
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setNote(err instanceof Error ? err.message : "Could not change that setting");
    } finally {
      setBusy(false);
    }
  }

  async function changeApproval(mode: string) {
    setBusy(true);
    setNote(null);
    try {
      await setApprovalMode(mode);
      setAgent(await getAgent());
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setNote(err instanceof Error ? err.message : "Could not change the approval mode");
    } finally {
      setBusy(false);
    }
  }

  function submit(event: React.SyntheticEvent) {
    event.preventDefault();
    const text = draft.trim();
    if (!text) return;
    setDraft("");
    if (field.current) field.current.style.height = "auto";
    onSend(text);
  }

  return (
    <form onSubmit={submit} className="composer-dock shrink-0 px-4 pb-[max(1.25rem,env(safe-area-inset-bottom))] pt-5 sm:px-8 sm:pb-7">
      <div className="mx-auto w-full max-w-4xl">
        {note && (
          <p className="mb-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive" role="alert">
            {note}
          </p>
        )}
        <div
          className={cn(
            "rounded-xl border bg-card transition-colors",
            "focus-within:border-primary",
          )}
        >
          <textarea
            ref={field}
            value={draft}
            rows={1}
            onChange={(event) => setDraft(event.target.value)}
            onInput={(event) => {
              const box = event.currentTarget;
              box.style.height = "auto";
              box.style.height = `${Math.min(box.scrollHeight, 220)}px`;
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                submit(event);
              }
            }}
            placeholder="Ask Eggy anything..."
            className="scrollbar-slim max-h-[220px] w-full resize-none bg-transparent px-4 pb-3 pt-4 text-[0.9375rem] leading-relaxed outline-none placeholder:text-muted-foreground/70"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-1 border-t px-2.5 py-2">
            <span className="px-1.5 text-xs font-medium text-muted-foreground">Run settings</span>
            {agent && agent.models.length > 0 && (
              <SettingSelect
                name="Model"
                value={agent.model}
                options={agent.models}
                busy={busy}
                title="Which model runs the next turn"
                onChange={(model) => change(() => setAgentModel(model))}
              />
            )}
            {agent && agent.efforts.length > 0 && (
                <SettingSelect
                  name="Effort"
                  value={agent.effort}
                  options={agent.efforts}
                  placeholder="Effort"
                  busy={busy}
                  title="How hard the model thinks before answering"
                  onChange={(effort) => change(() => setAgentEffort(effort))}
                />
            )}
            {agent?.approval_mode && (
                <SettingSelect
                  name="Access"
                  value={agent.approval_mode}
                  options={APPROVAL_MODES}
                  labelFor={(mode) => APPROVAL_LABELS[mode] ?? mode}
                  busy={busy}
                  title={APPROVAL_TITLES[agent.approval_mode]}
                  onChange={changeApproval}
                />
            )}
            <button
              type="submit"
              disabled={!draft.trim()}
              aria-label="Send message"
              className={cn(
                "ml-auto flex h-11 w-11 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground transition-opacity",
                "hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:opacity-30",
              )}
            >
              <ArrowUpIcon className="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>
    </form>
  );
}
