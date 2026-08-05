import { useEffect, useRef, useState, type ReactNode } from "react";
import { AgentSelection, SessionExpiredError, getAgent, setAgentEffort, setAgentModel, setApprovalMode } from "./api";
import { ArrowUpIcon, ChevronDownIcon, CpuIcon, GaugeIcon, LockIcon } from "./components/ui/icons";
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

/**
 * One control in the composer's bottom row.
 *
 * The chip is drawn, and a native <select> sits transparently on top of it: on
 * a phone that means the platform picker, which is the one this panel is most
 * often used from. With a single option there is nothing to pick, so the chip
 * renders as a plain label -- that is what "dynamic in the number of models"
 * means here, an owner with one model configured sees a statement rather than
 * a menu that can only re-choose what is already chosen.
 */
function ChipSelect({
  icon,
  value,
  options,
  labelFor,
  onChange,
  title,
  name,
  placeholder,
  busy,
}: {
  icon: ReactNode;
  value: string;
  options: string[];
  labelFor?: (option: string) => string;
  onChange: (next: string) => void;
  title?: string;
  name: string;
  // What to show when nothing is selected. Only the effort chip can be in
  // that state, and it must not name a level instead: an unset effort means
  // the request carries no reasoning_effort at all, so showing the lowest
  // level would claim a setting the turn does not send.
  placeholder?: string;
  busy?: boolean;
}) {
  const label = value ? (labelFor ? labelFor(value) : value) : (placeholder ?? "");
  const pickable = options.length > 1 || (!value && options.length > 0);
  return (
    <div
      title={title}
      className={cn(
        "relative flex shrink-0 items-center gap-1.5 rounded-lg px-2 py-1.5 text-xs text-muted-foreground transition-colors",
        pickable && "cursor-pointer hover:bg-muted hover:text-foreground",
        busy && "opacity-60",
      )}
    >
      <span className="shrink-0 [&>svg]:h-3.5 [&>svg]:w-3.5">{icon}</span>
      <span className={cn("max-w-[9rem] truncate font-medium", value ? "text-foreground/80" : "text-muted-foreground")}>{label}</span>
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

function Divider() {
  return <span aria-hidden="true" className="h-4 w-px shrink-0 bg-border" />;
}

/**
 * The prompt box: a raised card floating over the transcript, with the model
 * configuration on its own row beneath the text.
 *
 * Those controls are the same ones /model and /mode write, read from the
 * server on mount and re-read from every write's response. They are not local
 * state mirrored back: switching models can invalidate the stored reasoning
 * effort, and the runtime is the only thing that knows when it did.
 */
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
      // A daemon with no model runtime answers 404 here. That is not an error
      // worth showing above the box the owner is typing in -- it means there
      // are no controls to draw, and the composer still sends.
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
      // The mode route answers in the command-result shape rather than with a
      // selection, so the composer re-reads the one it renders from instead of
      // assuming the write landed as asked.
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
    // The grown height is an inline style, so clearing the value does not undo
    // it -- the box would stay several lines tall over a one-line draft.
    if (field.current) field.current.style.height = "auto";
    onSend(text);
  }

  return (
    <form onSubmit={submit} className="shrink-0 px-4 pb-4 pt-2 sm:px-6">
      <div className="mx-auto w-full max-w-3xl">
        {note && (
          <p className="mb-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive" role="alert">
            {note}
          </p>
        )}
        {/* The accent ring is the whole point of the card: it lifts the one
            thing on the screen the owner acts on off a transcript that is
            otherwise the same two greys top to bottom. */}
        <div
          className={cn(
            "rounded-2xl border border-primary/50 bg-card shadow-lift transition-shadow",
            "focus-within:border-primary focus-within:ring-2 focus-within:ring-primary/25",
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
            className="scrollbar-slim max-h-[220px] w-full resize-none bg-transparent px-4 pb-2 pt-3.5 text-sm leading-relaxed outline-none placeholder:text-muted-foreground/70"
          />
          <div className="flex items-center gap-1 px-2.5 pb-2.5">
            {agent && agent.models.length > 0 && (
              <ChipSelect
                name="Model"
                icon={<CpuIcon />}
                value={agent.model}
                options={agent.models}
                busy={busy}
                title="Which model runs the next turn"
                onChange={(model) => change(() => setAgentModel(model))}
              />
            )}
            {/* Efforts are the selected model's, so this chip appears and
                disappears as models are switched rather than offering a level
                the current model would refuse. */}
            {agent && agent.efforts.length > 0 && (
              <>
                <Divider />
                <ChipSelect
                  name="Reasoning effort"
                  icon={<GaugeIcon />}
                  value={agent.effort}
                  options={agent.efforts}
                  placeholder="Effort"
                  busy={busy}
                  title="How hard the model thinks before answering"
                  onChange={(effort) => change(() => setAgentEffort(effort))}
                />
              </>
            )}
            {agent?.approval_mode && (
              <>
                <Divider />
                <ChipSelect
                  name="Approvals"
                  icon={<LockIcon />}
                  value={agent.approval_mode}
                  options={APPROVAL_MODES}
                  labelFor={(mode) => APPROVAL_LABELS[mode] ?? mode}
                  busy={busy}
                  title={APPROVAL_TITLES[agent.approval_mode]}
                  onChange={changeApproval}
                />
              </>
            )}
            <button
              type="submit"
              disabled={!draft.trim()}
              aria-label="Send message"
              className={cn(
                "ml-auto flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity",
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
