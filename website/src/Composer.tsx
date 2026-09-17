import { useEffect, useRef, useState } from "react";
import { AgentSelection, SessionExpiredError, getAgent, setAgentEffort, setAgentModel, setApprovalMode } from "./api";
import { ArrowUpIcon, ChevronDownIcon, CloseIcon } from "./components/ui/icons";
import { cn, errorMessage } from "./lib/utils";

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
        "relative flex min-h-9 min-w-0 items-center gap-1.5 rounded-xl px-2.5 text-xs text-muted-foreground transition-colors",
        pickable && "cursor-pointer hover:bg-background hover:text-foreground",
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
// A selection the user chose to reply to, and whose message it came from.
export type Quote = { text: string; ownMessage: boolean };

export function Composer({
  onSend,
  onSessionExpired,
  quote,
  onClearQuote,
}: {
  onSend: (text: string, quote: Quote | null) => void;
  onSessionExpired: () => void;
  quote: Quote | null;
  onClearQuote: () => void;
}) {
  const [draft, setDraft] = useState("");
  const [agent, setAgent] = useState<AgentSelection | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const field = useRef<HTMLTextAreaElement | null>(null);

  // A fresh quote means the user is mid-thought; bring them to the field.
  useEffect(() => {
    if (quote) field.current?.focus();
  }, [quote]);

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
      setNote(errorMessage(err, "Could not change that setting"));
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
      setNote(errorMessage(err, "Could not change the approval mode"));
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
    onSend(text, quote);
    onClearQuote();
  }

  return (
    <form onSubmit={submit} className="composer-dock shrink-0 px-4 pb-[max(1.25rem,env(safe-area-inset-bottom))] pt-5 sm:px-8 sm:pb-7">
      <div className="mx-auto w-full max-w-4xl">
        {note && (
          <p className="mb-2 rounded-xl bg-destructive/10 px-3 py-2 text-xs text-destructive" role="alert">
            {note}
          </p>
        )}
        <div className="rounded-3xl bg-neutral-100 shadow-sm transition-colors focus-within:ring-2 focus-within:ring-ring/30">
          {quote && (
            <div className="px-3 pt-3">
              <div className="relative inline-flex max-w-[16rem] items-start rounded-2xl bg-background px-3 py-2.5 pr-8 shadow-sm">
                <span aria-hidden="true" className="mr-2.5 mt-0.5 w-0.5 shrink-0 self-stretch rounded-full bg-muted-foreground/50" />
                <p className="line-clamp-6 whitespace-pre-line break-words text-xs leading-5 text-foreground/85">{quote.text}</p>
                <button
                  type="button"
                  aria-label="Remove quoted text"
                  onClick={onClearQuote}
                  className="absolute right-1.5 top-1.5 flex h-5 w-5 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-neutral-100 hover:text-foreground"
                >
                  <CloseIcon className="h-3 w-3" />
                </button>
              </div>
            </div>
          )}
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
            placeholder={quote ? "Reply to the quoted text..." : "Ask Eggy anything..."}
            className="scrollbar-slim max-h-[220px] w-full resize-none bg-transparent px-[18px] pb-2 pt-4 text-[0.9375rem] leading-relaxed outline-none placeholder:text-muted-foreground/70"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-1 px-2 pb-2 pt-1 shadow-[inset_0_1px_0_hsl(var(--border))]">
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
                "ml-auto flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity",
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
