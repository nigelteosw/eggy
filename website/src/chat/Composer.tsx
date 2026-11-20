import { CACHE, recall, remember } from "../lib/cache";
import { enterSendsMessage, usesTouchKeyboard } from "../lib/composer";
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { AgentSelection, SessionExpiredError, getAgent, setAgentEffort, setAgentModel, setApprovalMode } from "../lib/api";
import { ArrowUpIcon, CheckIcon, ChevronLeftIcon, ChevronRightIcon, CloseIcon, PaperclipIcon, PlusIcon, ReturnIcon } from "../components/ui/icons";
import { IconSwap } from "../components/ui/icon-swap";
import { APPROVAL_MODES } from "../settings/PersonalSettingsCard";
import { MAX_UPLOAD_FILES, UPLOAD_TYPES, uploadProblem } from "../lib/uploads";
import { cn, errorMessage } from "../lib/utils";

function titleCase(value: string): string {
  return value ? value.charAt(0).toUpperCase() + value.slice(1) : value;
}

// The quiet controls under the box: text until hovered, then a soft fill.
const CHIP = "flex h-8 min-w-0 items-center gap-1.5 rounded-control px-2 text-caption transition-colors hover:bg-neutral-100 disabled:opacity-60";
const PANEL = "rounded-card border border-border bg-card p-1.5 shadow-lift";
const ROW = "flex w-full items-center justify-between gap-3 rounded-control px-3 py-2 text-left text-small transition-colors hover:bg-neutral-100";

// A menu that opens upward from a control under the box, closes on Escape or a
// click outside, and hands its content a way to close it.
function useMenu() {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);
  return [open, setOpen] as const;
}

function Backdrop({ onClose }: { onClose: () => void }) {
  return <div className="fixed inset-0 z-10" aria-hidden="true" onClick={onClose} />;
}

// The model and its thinking effort as one control. The chip shows the model
// bright and the effort muted. The menu leads with the model in force; Effort
// and the other models open beside it on a wide screen and in its place on a
// phone, where there is no room beside it.
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
  const [sub, setSub] = useState<"effort" | "models" | null>(null);
  const [open, setOpen] = useMenu();
  const others = agent.models.filter((model) => model !== agent.model);
  const hasEfforts = agent.efforts.length > 0;

  function close() {
    setOpen(false);
    setSub(null);
  }

  useEffect(() => {
    if (!open) setSub(null);
  }, [open]);

  // With one model and no effort levels there is nothing to choose; the
  // name is shown so the owner still knows what answers.
  if (!hasEfforts && others.length === 0) {
    return <span className={cn(CHIP, "truncate text-foreground hover:bg-transparent")}>{agent.model}</span>;
  }

  return (
    // On a phone the menu hangs from the whole row, not the chip, so it has
    // the screen's width to open into.
    <div className="min-w-0 sm:relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={busy}
        onClick={() => (open ? close() : setOpen(true))}
        title="Which model runs the next turn, and how hard it thinks"
        className={cn(CHIP, open && "bg-neutral-100")}
      >
        <span className="max-w-[7rem] truncate text-foreground sm:max-w-[12rem]">{agent.model}</span>
        {hasEfforts && <span className="shrink-0 text-muted-foreground">{titleCase(agent.effort)}</span>}
      </button>
      {open && (
        <>
          <Backdrop onClose={close} />
          <div className="absolute bottom-full right-0 z-20 mb-2 w-72 max-w-[calc(100vw-2rem)]">
            <div role="menu" aria-label="Model" className={cn(PANEL, sub && "max-sm:hidden")}>
              <div className="flex items-center justify-between gap-3 px-3 py-2 text-small">
                <span className="truncate">{agent.model}</span>
                <CheckIcon className="h-4 w-4 shrink-0 text-accent-700" />
              </div>
              {(hasEfforts || others.length > 0) && <div className="mx-2 my-1 h-px bg-border" />}
              {hasEfforts && (
                <button
                  type="button"
                  aria-expanded={sub === "effort"}
                  onMouseEnter={() => setSub("effort")}
                  onClick={() => setSub("effort")}
                  className={cn(ROW, sub === "effort" && "bg-neutral-100")}
                >
                  <span>Effort</span>
                  <span className="flex items-center gap-1 text-caption text-muted-foreground">
                    {titleCase(agent.effort)}
                    <ChevronRightIcon className="h-3.5 w-3.5" />
                  </span>
                </button>
              )}
              {others.length > 0 && (
                <button
                  type="button"
                  aria-expanded={sub === "models"}
                  onMouseEnter={() => setSub("models")}
                  onClick={() => setSub("models")}
                  className={cn(ROW, sub === "models" && "bg-neutral-100")}
                >
                  <span>More models</span>
                  <ChevronRightIcon className="h-3.5 w-3.5 text-muted-foreground" />
                </button>
              )}
            </div>
            {sub && (
              <div
                role="menu"
                aria-label={sub === "effort" ? "Effort" : "More models"}
                className={cn(PANEL, "max-h-[min(24rem,60dvh)] w-full fade-y overflow-y-auto sm:absolute sm:bottom-0 sm:right-full sm:mr-2 sm:w-60")}
              >
                <button type="button" onClick={() => setSub(null)} className={cn(ROW, "justify-start gap-1.5 text-muted-foreground sm:hidden")}>
                  <ChevronLeftIcon className="h-3.5 w-3.5" />
                  {sub === "effort" ? "Effort" : "More models"}
                </button>
                {sub === "effort" && (
                  <p className="px-3 pb-1.5 pt-2 text-caption leading-5 text-muted-foreground">Higher effort means more thorough replies, but they take longer.</p>
                )}
                {(sub === "effort" ? agent.efforts : others).map((choice) => {
                  const current = sub === "effort" ? choice === agent.effort : false;
                  return (
                    <button
                      key={choice}
                      type="button"
                      role="menuitemradio"
                      aria-checked={current}
                      onClick={() => {
                        close();
                        if (sub === "effort") {
                          if (!current) onEffort(choice);
                        } else onModel(choice);
                      }}
                      className={ROW}
                    >
                      <span className="truncate">{sub === "effort" ? titleCase(choice) : choice}</span>
                      {current && <CheckIcon className="h-4 w-4 shrink-0 text-accent-700" />}
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

// When Eggy asks, beside the model. A mode other than the default is tinted,
// so a setting that changes what asks cannot sit unnoticed; each choice reads
// in the words the chat reply uses, from the server.
function ModeMenu({
  mode,
  summaries,
  busy,
  onMode,
}: {
  mode: string;
  summaries?: Record<string, string>;
  busy: boolean;
  onMode: (mode: string) => void;
}) {
  const [open, setOpen] = useMenu();
  const label = APPROVAL_MODES.find((m) => m.value === mode)?.chip ?? titleCase(mode);
  return (
    <div className="shrink-0 sm:relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={busy}
        onClick={() => setOpen(!open)}
        aria-label={`When Eggy asks: ${label}`}
        title={summaries?.[mode] ?? "When Eggy asks before acting"}
        className={cn(CHIP, mode === "normal" ? "text-foreground" : "bg-yolk-soft font-medium text-yolk-ink hover:bg-yolk-soft hover:opacity-90", open && mode === "normal" && "bg-neutral-100")}
      >
        {label}
      </button>
      {open && (
        <>
          <Backdrop onClose={() => setOpen(false)} />
          <div role="menu" aria-label="When Eggy asks" className={cn(PANEL, "absolute bottom-full right-0 z-20 mb-2 w-72 max-w-[calc(100vw-2rem)]")}>
            <p className="px-3 pb-1 pt-2 text-caption text-muted-foreground">When Eggy asks</p>
            {APPROVAL_MODES.map((choice) => (
              <button
                key={choice.value}
                type="button"
                role="menuitemradio"
                aria-checked={choice.value === mode}
                onClick={() => {
                  setOpen(false);
                  if (choice.value !== mode) onMode(choice.value);
                }}
                className={cn(ROW, "items-start")}
              >
                <span className="min-w-0">
                  <span className="block">{choice.label}</span>
                  <span className="block text-caption leading-5 text-muted-foreground">{summaries?.[choice.value] ?? choice.hint}</span>
                </span>
                {choice.value === mode && <CheckIcon className="mt-0.5 h-4 w-4 shrink-0 text-accent-700" />}
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

// What + offers: today only files, the one thing the web chat can carry
// besides words.
function AddMenu({ onUpload, disabled }: { onUpload: () => void; disabled: boolean }) {
  const [open, setOpen] = useMenu();
  return (
    <div className="relative shrink-0">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Add"
        disabled={disabled}
        onClick={() => setOpen(!open)}
        className={cn(CHIP, "w-8 justify-center px-0 text-foreground [@media(pointer:coarse)]:w-11", open && "bg-neutral-100")}
      >
        <PlusIcon className="h-4 w-4" />
      </button>
      {open && (
        <>
          <Backdrop onClose={() => setOpen(false)} />
          <div role="menu" aria-label="Add" className={cn(PANEL, "absolute bottom-full left-0 z-20 mb-2 w-60")}>
            <MenuButton
              icon={<PaperclipIcon className="h-4 w-4" />}
              label="Upload files"
              onClick={() => {
                setOpen(false);
                onUpload();
              }}
            />
          </div>
        </>
      )}
    </div>
  );
}

function MenuButton({ icon, label, onClick }: { icon: ReactNode; label: string; onClick: () => void }) {
  return (
    <button type="button" role="menuitem" onClick={onClick} className={cn(ROW, "justify-start gap-2.5")}>
      <span className="text-muted-foreground">{icon}</span>
      {label}
    </button>
  );
}

// A picked file waiting to go, shown in the box above the text: a thumbnail
// for a picture, the name for a PDF.
function PendingFile({ file, onRemove }: { file: File; onRemove: () => void }) {
  const [preview, setPreview] = useState<string | null>(null);
  useEffect(() => {
    if (!file.type.startsWith("image/") || typeof URL.createObjectURL !== "function") return;
    const url = URL.createObjectURL(file);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [file]);
  return (
    <div className="group/file relative">
      {preview ? (
        <img src={preview} alt={file.name} className="h-14 w-14 rounded-control border border-border object-cover" />
      ) : (
        <div className="flex h-14 max-w-[12rem] items-center gap-2 rounded-control border border-border bg-neutral-100 px-3">
          <span className="rounded-mark bg-tomato-soft px-1.5 py-0.5 text-caption font-semibold text-tomato">PDF</span>
          <span className="truncate text-caption">{file.name}</span>
        </div>
      )}
      {/* The circle is small; the hit area around it is a finger's size. */}
      <button
        type="button"
        aria-label={`Remove ${file.name}`}
        onClick={onRemove}
        className="group/remove absolute -right-3.5 -top-3.5 flex h-8 w-8 items-center justify-center [@media(pointer:coarse)]:-right-5 [@media(pointer:coarse)]:-top-5 [@media(pointer:coarse)]:h-11 [@media(pointer:coarse)]:w-11"
      >
        <span className="flex h-5 w-5 items-center justify-center rounded-full border border-border bg-card text-muted-foreground shadow-subtle transition-colors group-hover/remove:text-foreground">
          <CloseIcon className="h-3 w-3" />
        </span>
      </button>
    </div>
  );
}

// A selection the user chose to reply to, and whose message it came from.
export type Quote = { text: string; ownMessage: boolean };

// Measuring the box needs the DOM; the server render (and the tests that use
// it) has none, so there the measuring runs as an ordinary effect, which never fires.
const useMeasuredEffect = typeof window === "undefined" ? useEffect : useLayoutEffect;

// The tallest the box grows before it scrolls: 240 px, or 40% of what is on
// screen when the keyboard has taken the rest.
const MAX_FIELD_HEIGHT = 240;

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
  onSend: (text: string, quote: Quote | null, files: File[]) => void;
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
  const [files, setFiles] = useState<File[]>([]);
  const [agent, setAgent] = useState<AgentSelection | null>(() => recall<AgentSelection>(CACHE.agent) ?? null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const field = useRef<HTMLTextAreaElement | null>(null);
  const picker = useRef<HTMLInputElement | null>(null);
  const touch = usesTouchKeyboard();

  // Switching conversations brings that conversation's draft back. The draft
  // is written as it is typed, not from an effect, so a switch can never write
  // one chat's text under the other's key. Picked files belong to the chat
  // they were picked in, and are not kept across a switch.
  useEffect(() => {
    setDraft(readDraft(draftKey));
    setFiles([]);
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
    box.style.height = `${fieldHeight(box.scrollHeight, window.visualViewport?.height ?? window.innerHeight)}px`;
  }, [draft, quote]);

  // A fresh quote means the user is mid-thought; bring them to the field.
  useEffect(() => {
    if (quote) field.current?.focus();
  }, [quote]);

  useEffect(() => {
    getAgent()
      .then((next) => setAgent(remember(CACHE.agent, next)))
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

  // Files are checked as they are added, so a refusal arrives while the
  // owner is still looking at what they picked rather than after sending.
  function addFiles(picked: File[]) {
    if (picked.length === 0) return;
    const problems = picked.map(uploadProblem).filter((problem): problem is string => problem !== null);
    const accepted = picked.filter((file) => uploadProblem(file) === null);
    const room = MAX_UPLOAD_FILES - files.length;
    if (accepted.length > room) problems.push(`Send at most ${MAX_UPLOAD_FILES} files at once.`);
    setNote(problems[0] ?? null);
    if (room > 0) setFiles((current) => [...current, ...accepted.slice(0, room)]);
    field.current?.focus();
  }

  function submit(event: React.SyntheticEvent) {
    event.preventDefault();
    const text = draft.trim();
    if (!text && files.length === 0) return;
    updateDraft("");
    onSend(text, quote, files);
    setFiles([]);
    setNote(null);
    onClearQuote();
    // On a phone the keyboard stays up only if the field keeps focus.
    field.current?.focus();
  }

  const hasContent = draft.trim() !== "" || files.length > 0;
  const showStop = running && onStop && !hasContent;
  const mode = agent?.approval_mode;

  return (
    <form onSubmit={submit} className="composer-dock shrink-0 px-3 pb-[max(0.5rem,var(--safe-bottom))] pt-3 sm:px-8 sm:pb-4">
      <div className="mx-auto w-full max-w-4xl">
        {note && (
          <p className="mb-2 rounded-control bg-tomato-soft px-3 py-1.5 text-caption text-tomato" role="alert">
            {note}
          </p>
        )}
        {/* One box: the quote and any picked files when there are some, then
            the text with the send control at its trailing edge. */}
        <div
          className={cn(
            "cursor-text rounded-card border border-border bg-card transition-[border-color,box-shadow] duration-150 focus-within:border-neutral-400",
            dragging && "border-accent-700 ring-2 ring-accent-700/20",
          )}
          onClick={(event) => {
            if (event.target === event.currentTarget) field.current?.focus();
          }}
          onDragOver={(event) => {
            if (!event.dataTransfer.types.includes("Files")) return;
            event.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(event) => {
            if (!event.dataTransfer.files.length) return;
            event.preventDefault();
            setDragging(false);
            addFiles(Array.from(event.dataTransfer.files));
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
          {files.length > 0 && (
            <div className="flex flex-wrap gap-2.5 px-3 pt-3">
              {files.map((file, index) => (
                <PendingFile key={`${file.name}-${index}`} file={file} onRemove={() => setFiles((current) => current.filter((_, i) => i !== index))} />
              ))}
            </div>
          )}
          <div className="flex items-end gap-1 py-1 pl-3.5 pr-1">
            <textarea
              ref={field}
              value={draft}
              rows={1}
              aria-label="Message"
              enterKeyHint={touch ? "enter" : "send"}
              onChange={(event) => updateDraft(event.target.value)}
              onPaste={(event) => {
                const pasted = Array.from(event.clipboardData.files);
                if (pasted.length === 0) return;
                event.preventDefault();
                addFiles(pasted);
              }}
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
              className="scrollbar-slim min-w-0 flex-1 resize-none bg-transparent py-2 text-field leading-6 outline-none placeholder:text-muted-foreground/70"
            />
            {/* One button that turns from send into stop and back. On a
                keyboard it is drawn as Return, the key it stands for. */}
            <div className="group/send relative shrink-0">
              <button
                type={showStop ? "button" : "submit"}
                onClick={showStop ? onStop : undefined}
                disabled={!showStop && !hasContent}
                aria-label={showStop ? "Stop" : "Send message"}
                title={touch || showStop ? (showStop ? "Stop this reply" : "Send") : undefined}
                className={cn(
                  "flex h-9 w-9 items-center justify-center rounded-control transition-[background-color,color,opacity,transform] duration-200 active:scale-95 [@media(pointer:coarse)]:h-11 [@media(pointer:coarse)]:w-11",
                  showStop
                    ? "bg-foreground text-background hover:opacity-85"
                    : hasContent
                      ? touch
                        ? "bg-primary text-primary-foreground"
                        : "text-foreground hover:bg-neutral-100"
                      : "text-muted-foreground/60",
                )}
              >
                <IconSwap
                  on={Boolean(showStop)}
                  onIcon={<span className="block h-3 w-3 rounded-mark bg-current" />}
                  offIcon={touch ? <ArrowUpIcon className="h-4 w-4" /> : <ReturnIcon className="h-4 w-4" />}
                />
              </button>
              {!touch && !showStop && (
                <span
                  role="tooltip"
                  className="pointer-events-none absolute bottom-full right-0 z-30 mb-1.5 flex items-center gap-1.5 whitespace-nowrap rounded-control border border-border bg-card px-2 py-1 text-caption text-foreground opacity-0 shadow-lift transition-opacity delay-300 group-hover/send:opacity-100"
                >
                  Send message
                  <ReturnIcon className="h-3 w-3 text-muted-foreground" />
                </span>
              )}
            </div>
          </div>
        </div>
        <input
          ref={picker}
          type="file"
          multiple
          accept={UPLOAD_TYPES.join(",")}
          className="hidden"
          onChange={(event) => {
            addFiles(Array.from(event.target.files ?? []));
            event.target.value = "";
          }}
        />
        <div className="relative mt-1.5 flex items-center gap-1">
          <AddMenu onUpload={() => picker.current?.click()} disabled={files.length >= MAX_UPLOAD_FILES} />
          <div className="ml-auto flex min-w-0 items-center gap-0.5">
            {agent && agent.models.length > 0 && (
              <ModelMenu
                agent={agent}
                busy={busy}
                onModel={(model) => change(() => setAgentModel(model))}
                onEffort={(effort) => change(() => setAgentEffort(effort))}
              />
            )}
            {mode && (
              <ModeMenu
                mode={mode}
                summaries={agent?.approval_summaries}
                busy={busy}
                onMode={(next) =>
                  change(async () => {
                    await setApprovalMode(next);
                    return getAgent();
                  })
                }
              />
            )}
          </div>
        </div>
      </div>
    </form>
  );
}
