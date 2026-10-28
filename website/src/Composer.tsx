import { enterSendsMessage, usesTouchKeyboard } from "./lib/composer";
import { useEffect, useRef, useState } from "react";
import { AgentSelection, SessionExpiredError, getAgent, setAgentEffort, setAgentModel, setApprovalMode } from "./api";
import { ArrowUpIcon, CheckIcon, ChevronDownIcon, CloseIcon } from "./components/ui/icons";
import { cn, errorMessage } from "./lib/utils";

function titleCase(value: string): string {
  return value ? value.charAt(0).toUpperCase() + value.slice(1) : value;
}

// The model and its thinking effort as one control. The chip shows the model
// bright and the effort muted; opening it lists the models, with Effort as a
// row that expands into its own choices.
function ModelMenu({
  agent,
  busy,
  onModel,
  onEffort,
}: {
  agent: AgentSelection;
  busy: boolean;
  onModel: (model: string) => void;
  onEffort: (effort: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [effortsOpen, setEffortsOpen] = useState(false);

  function close() {
    setOpen(false);
    setEffortsOpen(false);
  }

  return (
    <div className="relative min-w-0">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={busy}
        onClick={() => (open ? close() : setOpen(true))}
        title="Which model runs the next turn, and how hard it thinks"
        className={cn(
          "flex h-9 min-w-0 items-center gap-1.5 rounded-xl px-2 text-xs transition-colors hover:bg-neutral-200 disabled:opacity-60",
          open && "bg-neutral-200",
        )}
      >
        <span className="max-w-[7rem] truncate font-medium text-foreground sm:max-w-[10rem]">{agent.model}</span>
        {agent.efforts.length > 0 && <span className="shrink-0 text-muted-foreground">{titleCase(agent.effort)}</span>}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-10" aria-hidden="true" onClick={close} />
          <div role="menu" aria-label="Model" className="absolute bottom-full right-0 z-20 mb-2 w-64 rounded-2xl border border-border bg-background p-1.5 shadow-lift">
            {agent.models.map((model) => (
              <button
                key={model}
                type="button"
                role="menuitemradio"
                aria-checked={model === agent.model}
                onClick={() => {
                  close();
                  if (model !== agent.model) onModel(model);
                }}
                className="flex w-full items-center justify-between gap-3 rounded-xl px-3 py-2.5 text-left text-sm transition-colors hover:bg-neutral-100"
              >
                <span className="truncate">{model}</span>
                {model === agent.model && <CheckIcon className="h-4 w-4 shrink-0 text-accent-700" />}
              </button>
            ))}
            {agent.efforts.length > 0 && (
              <>
                <div className="my-1 h-px bg-border" />
                <button
                  type="button"
                  aria-expanded={effortsOpen}
                  onClick={() => setEffortsOpen((current) => !current)}
                  className="flex w-full items-center justify-between gap-3 rounded-xl px-3 py-2.5 text-left text-sm transition-colors hover:bg-neutral-100"
                >
                  <span>Effort</span>
                  <span className="flex items-center gap-1 text-muted-foreground">
                    {titleCase(agent.effort)}
                    <ChevronDownIcon className={cn("h-3.5 w-3.5 transition-transform", effortsOpen ? "rotate-0" : "-rotate-90")} />
                  </span>
                </button>
                {effortsOpen &&
                  agent.efforts.map((effort) => (
                    <button
                      key={effort}
                      type="button"
                      role="menuitemradio"
                      aria-checked={effort === agent.effort}
                      onClick={() => {
                        close();
                        if (effort !== agent.effort) onEffort(effort);
                      }}
                      className="flex w-full items-center justify-between gap-3 rounded-xl py-2 pl-6 pr-3 text-left text-sm text-muted-foreground transition-colors hover:bg-neutral-100 hover:text-foreground"
                    >
                      <span>{titleCase(effort)}</span>
                      {effort === agent.effort && <CheckIcon className="h-4 w-4 shrink-0 text-accent-700" />}
                    </button>
                  ))}
              </>
            )}
          </div>
        </>
      )}
    </div>
  );
}

// The three approval modes as the composer says them: a short label for the
// chip, and the full sentence as its tooltip. The sentences are the ones
// internal/commands.ModeMessage sends to Telegram, so the two surfaces do not
// describe the same setting differently.
// What a non-default mode means, in a few words, for the pill beside the send
// button. The titles say the full thing on hover.
const APPROVAL_WARNINGS: Record<string, string> = {
  strict: "Asks before every tool",
  auto: "Nothing asks",
};

const APPROVAL_TITLES: Record<string, string> = {
  strict: "Strict mode. Every tool call asks first, reading included.",
  normal: "Normal mode. Reading runs freely; anything that writes asks first.",
  auto: "Auto mode. Nothing asks — tool calls that change things now run unapproved.",
};

// A selection the user chose to reply to, and whose message it came from.
export type Quote = { text: string; ownMessage: boolean };

export function Composer({
  onSend,
  onStop,
  running = false,
  onSessionExpired,
  quote,
  onClearQuote,
}: {
  onSend: (text: string, quote: Quote | null) => void;
  // While a reply is being written the send control becomes Stop, as /stop
  // does in Telegram. Typing a new message still sends it, and queues behind
  // the running turn the way a message sent mid-turn already does.
  onStop?: () => void;
  running?: boolean;
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
        {/* The box holds only what is typed and the send control, so it stays a
            slim single line until the text needs more room. */}
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
          <div className="flex items-end gap-2 py-1.5 pl-[18px] pr-1.5">
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
                if (enterSendsMessage(event.key, event.shiftKey, usesTouchKeyboard())) {
                  event.preventDefault();
                  submit(event);
                }
              }}
              placeholder={quote ? "Reply to the quoted text..." : "Ask Eggy anything..."}
              className="scrollbar-slim max-h-[220px] min-w-0 flex-1 resize-none bg-transparent py-2.5 text-base leading-relaxed outline-none placeholder:text-muted-foreground/70"
            />
            {running && onStop && !draft.trim() ? (
              <button
                type="button"
                onClick={onStop}
                aria-label="Stop"
                title="Stop this reply"
                className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-neutral-200 text-foreground transition-colors hover:bg-neutral-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              >
                <span aria-hidden="true" className="h-3.5 w-3.5 rounded-sm bg-foreground" />
              </button>
            ) : (
              <button
                type="submit"
                disabled={!draft.trim()}
                aria-label="Send message"
                className={cn(
                  "flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity",
                  "hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:opacity-30",
                )}
              >
                <ArrowUpIcon className="h-4 w-4" />
              </button>
            )}
          </div>
        </div>
        {/* Everything else sits outside the box: upload on the left, the run
            settings on the right. The upload control names itself on hover. */}
        <div className="mt-2 flex min-w-0 items-center gap-1 px-1">
          <span className="flex-1" />
          {agent && agent.models.length > 0 && (
            <ModelMenu
              agent={agent}
              busy={busy}
              onModel={(model) => change(() => setAgentModel(model))}
              onEffort={(effort) => change(() => setAgentEffort(effort))}
            />
          )}
          {agent?.approval_mode && agent.approval_mode !== "normal" && (
            // Only a departure from the default is shown, and it names the
            // departure, so a mode that lets things run unasked cannot sit
            // unnoticed. One tap returns to the default.
            <button
              type="button"
              onClick={() => changeApproval("normal")}
              disabled={busy}
              title={APPROVAL_TITLES[agent.approval_mode]}
              className="flex h-9 shrink-0 items-center gap-1.5 rounded-full bg-eg-ask px-3 text-xs font-medium text-eg-ask-ink transition-opacity hover:opacity-90 disabled:opacity-60"
            >
              {APPROVAL_WARNINGS[agent.approval_mode] ?? agent.approval_mode}
              <span className="font-normal underline underline-offset-2">Restore</span>
            </button>
          )}
        </div>
      </div>
    </form>
  );
}
