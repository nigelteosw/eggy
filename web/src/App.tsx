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
  // Below the md breakpoint, the sidebar is an off-canvas overlay (there's
  // no room for a static 256px column next to the chat panel on a phone);
  // at md and up, it's always visible inline, so this flag is only read on
  // small screens (see the md:hidden/md:translate-x-0 classes below).
  const [sidebarOpen, setSidebarOpen] = useState(false);

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
        className="absolute right-4 top-4 z-40 rounded-full bg-white p-2 text-slate-500 shadow hover:text-slate-900"
        aria-label={view === "chat" ? "Open settings" : "Back to chat"}
      >
        {view === "chat" ? "⚙" : "💬"}
      </button>
      {view === "chat" ? (
        <>
          <button
            type="button"
            onClick={() => setSidebarOpen(true)}
            className="absolute left-4 top-4 z-40 rounded-full bg-white p-2 text-slate-500 shadow hover:text-slate-900 md:hidden"
            aria-label="Open chat list"
          >
            ☰
          </button>
          {sidebarOpen && (
            <div className="fixed inset-0 z-20 bg-black/30 md:hidden" onClick={() => setSidebarOpen(false)} aria-hidden="true" />
          )}
          <div
            className={`fixed inset-y-0 left-0 z-30 transition-transform duration-200 md:static md:translate-x-0 ${
              sidebarOpen ? "translate-x-0" : "-translate-x-full"
            }`}
          >
            <ThreadSidebar
              activeThreadId={activeThreadId}
              onSelect={(id) => {
                setActiveThreadId(id);
                setSidebarOpen(false);
              }}
              reloadKey={sidebarReloadKey}
            />
          </div>
          {activeThreadId ? (
            <div className="min-w-0 flex-1">
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
