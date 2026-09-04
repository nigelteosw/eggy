import { useEffect, useRef, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ChatEvent, SessionExpiredError, approveChatDecision, getChatHistory, sendChatMessage } from "./api";
import { Composer } from "./Composer";
import { Button } from "./components/ui/button";
import { cn } from "./lib/utils";

type ChatMessage = { id: string; role: "user" | "assistant"; text: string };
type PendingApproval = { id: string; summary: string };

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
}: {
  threadId: string;
  title: string;
  sidebarOpen: boolean;
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
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement | null>(null);
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

  async function handleSend(text: string) {
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
    <div className="app-canvas flex h-full flex-col">
      <header className={cn("flex h-14 shrink-0 items-center border-b bg-background px-4 sm:px-8", !sidebarOpen && "pl-14 sm:pl-14")}>
        <div className="min-w-0">
          <h1 className="truncate text-base font-medium tracking-tight">{title}</h1>
        </div>
      </header>
      <div className="scrollbar-slim flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-4xl flex-col gap-7 px-4 py-8 sm:px-8 sm:py-10">
          {messages.length === 0 && !typing && (
            <div className="py-16 text-center">
              <p className="text-sm text-muted-foreground">Start by asking a question or describing a task.</p>
            </div>
          )}
          {messages.map((message) => {
            const isUser = message.role === "user";
            return (
              <div key={message.id} className={cn("flex animate-fade-in-up", isUser ? "justify-end" : "justify-start")}>
                <div
                  className={cn(
                    "text-sm",
                    isUser
                      ? "max-w-[88%] rounded-lg bg-primary px-4 py-3 text-primary-foreground sm:max-w-[72%] sm:px-5"
                      : "w-full",
                  )}
                >
                  <div className={cn(!isUser && "min-w-0")}>
                    <MessageBody text={message.text} isUserBubble={isUser} />
                  </div>
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
              className="animate-fade-in-up self-start rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 text-sm"
            >
              <p className="mb-3 text-foreground">{approval.summary}</p>
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
      <Composer onSend={handleSend} onSessionExpired={onSessionExpired} />
    </div>
  );
}
