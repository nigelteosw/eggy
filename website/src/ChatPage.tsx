import { useEffect, useRef, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ChatEvent, SessionExpiredError, approveChatDecision, createThread, getChatHistory, sendChatMessage } from "./api";
import { Composer, Quote } from "./Composer";
import { Button } from "./components/ui/button";
import { LockIcon, ReplyIcon } from "./components/ui/icons";
import { cn, errorMessage } from "./lib/utils";

type ChatMessage = { id: string; role: "user" | "assistant"; text: string };

// The reply prefix as events.Message.Prompt builds it on the backend. The
// optimistic bubble must be the exact string history will later hold, or the
// reconcile in loadHistory never drops it; and a recorded user turn is
// split back apart here so the transcript shows a quote, not brackets.
const REPLY_PREFIX = /^\[Replying to( your previous message)?: "([\s\S]*?)"\]\n\n([\s\S]*)$/;

function withQuote(quote: Quote | null, text: string): string {
  if (!quote) return text;
  return `[Replying to${quote.ownMessage ? " your previous message" : ""}: "${quote.text}"]\n\n${text}`;
}

function splitQuote(text: string): { quote: string | null; body: string } {
  const match = REPLY_PREFIX.exec(text);
  return match ? { quote: match[2], body: match[3] } : { quote: null, body: text };
}
type PendingApproval = { id: string; summary: string };
// Where the floating Reply button sits, relative to the scrolling transcript.
type SelectionAnchor = { text: string; ownMessage: boolean; top: number; left: number };

// Watches text selection within `container` and reports the selected text
// with a spot to hang a button on, or null when nothing useful is selected.
function useSelectionAnchor(container: React.RefObject<HTMLElement | null>) {
  const [anchor, setAnchor] = useState<SelectionAnchor | null>(null);

  useEffect(() => {
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
    // selectionchange fires while the mouse is still dragging; settle on
    // mouseup/keyup so the button does not chase the cursor.
    document.addEventListener("mouseup", update);
    document.addEventListener("keyup", update);
    document.addEventListener("selectionchange", update);
    return () => {
      document.removeEventListener("mouseup", update);
      document.removeEventListener("keyup", update);
      document.removeEventListener("selectionchange", update);
    };
  }, [container]);

  return [anchor, () => setAnchor(null)] as const;
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
        }}
      >
        {text}
      </Markdown>
    </div>
  );
}

export function ChatPage({
  threadId,
  title,
  sidebarOpen,
  onSessionExpired,
  onMessageResolved,
  onThreadCreated,
}: {
  threadId: string | null;
  title: string;
  sidebarOpen: boolean;
  onSessionExpired: () => void;
  onMessageResolved?: () => void;
  onThreadCreated?: (threadId: string) => void;
}) {
  const [history, setHistory] = useState<ChatMessage[]>([]);
  // pending holds our own just-sent messages that loadHistory hasn't
  // corroborated yet. The backend only durably records a turn once the
  // whole model turn finishes (see ConversationService.Record), so a
  // history refetch mid-turn -- e.g. on an SSE auto-reconnect while the
  // model is still thinking -- must never silently erase what the user
  // just typed by replacing it wholesale.
  const [pending, setPending] = useState<ChatMessage[]>([]);
  const [typing, setTyping] = useState(false);
  const [approvals, setApprovals] = useState<PendingApproval[]>([]);
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement | null>(null);
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  const [selection, clearSelection] = useSelectionAnchor(transcriptRef);
  const [quote, setQuote] = useState<Quote | null>(null);
  const threadRef = useRef<string | null>(threadId);
  const previousThreadId = useRef<string | null>(threadId);
  const messages = [...history, ...pending];

  function loadHistory(targetThreadId = threadId) {
    if (!targetThreadId) return;
    getChatHistory(targetThreadId)
      .then((result) => {
        const rows = result.table_rows ?? [];
        const fetched = rows.map((row, index) => ({
          id: `history-${index}`,
          role: row[0] === "user" ? ("user" as const) : ("assistant" as const),
          text: row[1] ?? "",
        }));
        setHistory(fetched);
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
    setTyping(false);
    setQuote(null);
    previousThreadId.current = threadId;
    if (!threadId) return;
    loadHistory();
    const source = new EventSource(`/api/chat/threads/${encodeURIComponent(threadId)}/stream`);

    source.addEventListener("open", () => loadHistory());

    source.addEventListener("message", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      setTyping(false);
      setHistory((current) => [...current, { id: event.id ?? `msg-${current.length}`, role: "assistant", text: event.text ?? "" }]);
      // A reply means the turn that recorded our pending send has finished;
      // reconcile now rather than waiting for the next reconnect.
      loadHistory();
      onMessageResolved?.();
    });

    source.addEventListener("typing", () => setTyping(true));

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
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  async function handleSend(text: string, quote: Quote | null) {
    setPending((current) => [...current, { id: `local-${Date.now()}-${current.length}`, role: "user", text: withQuote(quote, text) }]);
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

  async function handleApproval(approvalId: string, approved: boolean) {
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

  return (
    <div className="app-canvas flex h-full flex-col">
      <header className={cn("flex h-14 shrink-0 items-center bg-background px-4 shadow-[inset_0_-1px_0_hsl(var(--border))] sm:px-8", !sidebarOpen && "pl-14 sm:pl-14")}>
        <div className="min-w-0">
          <h1 className="truncate text-base font-medium tracking-tight">{title}</h1>
        </div>
      </header>
      <div ref={transcriptRef} className="scrollbar-slim relative flex-1 overflow-y-auto">
        {selection && (
          <button
            type="button"
            // mousedown would collapse the selection before click fires.
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => {
              setQuote({ text: selection.text, ownMessage: selection.ownMessage });
              document.getSelection()?.removeAllRanges();
              clearSelection();
            }}
            style={{ top: selection.top, left: selection.left }}
            className={cn(
              "absolute z-10 flex -translate-y-[calc(100%+8px)] animate-fade-in-up items-center gap-2 rounded-xl bg-foreground px-3.5 py-2 text-sm font-medium text-background shadow-lg",
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
          {messages.map((message) => {
            const isUser = message.role === "user";
            if (isUser) {
              const { quote: quoted, body } = splitQuote(message.text);
              return (
                <div key={message.id} data-role="user" className="flex animate-fade-in-up justify-end">
                  <div className="max-w-[88%] rounded-2xl bg-surface px-4 py-3 text-sm text-foreground sm:max-w-[72%] sm:px-5">
                    {quoted !== null && (
                      <div className="mb-2.5 flex items-start rounded-xl bg-black/5 px-3 py-2">
                        <span aria-hidden="true" className="mr-2.5 w-0.5 shrink-0 self-stretch rounded-full bg-muted-foreground/50" />
                        <p className="line-clamp-4 whitespace-pre-line break-words text-xs leading-5 text-foreground/75">{quoted}</p>
                      </div>
                    )}
                    <MessageBody text={body} isUserBubble />
                  </div>
                </div>
              );
            }
            return (
              <div key={message.id} data-role="assistant" className="flex animate-fade-in-up gap-3.5">
                <div
                  aria-hidden="true"
                  className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-accent-100 text-xs font-semibold text-accent-700"
                >
                  E
                </div>
                <div className="min-w-0 flex-1 text-sm">
                  <MessageBody text={message.text} isUserBubble={false} />
                </div>
              </div>
            );
          })}
          {typing && (
            <div className="flex items-center gap-2 pl-[2.6rem] text-xs text-muted-foreground">
              <span className="flex gap-1">
                <span className="h-1.5 w-1.5 animate-blink rounded-full bg-accent-500" />
                <span className="h-1.5 w-1.5 animate-blink rounded-full bg-accent-500 [animation-delay:0.15s]" />
                <span className="h-1.5 w-1.5 animate-blink rounded-full bg-accent-500 [animation-delay:0.3s]" />
              </span>
              Eggy is typing
            </div>
          )}
          {approvals.map((approval) => (
            <div
              key={approval.id}
              className="ml-[2.6rem] max-w-lg animate-fade-in-up self-start rounded-2xl bg-eg-ask p-[18px] text-sm text-eg-ask-ink"
            >
              <div className="mb-2 flex items-center gap-2 text-xs font-semibold">
                <LockIcon className="h-3.5 w-3.5" />
                Needs your approval
              </div>
              <p className="mb-3.5 leading-relaxed">{approval.summary}</p>
              <div className="flex gap-2">
                <Button
                  type="button"
                  size="sm"
                  className="rounded-xl"
                  onClick={() => handleApproval(approval.id, true)}
                >
                  Approve
                </Button>
                <Button
                  type="button"
                  size="sm"
                  className="rounded-xl bg-neutral-100 text-foreground hover:bg-neutral-200"
                  onClick={() => handleApproval(approval.id, false)}
                >
                  Not now
                </Button>
              </div>
            </div>
          ))}
          {error && (
            <p className="rounded-xl bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
          <div ref={bottomRef} />
        </div>
      </div>
      <Composer onSend={handleSend} onSessionExpired={onSessionExpired} quote={quote} onClearQuote={() => setQuote(null)} />
    </div>
  );
}
