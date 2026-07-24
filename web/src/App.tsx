import { useEffect, useState } from "react";
import { checkSession } from "./api";
import { LoginPage } from "./LoginPage";
import { ChatPage } from "./ChatPage";
import { ConfigPage } from "./ConfigPage";
import { ThreadSidebar } from "./ThreadSidebar";

type Status = "checking" | "authenticated" | "unauthenticated";
type View = "chat" | "config";

export function App() {
  const [status, setStatus] = useState<Status>("checking");
  const [view, setView] = useState<View>("chat");
  const [activeThreadId, setActiveThreadId] = useState<string | null>(null);
  const [sidebarReloadKey, setSidebarReloadKey] = useState(0);

  useEffect(() => {
    checkSession()
      .then(() => setStatus("authenticated"))
      .catch(() => setStatus("unauthenticated"));
  }, []);

  if (status === "checking") {
    return <div className="flex min-h-screen items-center justify-center text-slate-500">Loading...</div>;
  }
  if (status === "unauthenticated") {
    return <LoginPage onLoggedIn={() => setStatus("authenticated")} />;
  }

  const onSessionExpired = () => setStatus("unauthenticated");

  return (
    <div className="relative flex h-screen overflow-hidden">
      <button
        type="button"
        onClick={() => setView(view === "chat" ? "config" : "chat")}
        className="absolute right-4 top-4 z-10 rounded-full bg-white p-2 text-slate-500 shadow hover:text-slate-900"
        aria-label={view === "chat" ? "Open settings" : "Back to chat"}
      >
        {view === "chat" ? "⚙" : "💬"}
      </button>
      {view === "chat" ? (
        <>
          <ThreadSidebar activeThreadId={activeThreadId} onSelect={setActiveThreadId} reloadKey={sidebarReloadKey} />
          {activeThreadId ? (
            <div className="flex-1">
              <ChatPage
                threadId={activeThreadId}
                onSessionExpired={onSessionExpired}
                onMessageResolved={() => setSidebarReloadKey((key) => key + 1)}
              />
            </div>
          ) : (
            <div className="flex flex-1 items-center justify-center text-slate-400">Select a chat, or start a new one.</div>
          )}
        </>
      ) : (
        <div className="flex-1 overflow-y-auto">
          <ConfigPage onSessionExpired={onSessionExpired} />
        </div>
      )}
    </div>
  );
}
