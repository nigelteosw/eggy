import { useEffect, useRef, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ChatEvent, SessionExpiredError, approveChatDecision, getChatHistory, sendChatMessage } from "./api";

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
      className={`prose prose-sm max-w-none break-words [&>*:first-child]:mt-0 [&>*:last-child]:mb-0 ${
        isUserBubble ? "prose-invert" : ""
      }`}
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
  onSessionExpired,
  onMessageResolved,
}: {
  threadId: string;
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

  async function handleSend(event: React.FormEvent) {
    event.preventDefault();
    const text = draft.trim();
    if (!text) return;
    setDraft("");
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
    <div className="flex h-full flex-col bg-slate-100">
      <div className="mx-auto flex w-full max-w-2xl flex-1 flex-col gap-3 overflow-y-auto p-6">
        {messages.map((message) => (
          <div
            key={message.id}
            className={`max-w-[80%] rounded-lg px-4 py-2 text-sm ${
              message.role === "user" ? "self-end bg-slate-900 text-white" : "self-start bg-white text-slate-900 shadow"
            }`}
          >
            <MessageBody text={message.text} isUserBubble={message.role === "user"} />
          </div>
        ))}
        {typing && <div className="self-start text-xs text-slate-400">Eggy is typing...</div>}
        {approvals.map((approval) => (
          <div key={approval.id} className="self-start rounded-lg border border-amber-300 bg-amber-50 p-4 text-sm shadow">
            <p className="mb-3 text-slate-800">{approval.summary}</p>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => handleApproval(approval.id, true)}
                className="rounded bg-slate-900 px-3 py-1 text-white"
              >
                Approve
              </button>
              <button
                type="button"
                onClick={() => handleApproval(approval.id, false)}
                className="rounded border border-slate-300 px-3 py-1 text-slate-700"
              >
                Reject
              </button>
            </div>
          </div>
        ))}
        {error && <p className="text-sm text-red-600">{error}</p>}
        <div ref={bottomRef} />
      </div>
      <form onSubmit={handleSend} className="border-t border-slate-200 bg-white p-4">
        <div className="mx-auto flex max-w-2xl gap-2">
          <input
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder="Message Eggy..."
            className="flex-1 rounded border border-slate-300 px-3 py-2"
          />
          <button type="submit" className="rounded bg-slate-900 px-4 py-2 text-white">
            Send
          </button>
        </div>
      </form>
    </div>
  );
}
