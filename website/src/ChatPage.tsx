import { useEffect, useRef, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ChatEvent, SessionExpiredError, approveChatDecision, getChatHistory, sendChatMessage } from "./api";
import { Button } from "./components/ui/button";
import { cn } from "./lib/utils";

type ChatMessage = { id: string; role: "user" | "assistant"; text: string };
type PendingApproval = { id: string; summary: string };

// Eggy's replies (and often what a user types back) are markdown -- bold,
// lists, code blocks, links. Rendering it raw is what made the web chat
// hard to read; this renders it properly instead, styled to fit a compact
// chat bubble rather than a full article (see tailwind.config.js's
// @tailwindcss/typography plugin for the prose classes below).
function MessageBody({ text, isUserBubble }: { text: string; isUserBubble: boolean }) {
  return (
    <div
      className={cn(
        "prose prose-sm max-w-none break-words leading-relaxed",
        "[&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
        "prose-pre:rounded-md prose-pre:text-[0.8125rem] prose-code:font-mono prose-code:text-[0.85em] prose-code:before:content-none prose-code:after:content-none",
        isUserBubble
          ? "prose-invert prose-a:text-primary-foreground prose-pre:bg-black/25 prose-code:text-primary-foreground"
          : "prose-headings:text-foreground prose-p:text-foreground prose-strong:text-foreground prose-a:text-primary prose-pre:bg-muted prose-pre:text-foreground prose-code:rounded prose-code:bg-muted prose-code:px-1 prose-code:py-0.5",
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
  onSessionExpired,
  onMessageResolved,
}: {
  threadId: string;
  title: string;
  onSessionExpired: () => void;
  onMessageResolved?: () => void;
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
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement | null>(null);
  const composer = useRef<HTMLTextAreaElement | null>(null);
  const messages = [...history, ...pending];

  function loadHistory() {
    getChatHistory(threadId)
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
    setHistory([]);
    setPending([]);
    setApprovals([]);
    setTyping(false);
    loadHistory();
    const source = new EventSource(`/api/chat/threads/${encodeURIComponent(threadId)}/stream`);

    source.addEventListener("open", loadHistory);

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

  async function handleSend(event: React.SyntheticEvent) {
    event.preventDefault();
    const text = draft.trim();
    if (!text) return;
    setDraft("");
    // The grown height is inline style, so clearing the value does not undo
    // it -- the box would stay several lines tall over a single-line draft.
    if (composer.current) composer.current.style.height = "auto";
    setPending((current) => [...current, { id: `local-${Date.now()}-${current.length}`, role: "user", text }]);
    try {
      await sendChatMessage(threadId, text);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Failed to send");
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
      setError(err instanceof Error ? err.message : "Failed to record decision");
    }
  }

  return (
    // bg-card/40 over the sidebar's bg-background, matching the config
    // panel's raised content column, so both screens are the same two
    // surfaces in the same order.
    <div className="flex h-full flex-col bg-card/40">
      {/* The config panel heads each section with its name; this is the chat
          equivalent, and it also gives the mobile menu button a bar to sit in
          rather than floating over the first message. */}
      <header className="flex h-14 shrink-0 items-center border-b border-border px-4 pl-16 sm:px-6 md:pl-6">
        <h1 className="truncate text-sm font-medium tracking-tight">{title}</h1>
      </header>
      <div className="scrollbar-slim flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 px-4 py-8 sm:px-6">
          {messages.length === 0 && !typing && (
            <div className="flex flex-col items-center gap-2 py-16 text-center">
              <span className="text-3xl">🥚</span>
              <p className="text-sm text-muted-foreground">Ask Eggy anything to get started.</p>
            </div>
          )}
          {messages.map((message) => {
            const isUser = message.role === "user";
            return (
              <div key={message.id} className={cn("flex animate-fade-in-up", isUser ? "justify-end" : "justify-start")}>
                <div
                  className={cn(
                    "max-w-[85%] px-4 py-2.5 text-sm sm:max-w-[80%]",
                    isUser
                      ? "rounded-2xl rounded-br-md bg-primary text-primary-foreground shadow-subtle"
                      : "rounded-2xl rounded-bl-md border border-border bg-card text-card-foreground shadow-card",
                  )}
                >
                  <MessageBody text={message.text} isUserBubble={isUser} />
                </div>
              </div>
            );
          })}
          {typing && (
            <div className="flex items-center gap-2 pl-1 text-xs text-muted-foreground">
              <span className="flex gap-1">
                <span className="h-1.5 w-1.5 animate-blink rounded-full bg-muted-foreground" />
                <span className="h-1.5 w-1.5 animate-blink rounded-full bg-muted-foreground [animation-delay:0.15s]" />
                <span className="h-1.5 w-1.5 animate-blink rounded-full bg-muted-foreground [animation-delay:0.3s]" />
              </span>
              Eggy is typing
            </div>
          )}
          {approvals.map((approval) => (
            <div
              key={approval.id}
              // Amber as a translucent wash over whatever surface is behind
              // it, rather than the fixed amber-50/amber-950 pair this had:
              // those were picked for the paper theme and turn into a glaring
              // light patch with unreadable text on charcoal.
              className="animate-fade-in-up self-start rounded-2xl rounded-bl-md border border-amber-500/40 bg-amber-500/10 p-4 text-sm shadow-card"
            >
              <div className="mb-3 flex items-start gap-2">
                <span className="mt-0.5 text-base leading-none">⚠️</span>
                <p className="text-foreground">{approval.summary}</p>
              </div>
              <div className="flex gap-2">
                <Button type="button" size="sm" onClick={() => handleApproval(approval.id, true)}>
                  Approve
                </Button>
                <Button type="button" size="sm" variant="outline" onClick={() => handleApproval(approval.id, false)}>
                  Reject
                </Button>
              </div>
            </div>
          ))}
          {error && (
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
          <div ref={bottomRef} />
        </div>
      </div>
      <form onSubmit={handleSend} className="shrink-0 border-t border-border px-4 py-4 sm:px-6">
        <div
          className={cn(
            "mx-auto flex max-w-2xl items-end gap-2 rounded-xl border border-input bg-card p-1.5 shadow-subtle transition-shadow",
            "focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/25",
          )}
        >
          {/*
            A textarea rather than an input, because a prompt is often several
            lines and the single-line field silently scrolled away everything
            but the last one. It grows with the content up to a cap, then
            scrolls -- Enter sends, Shift+Enter breaks the line.
          */}
          <textarea
            ref={composer}
            value={draft}
            rows={1}
            onChange={(event) => setDraft(event.target.value)}
            onInput={(event) => {
              const field = event.currentTarget;
              field.style.height = "auto";
              field.style.height = `${Math.min(field.scrollHeight, 200)}px`;
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                handleSend(event);
              }
            }}
            placeholder="Message Eggy..."
            className="scrollbar-slim max-h-[200px] flex-1 resize-none bg-transparent px-3 py-2 text-sm leading-relaxed outline-none placeholder:text-muted-foreground/70"
          />
          <Button type="submit" size="icon" disabled={!draft.trim()} aria-label="Send message" className="shrink-0 rounded-lg">
            <svg viewBox="0 0 20 20" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <path d="M3.4 10h13.2M11 4.4 16.6 10 11 15.6" />
            </svg>
          </Button>
        </div>
      </form>
    </div>
  );
}
