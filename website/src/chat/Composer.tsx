import { enterSendsMessage, usesTouchKeyboard } from "../lib/composer";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { AgentSelection, SessionExpiredError, getAgent, setAgentEffort, setAgentModel, setApprovalMode } from "../lib/api";
import { ArrowUpIcon, CheckIcon, ChevronDownIcon, CloseIcon } from "../components/ui/icons";
import { cn, errorMessage } from "../lib/utils";

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

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") close();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

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
          "flex h-9 min-w-0 items-center gap-1 rounded-full px-2.5 text-caption transition-colors hover:bg-neutral-100 disabled:opacity-60",
          open && "bg-neutral-100",
        )}
      >
        <span className="max-w-[6rem] truncate font-medium text-foreground sm:max-w-[10rem]">{agent.model}</span>
        {agent.efforts.length > 0 && <span className="hidden shrink-0 text-muted-foreground sm:inline">{titleCase(agent.effort)}</span>}
        <ChevronDownIcon className={cn("h-3 w-3 shrink-0 text-muted-foreground transition-transform", open && "rotate-180")} />
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-10" aria-hidden="true" onClick={close} />
          <div role="menu" aria-label="Model" className="absolute bottom-full right-0 z-20 mb-2 max-h-[min(24rem,60dvh)] w-64 max-w-[calc(100vw-2rem)] overflow-y-auto rounded-card border border-border bg-card p-1.5 shadow-lift">
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
                className="flex w-full items-center justify-between gap-3 rounded-control px-3 py-2.5 text-left text-small transition-colors hover:bg-neutral-100"
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
                  className="flex w-full items-center justify-between gap-3 rounded-control px-3 py-2.5 text-left text-small transition-colors hover:bg-neutral-100"
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
                      className="flex w-full items-center justify-between gap-3 rounded-control py-2 pl-6 pr-3 text-left text-small text-muted-foreground transition-colors hover:bg-neutral-100 hover:text-foreground"
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

// A mode other than the default shows as a quiet pill above the box, in the
// panel's words for it, so a setting that changes what asks cannot sit
// unnoticed. The full sentence is its tooltip, from the server, which is the
// one Telegram sends.
const APPROVAL_WARNINGS: Record<string, string> = {
  strict: "Asking every time",
  auto: "Never asking",
};

// A selection the user chose to reply to, and whose message it came from.
export type Quote = { text: string; ownMessage: boolean };

// Measuring the box needs the DOM; the server render (and the tests that use
// it) has none, so there the measuring runs as an ordinary effect, which never fires.
const useMeasuredEffect = typeof window === "undefined" ? useEffect : useLayoutEffect;

// The tallest the box grows before it scrolls: 240 px, or 40% of what is on
// screen when the keyboard has taken the rest.
const MAX_FIELD_HEIGHT = 240;
// One line of the field: a 24 px line and 8 px of padding above and below.
const SINGLE_LINE = 44;

// fieldHeight is how tall the message box should be for its content.
export function fieldHeight(scrollHeight: number, visible: number): number {
  return Math.min(scrollHeight, MAX_FIELD_HEIGHT, Math.max(96, Math.round(visible * 0.4)));
}

// A draft survives switching chats and a reload in the same tab, so a thought
// half-typed in one conversation is still there on the way back. Storage can be
// missing or refuse; the draft is then only in memory.
function readDraft(key: string | undefined): string {
  if (!key) return "";
  try {
    return sessionStorage.getItem("eggy.draft." + key) ?? "";
  } catch {
    return "";
  }
}

function writeDraft(key: string | undefined, text: string) {
  if (!key) return;
  try {
    if (text) sessionStorage.setItem("eggy.draft." + key, text);
    else sessionStorage.removeItem("eggy.draft." + key);
  } catch {
    // The draft stays in memory.
  }
}

export function Composer({
  onSend,
  onStop,
  running = false,
  onSessionExpired,
  quote,
  onClearQuote,
  draftKey,
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
  // Which conversation the draft belongs to; a new chat has its own.
  draftKey?: string;
}) {
  const [draft, setDraft] = useState(() => readDraft(draftKey));
  const [agent, setAgent] = useState<AgentSelection | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [expanded, setExpanded] = useState(false);
  const field = useRef<HTMLTextAreaElement | null>(null);
  const touch = usesTouchKeyboard();

  // Switching conversations brings that conversation's draft back. The draft
  // is written as it is typed, not from an effect, so a switch can never write
  // one chat's text under the other's key.
  useEffect(() => {
    setDraft(readDraft(draftKey));
  }, [draftKey]);

  function updateDraft(text: string) {
    setDraft(text);
    writeDraft(draftKey, text);
  }

  // The box fits what is typed, pasted, restored or cleared -- not only what
  // a key press changed -- so it is measured after every change of the text.
  useMeasuredEffect(() => {
    const box = field.current;
    if (!box) return;
    box.style.height = "auto";
    // Past one line the text takes the box's whole width and the controls drop
    // to a row of their own, so a long message is not squeezed into a column
    // beside them. It stays that way until the box is emptied, so the layout
    // does not flicker at the edge of a line.
    if (draft === "") setExpanded(false);
    else if (!expanded && box.scrollHeight > SINGLE_LINE) setExpanded(true);
    box.style.height = `${fieldHeight(box.scrollHeight, window.visualViewport?.height ?? window.innerHeight)}px`;
  }, [draft, quote, expanded]);

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

  async function restoreApproval() {
    setBusy(true);
    setNote(null);
    try {
      await setApprovalMode("normal");
      setAgent(await getAgent());
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setNote(errorMessage(err, "Could not change when Eggy asks"));
    } finally {
      setBusy(false);
    }
  }

  function submit(event: React.SyntheticEvent) {
    event.preventDefault();
    const text = draft.trim();
    if (!text) return;
    updateDraft("");
    onSend(text, quote);
    onClearQuote();
    // On a phone the keyboard stays up only if the field keeps focus.
    field.current?.focus();
  }

  const hasText = draft.trim() !== "";
  const showStop = running && onStop && !hasText;
  const mode = agent?.approval_mode;

  return (
    <form onSubmit={submit} className="composer-dock shrink-0 px-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] pt-3 sm:px-8 sm:pb-6">
      <div className="mx-auto w-full max-w-4xl">
        {(note || (mode && mode !== "normal")) && (
          <div className="mb-2 flex flex-wrap items-center justify-end gap-2">
            {note && (
              <p className="mr-auto rounded-control bg-tomato-soft px-3 py-1.5 text-caption text-tomato" role="alert">
                {note}
              </p>
            )}
            {mode && mode !== "normal" && (
              // One tap returns to the default, Before changes.
              <button
                type="button"
                onClick={restoreApproval}
                disabled={busy}
                title={agent?.approval_summaries?.[mode]}
                className="flex h-8 shrink-0 items-center gap-1.5 rounded-full bg-yolk-soft px-3 text-caption font-medium text-yolk-ink transition-opacity hover:opacity-90 disabled:opacity-60"
              >
                {APPROVAL_WARNINGS[mode] ?? mode}
                <span className="font-normal underline underline-offset-2">Restore</span>
              </button>
            )}
          </div>
        )}
        {/* One box: the quote when there is one, then the text with the run
            settings and the send control at its trailing edge. It is a single
            line until the text needs more, and the controls stay on the last
            line as it grows. */}
        <div
          className="cursor-text rounded-card border border-border bg-card shadow-subtle transition-[border-color,box-shadow] duration-150 focus-within:border-neutral-400 focus-within:shadow-card"
          onClick={(event) => {
            if (event.target === event.currentTarget) field.current?.focus();
          }}
        >
          {quote && (
            <div className="flex items-start gap-2 px-3 pt-3">
              <div className="flex min-w-0 flex-1 items-start rounded-control bg-neutral-100 px-3 py-2">
                <span aria-hidden="true" className="mr-2.5 mt-0.5 w-0.5 shrink-0 self-stretch rounded-full bg-muted-foreground/50" />
                <p className="line-clamp-3 min-w-0 whitespace-pre-line break-words text-caption leading-5 text-foreground/85">{quote.text}</p>
              </div>
              <button
                type="button"
                aria-label="Remove quoted text"
                onClick={onClearQuote}
                className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-neutral-100 hover:text-foreground"
              >
                <CloseIcon className="h-3.5 w-3.5" />
              </button>
            </div>
          )}
          <div className={cn("flex flex-wrap items-end gap-1 py-1.5 pl-4 pr-1.5", expanded && "pt-2.5")}>
            <textarea
              ref={field}
              value={draft}
              rows={1}
              aria-label="Message"
              enterKeyHint={touch ? "enter" : "send"}
              onChange={(event) => updateDraft(event.target.value)}
              onKeyDown={(event) => {
                // A key that ends an IME composition is the composition's, not a send.
                if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                if (event.key === "Escape" && quote) {
                  event.preventDefault();
                  onClearQuote();
                  return;
                }
                if (enterSendsMessage(event.key, event.shiftKey, touch)) {
                  event.preventDefault();
                  submit(event);
                }
              }}
              placeholder={quote ? "Reply to the quote" : "Message Eggy"}
              className={cn(
                "scrollbar-slim min-w-0 resize-none bg-transparent py-2 text-field leading-6 outline-none placeholder:text-muted-foreground/70",
                expanded ? "basis-full pr-2.5" : "flex-1",
              )}
            />
            <div className="ml-auto flex shrink-0 items-center gap-1 pb-px">
              {agent && agent.models.length > 0 && (
                <ModelMenu
                  agent={agent}
                  busy={busy}
                  onModel={(model) => change(() => setAgentModel(model))}
                  onEffort={(effort) => change(() => setAgentEffort(effort))}
                />
              )}
              {showStop ? (
                <button
                  type="button"
                  onClick={onStop}
                  aria-label="Stop"
                  title="Stop this reply"
                  className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-foreground text-background transition-opacity hover:opacity-85 [@media(pointer:coarse)]:h-11 [@media(pointer:coarse)]:w-11"
                >
                  <span aria-hidden="true" className="h-3 w-3 rounded-mark bg-background" />
                </button>
              ) : (
                <button
                  type="submit"
                  disabled={!hasText}
                  aria-label="Send message"
                  title={touch ? "Send" : "Send (Enter) · new line (Shift+Enter)"}
                  className={cn(
                    "flex h-9 w-9 shrink-0 items-center justify-center rounded-full transition-[background-color,color,transform] duration-150 active:scale-95 [@media(pointer:coarse)]:h-11 [@media(pointer:coarse)]:w-11",
                    hasText ? "bg-primary text-primary-foreground hover:opacity-90" : "bg-neutral-200 text-muted-foreground",
                  )}
                >
                  <ArrowUpIcon className="h-4 w-4" />
                </button>
              )}
            </div>
          </div>
        </div>
      </div>
    </form>
  );
}
