import { useEffect, useRef, useState } from "react";
import { AgentSelection, SessionExpiredError, getAgent, setAgentEffort, setAgentModel, setApprovalMode } from "./api";
import { ArrowUpIcon, ChevronDownIcon, CloseIcon, PaperclipIcon, PlusIcon } from "./components/ui/icons";
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
        "relative flex h-9 min-w-0 items-center gap-1 rounded-xl px-2 text-xs text-foreground transition-colors",
        pickable && "cursor-pointer hover:bg-neutral-200",
        busy && "opacity-60",
      )}
    >
      <span className={cn("max-w-[6rem] truncate font-medium sm:max-w-[9rem]", !value && "text-muted-foreground")}>{label}</span>
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
  // Files the owner picked with the + control. They show as chips above the
  // field; the chat API does not take attachments yet, so sending is refused
  // while any are attached rather than dropping them silently.
  const [files, setFiles] = useState<File[]>([]);
  const field = useRef<HTMLTextAreaElement | null>(null);
  const fileInput = useRef<HTMLInputElement | null>(null);

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
    if (files.length > 0) {
      setNote("Eggy can't read attached files yet. Remove them to send.");
      return;
    }
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
          {files.length > 0 && (
            <ul className="flex flex-wrap gap-1.5 px-3 pt-3" aria-label="Attached files">
              {files.map((file, index) => (
                <li
                  key={`${file.name}-${index}`}
                  className="flex max-w-[14rem] items-center gap-1.5 rounded-xl bg-background py-1 pl-2.5 pr-1 text-xs shadow-sm"
                >
                  <PaperclipIcon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  <span className="truncate">{file.name}</span>
                  <button
                    type="button"
                    aria-label={`Remove ${file.name}`}
                    onClick={() => setFiles((current) => current.filter((_, at) => at !== index))}
                    className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-neutral-100 hover:text-foreground"
                  >
                    <CloseIcon className="h-3 w-3" />
                  </button>
                </li>
              ))}
            </ul>
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
          <div className="flex min-w-0 items-center gap-1 px-2 pb-2 pt-1">
            <button
              type="button"
              onClick={() => fileInput.current?.click()}
              aria-label="Add files or photos"
              title="Add files or photos"
              className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-neutral-700 transition-colors hover:bg-neutral-200 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            >
              <PlusIcon className="h-5 w-5" />
            </button>
            <input
              ref={fileInput}
              type="file"
              multiple
              accept="image/*,application/pdf,text/*"
              className="hidden"
              onChange={(event) => {
                const picked = Array.from(event.target.files ?? []);
                if (picked.length > 0) setFiles((current) => [...current, ...picked]);
                event.target.value = "";
              }}
            />
            <span className="hidden flex-1 truncate text-center text-xs text-muted-foreground sm:block">Eggy can make mistakes.</span>
            <span className="flex-1 sm:hidden" />
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
                "ml-1 flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity",
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
