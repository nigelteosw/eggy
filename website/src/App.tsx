import { useEffect, useState } from "react";
import { checkSession, getMode, type Mode } from "./api";
import { LoginPage } from "./LoginPage";
import { ChatPage } from "./ChatPage";
import { ConfigPage } from "./ConfigPage";
import { SafeModePage } from "./SafeModePage";
import { ThreadSidebar } from "./ThreadSidebar";

type Status = "checking" | "authenticated" | "unauthenticated";
type View = "chat" | "config";

export function App() {
  const [status, setStatus] = useState<Status>("checking");
  const [mode, setMode] = useState<Mode>("normal");
  const [view, setView] = useState<View>("chat");
  const [activeThreadId, setActiveThreadId] = useState<string | null>(null);
  const [sidebarReloadKey, setSidebarReloadKey] = useState(0);
  // Below the md breakpoint, the sidebar is an off-canvas overlay (there's
  // no room for a static 256px column next to the chat panel on a phone);
  // at md and up, it's always visible inline, so this flag is only read on
  // small screens (see the md:hidden/md:translate-x-0 classes below).
  const [sidebarOpen, setSidebarOpen] = useState(false);

  useEffect(() => {
    // The mode probe decides which app this is before the session decides
    // which screen: in safe mode chat and settings do not exist, so rendering
    // them and letting each request fail would be noise around the one thing
    // the owner can act on. A probe that itself fails is treated as normal --
    // an old server that predates the route still runs the agent.
    getMode()
      .then(setMode)
      .catch(() => setMode("normal"))
      .finally(() => {
        checkSession()
          .then(() => setStatus("authenticated"))
          .catch(() => setStatus("unauthenticated"));
      });
  }, []);

  if (status === "checking") {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-3 bg-background text-muted-foreground">
        <span className="h-6 w-6 animate-spin rounded-full border-2 border-border border-t-primary" />
        <span className="text-sm">Loading...</span>
      </div>
    );
  }
  if (status === "unauthenticated") {
    return <LoginPage onLoggedIn={() => setStatus("authenticated")} />;
  }

  const onSessionExpired = () => setStatus("unauthenticated");

  if (mode === "safe") {
    return <SafeModePage onSessionExpired={onSessionExpired} />;
  }

  return (
    <div className="relative flex h-screen overflow-hidden bg-background">
      <button
        type="button"
        onClick={() => setView(view === "chat" ? "config" : "chat")}
        className="absolute right-4 top-4 z-40 flex h-9 w-9 items-center justify-center rounded-full border border-border bg-card text-muted-foreground shadow-subtle transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        aria-label={view === "chat" ? "Open settings" : "Back to chat"}
      >
        {view === "chat" ? (
          <svg viewBox="0 0 20 20" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="10" cy="10" r="2.6" />
            <path d="M16.1 12.1a1.3 1.3 0 0 0 .26 1.44l.05.05a1.6 1.6 0 1 1-2.26 2.26l-.05-.05a1.3 1.3 0 0 0-1.44-.26 1.3 1.3 0 0 0-.79 1.19v.14a1.6 1.6 0 0 1-3.2 0v-.07a1.3 1.3 0 0 0-.85-1.19 1.3 1.3 0 0 0-1.44.26l-.05.05a1.6 1.6 0 1 1-2.26-2.26l.05-.05a1.3 1.3 0 0 0 .26-1.44 1.3 1.3 0 0 0-1.19-.79h-.14a1.6 1.6 0 0 1 0-3.2h.07a1.3 1.3 0 0 0 1.19-.85 1.3 1.3 0 0 0-.26-1.44l-.05-.05a1.6 1.6 0 1 1 2.26-2.26l.05.05a1.3 1.3 0 0 0 1.44.26h.07a1.3 1.3 0 0 0 .79-1.19v-.14a1.6 1.6 0 1 1 3.2 0v.07a1.3 1.3 0 0 0 .79 1.19 1.3 1.3 0 0 0 1.44-.26l.05-.05a1.6 1.6 0 1 1 2.26 2.26l-.05.05a1.3 1.3 0 0 0-.26 1.44v.07a1.3 1.3 0 0 0 1.19.79h.14a1.6 1.6 0 0 1 0 3.2h-.07a1.3 1.3 0 0 0-1.19.79Z" />
          </svg>
        ) : (
          <svg viewBox="0 0 20 20" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
            <path d="M17 12.1a1.6 1.6 0 0 1-1.6 1.6H5.8L2.6 16.9V4.3A1.6 1.6 0 0 1 4.2 2.7h11.2A1.6 1.6 0 0 1 17 4.3Z" />
          </svg>
        )}
      </button>
      {view === "chat" ? (
        <>
          <button
            type="button"
            onClick={() => setSidebarOpen(true)}
            className="absolute left-4 top-4 z-40 flex h-9 w-9 items-center justify-center rounded-full border border-border bg-card text-muted-foreground shadow-subtle transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 md:hidden"
            aria-label="Open chat list"
          >
            <svg viewBox="0 0 20 20" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round">
              <path d="M3.5 6h13M3.5 10h13M3.5 14h13" />
            </svg>
          </button>
          {sidebarOpen && (
            <div
              className="fixed inset-0 z-20 bg-foreground/25 backdrop-blur-[2px] md:hidden"
              onClick={() => setSidebarOpen(false)}
              aria-hidden="true"
            />
          )}
          <div
            className={`fixed inset-y-0 left-0 z-30 shadow-lift transition-transform duration-200 ease-out md:static md:shadow-none md:translate-x-0 ${
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
            <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center">
              <span className="text-3xl">🥚</span>
              <p className="text-sm text-muted-foreground">Select a chat, or start a new one.</p>
            </div>
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
