import { useEffect, useRef, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ChatEvent, SessionExpiredError, approveChatDecision, createThread, getChatHistory, renameThread, sendChatMessage, stopChatTurn } from "../lib/api";
import { Composer, Quote } from "./Composer";
import { Button } from "../components/ui/button";
import { ArrowDownIcon, CheckIcon, ChevronDownIcon, CopyIcon, LockIcon, MenuIcon, PlusIcon, ReplyIcon, RetryIcon } from "../components/ui/icons";
import { usesTouchKeyboard } from "../lib/composer";
import { replyQuote } from "../lib/reply";
import { timeAgo } from "../lib/time";
import { cn, errorMessage } from "../lib/utils";
import { withImages } from "./images";

// The status steps a turn went through, and how long it ran, kept on the reply
// the turn produced. Live steps are not persisted, so a reloaded chat shows
// only the reply and any reasoning the backend stored as its own message.
type Activity = { steps: string[]; seconds: number | null };
// at is when the message was written, as an ISO time. A reply that has not come
// back from history yet has none, so its footer shows no time.
type ChatMessage = { id: string; role: "user" | "assistant"; text: string; at?: string };

// The backend delivers a model's reasoning as its own assistant message with
// this prefix when the owner has thinking shown (see turns.run). The web view
// draws it as a collapsed thought rather than as a reply.
const THINKING_PREFIX = "Thinking:\n";

// The reply prefix as events.Message.Prompt builds it on the backend. The
// optimistic bubble must be the exact string history will later hold, or the
// reconcile in loadHistory never drops it; and a recorded user turn is
// split back apart here so the transcript shows a quote, not brackets.
const REPLY_PREFIX = /^\[Replying to( your previous message)?: "([\s\S]*?)"\]\n\n([\s\S]*)$/;

function withQuote(quote: Quote | null, text: string): string {
  if (!quote) return text;
  return `[Replying to${quote.ownMessage ? " your previous message" : ""}: "${quote.text}"]\n\n${text}`;
}

function splitQuote(text: string): { quote: string | null; ownMessage: boolean; body: string } {
  const match = REPLY_PREFIX.exec(text);
  return match ? { quote: match[2], ownMessage: match[1] !== undefined, body: match[3] } : { quote: null, ownMessage: false, body: text };
}
type PendingApproval = { id: string; summary: string; details?: string };
// How long a finger rests on a message before its actions open.
const LONG_PRESS_MS = 500;
// Where the floating Reply button sits, relative to the scrolling transcript.
type SelectionAnchor = { text: string; ownMessage: boolean; top: number; left: number };

// Watches text selection within `container` and reports the selected text
// with a spot to hang a button on, or null when nothing useful is selected.
function useSelectionAnchor(container: React.RefObject<HTMLElement | null>) {
  const [anchor, setAnchor] = useState<SelectionAnchor | null>(null);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    let selecting = false;

    function hide() {
      clearTimeout(timer);
      setAnchor(null);
    }

    function schedule() {
      hide();
      if (!selecting) timer = setTimeout(update, 150);
    }

    function pointerDown() {
      selecting = true;
      hide();
    }

    function pointerUp() {
      if (!selecting) return;
      selecting = false;
      schedule();
    }

    function update() {
      const selection = document.getSelection();
      const host = container.current;
      if (!selection || selection.isCollapsed || !host || selection.rangeCount === 0) {
        setAnchor(null);
        return;
      }
      const range = selection.getRangeAt(0);
      const text = selection.toString().trim();
      if (!text || !host.contains(range.commonAncestorContainer)) {
        setAnchor(null);
        return;
      }
      // Position from the first line of the selection so the button stays
      // near where the drag started, not somewhere below a long block.
      // "Own" from the model's side: a quote of an assistant message tells
      // it that it is being pointed back at something it said.
      const start = range.startContainer instanceof Element ? range.startContainer : range.startContainer.parentElement;
      const ownMessage = start?.closest("[data-role]")?.getAttribute("data-role") === "assistant";
      const rects = range.getClientRects();
      const first = rects.length > 0 ? rects[0] : range.getBoundingClientRect();
      const hostRect = host.getBoundingClientRect();
      setAnchor({
        text,
        ownMessage,
        top: first.top - hostRect.top + host.scrollTop,
        left: Math.max(0, first.left - hostRect.left + host.scrollLeft),
      });
    }
    // Read geometry only after the selection settles, never mid-drag.
    document.addEventListener("pointerdown", pointerDown);
    document.addEventListener("pointerup", pointerUp);
    document.addEventListener("pointercancel", pointerUp);
    document.addEventListener("keyup", schedule);
    document.addEventListener("selectionchange", schedule);
    return () => {
      clearTimeout(timer);
      document.removeEventListener("pointerdown", pointerDown);
      document.removeEventListener("pointerup", pointerUp);
      document.removeEventListener("pointercancel", pointerUp);
      document.removeEventListener("keyup", schedule);
      document.removeEventListener("selectionchange", schedule);
    };
  }, [container]);

  return [anchor, () => setAnchor(null)] as const;
}

// copyText puts a message on the clipboard. It reports whether that worked: a
// refused clipboard (an insecure page, a locked-down browser) leaves the text on
// screen to select by hand.
async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

// MessageSheet is a long press's menu: the message's actions, at the thumb.
function MessageSheet({ onReply, onCopy, onClose }: { onReply: () => void; onCopy: () => void; onClose: () => void }) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-foreground/25" onClick={onClose}>
      <div
        role="dialog"
        aria-label="Message actions"
        onClick={(event) => event.stopPropagation()}
        className="flex w-full flex-col gap-1 rounded-t-3xl bg-card p-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] shadow-lift"
      >
        <button type="button" onClick={onReply} className="flex min-h-12 items-center gap-3 rounded-2xl px-4 text-left text-sm font-medium transition-colors hover:bg-neutral-100">
          <ReplyIcon className="h-4 w-4" />
          Reply
        </button>
        <button type="button" onClick={onCopy} className="flex min-h-12 items-center gap-3 rounded-2xl px-4 text-left text-sm font-medium transition-colors hover:bg-neutral-100">
          Copy
        </button>
        <button type="button" onClick={onClose} className="min-h-12 rounded-2xl px-4 text-sm font-medium text-neutral-700 transition-colors hover:bg-neutral-100">
          Cancel
        </button>
      </div>
    </div>
  );
}

// MessageFooter is the row under a message: copy it, retry the turn behind it,
// and when it was written. Desktop shows it on hover, except that the latest
// reply keeps it; a finger has no hover, so a touch screen always shows it.
// Copy swaps its icon for a check for a moment, because a toast would sit over
// the composer the thumb is about to use.
function MessageFooter({
  text,
  at,
  touch,
  alwaysVisible,
  onRetry,
}: {
  text: string;
  at?: string;
  touch: boolean;
  alwaysVisible: boolean;
  onRetry?: () => void;
}) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    if (!(await copyText(text))) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }
  const action = cn(
    "flex items-center justify-center rounded-lg text-muted-foreground transition-colors hover:text-foreground",
    touch ? "h-11 w-11" : "h-7 w-7",
  );
  return (
    <div
      className={cn(
        "flex items-center gap-1 transition-opacity",
        !alwaysVisible && "opacity-0 group-hover:opacity-100 focus-within:opacity-100",
      )}
    >
      <button type="button" onClick={copy} aria-label={copied ? "Copied" : "Copy message"} title="Copy" className={action}>
        {copied ? <CheckIcon className="h-4 w-4" /> : <CopyIcon className="h-4 w-4" />}
      </button>
      {onRetry && (
        <button type="button" onClick={onRetry} aria-label="Retry" title="Retry" className={action}>
          <RetryIcon className="h-4 w-4" />
        </button>
      )}
      {at && <MessageTime at={at} />}
    </div>
  );
}

// MessageTime shows how long ago a message was written. Its exact time is on
// hover for a mouse, and on a tap for a finger.
function MessageTime({ at }: { at: string }) {
  const [exact, setExact] = useState(false);
  const full = new Date(at).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  return (
    <button
      type="button"
      title={full}
      onClick={() => setExact((current) => !current)}
      className="flex min-h-7 items-center rounded-lg px-2 text-xs text-muted-foreground transition-colors hover:text-foreground"
    >
      <time dateTime={at}>{exact ? full : timeAgo(at, Date.now())}</time>
    </button>
  );
}

function MessageBody({ text, isUserBubble }: { text: string; isUserBubble: boolean }) {
  return (
    <div
      className={cn(
        "prose max-w-none break-words text-[0.9375rem] leading-7",
        "[&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
        "prose-pre:rounded-md prose-pre:text-[0.8125rem] prose-code:font-mono prose-code:text-[0.85em] prose-code:before:content-none prose-code:after:content-none",
        isUserBubble
          ? "user-message prose-pre:bg-black/20"
          : "assistant-message prose-code:rounded prose-code:bg-muted prose-code:px-1 prose-code:py-0.5",
      )}
    >
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ children, ...props }: React.ComponentPropsWithoutRef<"a">) => (
            <a {...props} target="_blank" rel="noreferrer noopener">
              {children}
            </a>
          ),
          // A drawn image opens full size in its own tab; in the thread it is
          // bounded so a tall one does not push the conversation away.
          img: ({ src, alt }: React.ComponentPropsWithoutRef<"img">) => (
            <a href={src} target="_blank" rel="noreferrer noopener" className="not-prose block">
              <img src={src} alt={alt ?? ""} loading="lazy" className="my-2 max-h-[28rem] max-w-full rounded-xl" />
            </a>
          ),
        }}
      >
        {withImages(text)}
      </Markdown>
    </div>
  );
}

// A turn's working-out, drawn as one line that opens onto its steps. Each step is
// a tool the turn called. While the turn runs the line names the newest step
// with the typing dots; once the reply is in it reads as a count and the time.
function ActivityDisclosure({ steps, seconds, live }: { steps: string[]; seconds: number | null; live: boolean }) {
  const [open, setOpen] = useState(!live);
  const summary = live
    ? (steps[steps.length - 1] ?? "I'm typing")
    : `Used ${steps.length} ${steps.length === 1 ? "tool" : "tools"}${seconds !== null ? ` · ${seconds}s` : ""}`;
  return (
    <div className="text-sm">
      <button
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-expanded={open}
        className="flex max-w-full items-center gap-2 rounded-lg py-1 text-left text-muted-foreground transition-colors hover:text-foreground"
      >
        {live && (
          <span className="flex shrink-0 gap-1" aria-hidden="true">
            <span className="h-1.5 w-1.5 animate-blink rounded-full bg-accent-500" />
            <span className="h-1.5 w-1.5 animate-blink rounded-full bg-accent-500 [animation-delay:0.15s]" />
            <span className="h-1.5 w-1.5 animate-blink rounded-full bg-accent-500 [animation-delay:0.3s]" />
          </span>
        )}
        <span className="truncate">{summary}</span>
        <ChevronDownIcon className={cn("h-3.5 w-3.5 shrink-0 transition-transform", open && "rotate-180")} />
      </button>
      {open && steps.length > 0 && (
        <ul className="mt-2 space-y-2 border-l border-border pl-4 text-[0.8125rem] text-muted-foreground">
          {steps.map((step, index) => {
            const running = live && index === steps.length - 1;
            return (
              <li key={`${index}-${step}`} className="flex items-center gap-2 leading-5">
                {running ? (
                  <span className="h-1.5 w-1.5 shrink-0 animate-blink rounded-full bg-accent-500" aria-hidden="true" />
                ) : (
                  <CheckIcon className="h-3.5 w-3.5 shrink-0 text-muted-foreground/70" />
                )}
                {step}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

// A reasoning message, collapsed until the owner opens it.
function ThoughtBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="text-sm">
      <button
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-expanded={open}
        className="flex items-center gap-1.5 rounded-lg py-1 text-muted-foreground transition-colors hover:text-foreground"
      >
        <span>Thinking</span>
        <ChevronDownIcon className={cn("h-3.5 w-3.5 shrink-0 transition-transform", open && "rotate-180")} />
      </button>
      {open && (
        <div className="mt-2 rounded-2xl border border-border px-4 py-3.5 text-muted-foreground">
          <MessageBody text={text.slice(THINKING_PREFIX.length)} isUserBubble={false} />
        </div>
      )}
    </div>
  );
}

// The conversation title, seamless with the page. Clicking it opens a small
// menu; Rename turns it into an input that saves on Enter or blur.
function ThreadTitle({ title, renamable, onRename }: { title: string; renamable: boolean; onRename: (next: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [draft, setDraft] = useState(title);
  // Enter and Escape both end the edit, and the blur that follows must not
  // save or cancel a second time. The ref is the one place that knows.
  const editingRef = useRef(false);

  function startEditing() {
    setMenuOpen(false);
    setDraft(title);
    editingRef.current = true;
    setEditing(true);
  }

  function finish(save: boolean) {
    if (!editingRef.current) return;
    editingRef.current = false;
    setEditing(false);
    const next = draft.trim();
    if (save && next && next !== title) onRename(next);
  }

  if (editing) {
    return (
      <input
        autoFocus
        aria-label="Chat name"
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onFocus={(event) => event.currentTarget.select()}
        onBlur={() => finish(true)}
        onKeyDown={(event) => {
          if (event.key === "Enter") finish(true);
          if (event.key === "Escape") finish(false);
        }}
        className="min-w-0 max-w-full rounded-lg bg-neutral-100 px-2 py-1 text-base font-medium tracking-tight outline-none ring-2 ring-ring/40"
      />
    );
  }

  return (
    <div className="relative min-w-0">
      <button
        type="button"
        disabled={!renamable}
        onClick={() => setMenuOpen((open) => !open)}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        className="flex max-w-full items-center gap-1 rounded-lg px-2 py-1 text-base font-medium tracking-tight transition-colors hover:bg-neutral-100 disabled:hover:bg-transparent"
      >
        <span className="truncate">{title}</span>
        {renamable && <ChevronDownIcon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
      </button>
      {menuOpen && (
        <>
          <div className="fixed inset-0 z-10" aria-hidden="true" onClick={() => setMenuOpen(false)} />
          <div role="menu" className="absolute left-0 top-full z-20 mt-1 min-w-[11rem] rounded-xl border border-border bg-background p-1 shadow-lift">
            <button
              type="button"
              role="menuitem"
              onClick={startEditing}
              className="flex w-full items-center rounded-lg px-3 py-2 text-left text-sm transition-colors hover:bg-neutral-100"
            >
              Rename
            </button>
          </div>
        </>
      )}
    </div>
  );
}

// The approval card: the sentence the owner is deciding on, the arguments
// behind it one tap away for anyone who wants to check, and the two answers.
// Details stay closed by default so the decision reads as one plain sentence.
export function ApprovalCard({ approval, onDecide }: { approval: PendingApproval; onDecide: (approved: boolean) => void }) {
  const [showDetails, setShowDetails] = useState(false);
  return (
    <div className="ml-[2.6rem] max-w-lg animate-fade-in-up self-start rounded-2xl bg-eg-ask p-[18px] text-sm text-eg-ask-ink">
      <div className="mb-2 flex items-center gap-2 text-xs font-semibold">
        <LockIcon className="h-3.5 w-3.5" />
        Needs your approval
      </div>
      <p className="mb-3 leading-relaxed">{approval.summary}</p>
      {approval.details && (
        <div className="mb-3.5">
          <button
            type="button"
            aria-expanded={showDetails}
            onClick={() => setShowDetails((open) => !open)}
            className="flex items-center gap-1 text-xs font-medium underline-offset-2 hover:underline"
          >
            {showDetails ? "Hide details" : "Show details"}
            <ChevronDownIcon className={cn("h-3.5 w-3.5 transition-transform", showDetails && "rotate-180")} />
          </button>
          {showDetails && (
            <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-xl bg-background/70 p-3 font-mono text-xs leading-relaxed text-foreground">
              {approval.details}
            </pre>
          )}
        </div>
      )}
      <div className="flex gap-2">
        <Button type="button" size="sm" className="rounded-xl" onClick={() => onDecide(true)}>
          Approve
        </Button>
        <Button type="button" size="sm" className="rounded-xl bg-neutral-100 text-foreground hover:bg-neutral-200" onClick={() => onDecide(false)}>
          Decline
        </Button>
      </div>
    </div>
  );
}

export function ChatPage({
  threadId,
  title,
  onSessionExpired,
  onMessageResolved,
  onThreadCreated,
  onRenamed,
  onOpenSidebar,
  onNewChat,
}: {
  threadId: string | null;
  title: string;
  onSessionExpired: () => void;
  onMessageResolved?: () => void;
  onThreadCreated?: (threadId: string) => void;
  onRenamed?: (title: string) => void;
  // Passed only while the rail is closed. On a phone the header carries the
  // menu control, so the chat does not leave an empty gutter for it.
  onOpenSidebar?: () => void;
  // Shown on a phone beside the title, so starting over takes one tap.
  onNewChat?: () => void;
}) {
  const [history, setHistory] = useState<ChatMessage[]>([]);
  // Whether the primary pointer is a finger, which decides whether a message's
  // footer is always shown (a finger has no hover to reveal it).
  const touch = usesTouchKeyboard();
  // pending holds our own just-sent messages that loadHistory hasn't
  // corroborated yet. The backend only durably records a turn once the
  // whole model turn finishes (see ConversationService.Record), so a
  // history refetch mid-turn -- e.g. on an SSE auto-reconnect while the
  // model is still thinking -- must never silently erase what the user
  // just typed by replacing it wholesale.
  const [pending, setPending] = useState<ChatMessage[]>([]);
  const [typing, setTyping] = useState(false);
  // liveSteps is the running turn's status lines ("Calling web_search..."),
  // shown as the turn's working-out. liveRef mirrors it for the SSE handlers,
  // which are bound once per thread and would otherwise read a stale copy.
  const [liveSteps, setLiveSteps] = useState<string[]>([]);
  const liveRef = useRef<string[]>([]);
  const turnStartRef = useRef<number | null>(null);
  // Finished turns' steps, keyed by the reply's index in history. The index
  // is what survives the history refetch that follows every reply.
  const [activityByIndex, setActivityByIndex] = useState<Record<number, Activity>>({});
  const historyCountRef = useRef(0);
  const [approvals, setApprovals] = useState<PendingApproval[]>([]);
  // Decided approvals, kept as a one-line record in the transcript so the
  // owner can see what they allowed after the card is gone.
  const [decisions, setDecisions] = useState<{ id: string; summary: string; approved: boolean }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement | null>(null);
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  // Whether the transcript is scrolled to its end. New replies follow the end
  // only while the owner is there; scrolled up, they keep their place.
  const [atBottom, setAtBottom] = useState(true);
  const atBottomRef = useRef(true);
  const [selection, clearSelection] = useSelectionAnchor(transcriptRef);
  const [quote, setQuote] = useState<Quote | null>(null);
  // The message a long press opened, on a touch screen: its actions in a sheet.
  const [pressedMessage, setPressedMessage] = useState<string | null>(null);
  const pressTimer = useRef<number | null>(null);
  function startMessagePress(event: React.PointerEvent<HTMLDivElement>) {
    const item = (event.target as Element).closest<HTMLElement>("[data-message-id]");
    cancelMessagePress();
    if (!item) return;
    const id = item.dataset.messageId ?? null;
    pressTimer.current = window.setTimeout(() => {
      pressTimer.current = null;
      setPressedMessage(id);
    }, LONG_PRESS_MS);
  }
  function cancelMessagePress() {
    if (pressTimer.current !== null) window.clearTimeout(pressTimer.current);
    pressTimer.current = null;
  }
  const threadRef = useRef<string | null>(threadId);
  const previousThreadId = useRef<string | null>(threadId);
  const messages = [...history, ...pending];
  const lastAssistantIndex = messages.map((message) => message.role).lastIndexOf("assistant");
  // previousUserIndex is the owner message a reply answers, or -1 if there is none.
  function previousUserIndex(index: number): number {
    for (let i = index - 1; i >= 0; i--) if (messages[i].role === "user") return i;
    return -1;
  }

  function loadHistory(targetThreadId = threadId) {
    if (!targetThreadId) return;
    getChatHistory(targetThreadId)
      .then((result) => {
        const rows = result.table_rows ?? [];
        const fetched = rows.map((row, index) => ({
          id: `history-${index}`,
          role: row[0] === "user" ? ("user" as const) : ("assistant" as const),
          text: row[1] ?? "",
          at: row[2] || undefined,
        }));
        setHistory(fetched);
        historyCountRef.current = fetched.length;
        // Drop any pending optimistic send the server has now caught up on;
        // keep the rest showing until it does.
        setPending((current) => current.filter((message) => !fetched.some((row) => row.role === "user" && row.text === message.text)));
      })
      .catch((err) => {
        if (err instanceof SessionExpiredError) onSessionExpired();
      });
  }

  useEffect(() => {
    const createdFromDraft = previousThreadId.current === null && threadId !== null;
    threadRef.current = threadId;
    setHistory([]);
    // The first send creates the durable thread and changes this prop while
    // its message is still in flight. Keep that optimistic message visible;
    // a normal switch between existing chats should reset the pane.
    if (!createdFromDraft) setPending([]);
    setApprovals([]);
    setDecisions([]);
    setTyping(false);
    liveRef.current = [];
    turnStartRef.current = null;
    setLiveSteps([]);
    setActivityByIndex({});
    historyCountRef.current = 0;
    setQuote(null);
    previousThreadId.current = threadId;
    if (!threadId) return;
    loadHistory();
    const source = new EventSource(`/api/chat/threads/${encodeURIComponent(threadId)}/stream`);

    // Records one status line of the running turn, skipping an immediate repeat.
    function pushStep(text: string | undefined) {
      if (!text || liveRef.current[liveRef.current.length - 1] === text) return;
      liveRef.current = [...liveRef.current, text];
      setLiveSteps(liveRef.current);
    }

    source.addEventListener("open", () => loadHistory());

    source.addEventListener("message", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      const text = event.text ?? "";
      const index = historyCountRef.current;
      setTyping(false);
      // Reasoning arrives before the reply it belongs to, so the turn's steps
      // are only closed off by the reply itself.
      if (!text.startsWith(THINKING_PREFIX)) {
        if (liveRef.current.length > 0) {
          const seconds = turnStartRef.current === null ? null : Math.round((Date.now() - turnStartRef.current) / 1000);
          const finished = { steps: liveRef.current, seconds };
          setActivityByIndex((current) => ({ ...current, [index]: finished }));
        }
        liveRef.current = [];
        turnStartRef.current = null;
        setLiveSteps([]);
      }
      historyCountRef.current = index + 1;
      setHistory((current) => [...current, { id: event.id ?? `msg-${current.length}`, role: "assistant", text }]);
      // A reply means the turn that recorded our pending send has finished;
      // reconcile now rather than waiting for the next reconnect.
      loadHistory();
      onMessageResolved?.();
    });

    source.addEventListener("typing", () => {
      turnStartRef.current ??= Date.now();
      setTyping(true);
    });

    source.addEventListener("progress", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      turnStartRef.current ??= Date.now();
      setTyping(true);
      pushStep(event.text);
    });

    source.addEventListener("edit", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      setHistory((current) => current.map((message) => (message.id === event.id ? { ...message, text: event.text ?? "" } : message)));
    });

    source.addEventListener("approval", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      if (event.approval) {
        setApprovals((current) => [...current, event.approval as PendingApproval]);
      }
    });

    source.onerror = () => {
      if (source.readyState === EventSource.CLOSED) {
        onSessionExpired();
      }
    };

    return () => source.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadId]);

  useEffect(() => {
    if (atBottomRef.current) bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  function onTranscriptScroll() {
    const host = transcriptRef.current;
    if (!host) return;
    const near = host.scrollHeight - host.scrollTop - host.clientHeight < 96;
    atBottomRef.current = near;
    setAtBottom(near);
  }

  function jumpToBottom() {
    atBottomRef.current = true;
    setAtBottom(true);
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }

  function rename(next: string) {
    if (!threadId) return;
    const previous = title;
    onRenamed?.(next);
    renameThread(threadId, next).catch((err) => {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      onRenamed?.(previous);
      setError(errorMessage(err, "Could not rename chat"));
    });
  }

  async function handleSend(text: string, quote: Quote | null) {
    // Sending is the owner asking to see the reply, so it brings them to the end.
    atBottomRef.current = true;
    setAtBottom(true);
    setPending((current) => [...current, { id: `local-${Date.now()}-${current.length}`, role: "user", text: withQuote(quote, text), at: new Date().toISOString() }]);
    try {
      let target = threadRef.current;
      if (!target) {
        target = await createThread();
        threadRef.current = target;
        onThreadCreated?.(target);
      }
      await sendChatMessage(target, text, quote ? { text: quote.text, own_message: quote.ownMessage } : null);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Failed to send"));
    }
  }

  // retry sends the owner's message behind a reply again, as a new turn. The
  // earlier exchange stays in the transcript, so a retry never rewrites history.
  function retry(userIndex: number) {
    const { quote: quoted, ownMessage, body } = splitQuote(messages[userIndex].text);
    handleSend(body, quoted === null ? null : { text: quoted, ownMessage });
  }

  async function handleStop() {
    if (!threadId) return;
    try {
      await stopChatTurn(threadId);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Could not stop the reply"));
    }
  }

  async function handleApproval(approvalId: string, approved: boolean) {
    const decided = approvals.find((approval) => approval.id === approvalId);
    if (decided) setDecisions((current) => [...current, { id: decided.id, summary: decided.summary, approved }]);
    setApprovals((current) => current.filter((approval) => approval.id !== approvalId));
    try {
      await approveChatDecision(approvalId, approved);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Failed to record decision"));
    }
  }

  const pressed = messages.find((message) => message.id === pressedMessage) ?? null;
  const pressedBody = pressed ? (pressed.role === "user" ? splitQuote(pressed.text).body : pressed.text) : "";
  return (
    <div className="app-canvas flex h-full flex-col">
      {/* No rule or fill: the header sits on the page, and only the title shows a
          soft highlight when hovered. */}
      <header className="flex h-14 shrink-0 items-center gap-2 px-4 sm:px-8">
        {onOpenSidebar && (
          <button
            type="button"
            onClick={onOpenSidebar}
            className="-ml-2 flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 md:hidden"
            aria-label="Open sidebar"
            title="Open sidebar"
          >
            <MenuIcon />
          </button>
        )}
        <ThreadTitle title={title} renamable={threadId !== null} onRename={rename} />
        {onNewChat && (
          <button
            type="button"
            onClick={onNewChat}
            className="ml-auto flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 md:hidden"
            aria-label="New chat"
            title="New chat"
          >
            <PlusIcon />
          </button>
        )}
      </header>
      <div className="relative flex min-h-0 flex-1 flex-col">
      <div
        ref={transcriptRef}
        onScroll={onTranscriptScroll}
        onPointerDown={touch ? startMessagePress : undefined}
        onPointerUp={cancelMessagePress}
        onPointerMove={cancelMessagePress}
        onPointerCancel={cancelMessagePress}
        onPointerLeave={cancelMessagePress}
        onContextMenu={touch ? (event) => event.preventDefault() : undefined}
        className="scrollbar-slim relative min-h-0 flex-1 overflow-y-auto"
      >
        {pressed && (
          <MessageSheet
            onReply={() => {
              setQuote(replyQuote(pressedBody, pressed.role === "assistant"));
              setPressedMessage(null);
            }}
            onCopy={async () => {
              await copyText(pressedBody);
              setPressedMessage(null);
            }}
            onClose={() => setPressedMessage(null)}
          />
        )}
        {selection && !touch && (
          <button
            type="button"
            // mousedown would collapse the selection before click fires.
            onPointerDown={(event) => event.stopPropagation()}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => {
              setQuote({ text: selection.text, ownMessage: selection.ownMessage });
              document.getSelection()?.removeAllRanges();
              clearSelection();
            }}
            style={{ top: selection.top, left: selection.left }}
            className={cn(
              "absolute z-10 flex -translate-y-[calc(100%+8px)] items-center gap-2 rounded-xl bg-foreground px-3.5 py-2 text-sm font-medium text-background shadow-lg",
              "hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
            )}
          >
            Reply
            <ReplyIcon className="h-4 w-4" />
          </button>
        )}
        <div className="mx-auto flex w-full max-w-4xl flex-col gap-7 px-4 py-8 sm:px-8 sm:py-10">
          {messages.length === 0 && !typing && (
            <div className="py-16 text-center">
              <p className="text-sm text-muted-foreground">Start by asking a question or describing a task.</p>
            </div>
          )}
          {messages.map((message, index) => {
            const isUser = message.role === "user";
            if (isUser) {
              const { quote: quoted, body } = splitQuote(message.text);
              return (
                <div key={message.id} data-message-id={message.id} data-role="user" className="group flex animate-fade-in-up flex-col items-end gap-1">
                  <div className="max-w-[88%] rounded-2xl bg-surface px-4 py-3 text-sm text-foreground sm:max-w-[72%] sm:px-5">
                    {quoted !== null && (
                      <div className="mb-2.5 flex items-start rounded-xl bg-black/5 px-3 py-2">
                        <span aria-hidden="true" className="mr-2.5 w-0.5 shrink-0 self-stretch rounded-full bg-muted-foreground/50" />
                        <p className="line-clamp-4 whitespace-pre-line break-words text-xs leading-5 text-foreground/75">{quoted}</p>
                      </div>
                    )}
                    <MessageBody text={body} isUserBubble />
                  </div>
                  <MessageFooter text={body} at={message.at} touch={touch} alwaysVisible={touch} />
                </div>
              );
            }
            if (message.text.startsWith(THINKING_PREFIX)) {
              return (
                <div key={message.id} data-role="assistant" className="animate-fade-in-up">
                  <ThoughtBlock text={message.text} />
                </div>
              );
            }
            const activity = activityByIndex[index];
            const isLatest = index === lastAssistantIndex;
            // Retry re-sends the owner message before this reply, and only on the
            // latest reply once the turn has finished.
            const retryFrom = isLatest && !typing ? previousUserIndex(index) : -1;
            return (
              <div key={message.id} data-message-id={message.id} data-role="assistant" className="group flex animate-fade-in-up flex-col gap-3 text-sm">
                {activity && <ActivityDisclosure steps={activity.steps} seconds={activity.seconds} live={false} />}
                <MessageBody text={message.text} isUserBubble={false} />
                <MessageFooter
                  text={message.text}
                  at={message.at}
                  touch={touch}
                  alwaysVisible={touch || isLatest}
                  onRetry={retryFrom >= 0 ? () => retry(retryFrom) : undefined}
                />
              </div>
            );
          })}
          {typing && <ActivityDisclosure steps={liveSteps} seconds={null} live />}
          {approvals.map((approval) => (
            <ApprovalCard key={approval.id} approval={approval} onDecide={(approved) => handleApproval(approval.id, approved)} />
          ))}
          {decisions.map((decision) => (
            <p key={decision.id} className="flex items-center gap-2 text-xs text-muted-foreground">
              <span className="font-medium text-foreground">
                {decision.approved ? "Approved" : "Declined"}
              </span>
              <span className="truncate">{decision.summary}</span>
            </p>
          ))}
          {error && (
            <p className="rounded-xl bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
          <div ref={bottomRef} />
        </div>
      </div>
      {/* The fade lets the transcript dissolve into the composer rather than
          stopping at a hard edge; the jump control appears once the owner has
          scrolled away from the latest reply. */}
      <div aria-hidden="true" className="pointer-events-none absolute inset-x-0 bottom-0 h-12 bg-gradient-to-t from-background to-transparent" />
      {!atBottom && (
        <button
          type="button"
          onClick={jumpToBottom}
          aria-label="Jump to latest"
          title="Jump to latest"
          className="absolute bottom-3 left-1/2 z-10 flex h-9 w-9 -translate-x-1/2 items-center justify-center rounded-full border border-border bg-background text-foreground shadow-lift transition-colors hover:bg-neutral-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <ArrowDownIcon className="h-4 w-4" />
        </button>
      )}
      </div>
      <Composer
        onSend={handleSend}
        onStop={handleStop}
        running={typing}
        onSessionExpired={onSessionExpired}
        quote={quote}
        onClearQuote={() => setQuote(null)}
      />
    </div>
  );
}
