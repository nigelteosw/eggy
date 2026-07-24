import { useEffect, useRef, useState } from "react";
import { ChatEvent, SessionExpiredError, approveChatDecision, getChatHistory, sendChatMessage } from "./api";

type ChatMessage = { id: string; role: "user" | "assistant"; text: string };
type PendingApproval = { id: string; summary: string };

export function ChatPage({
  threadId,
  onSessionExpired,
  onMessageResolved,
}: {
  threadId: string;
  onSessionExpired: () => void;
  onMessageResolved?: () => void;
}) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [typing, setTyping] = useState(false);
  const [approvals, setApprovals] = useState<PendingApproval[]>([]);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement | null>(null);

  function loadHistory() {
    getChatHistory(threadId)
      .then((result) => {
        const rows = result.table_rows ?? [];
        setMessages(
          rows.map((row, index) => ({
            id: `history-${index}`,
            role: row[0] === "user" ? "user" : "assistant",
            text: row[1] ?? "",
          })),
        );
      })
      .catch((err) => {
        if (err instanceof SessionExpiredError) onSessionExpired();
      });
  }

  useEffect(() => {
    setMessages([]);
    setApprovals([]);
    setTyping(false);
    loadHistory();
    const source = new EventSource(`/api/chat/threads/${encodeURIComponent(threadId)}/stream`);

    source.addEventListener("open", loadHistory);

    source.addEventListener("message", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      setTyping(false);
      setMessages((current) => [...current, { id: event.id ?? `msg-${current.length}`, role: "assistant", text: event.text ?? "" }]);
      onMessageResolved?.();
    });

    source.addEventListener("typing", () => setTyping(true));

    source.addEventListener("edit", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      setMessages((current) => current.map((message) => (message.id === event.id ? { ...message, text: event.text ?? "" } : message)));
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
    setMessages((current) => [...current, { id: `local-${current.length}`, role: "user", text }]);
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
            {message.text}
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
