import { useEffect, useState } from "react";
import { applyTheme, checkSession, getMode, type Mode, type Theme } from "./api";
import { LoginPage } from "./LoginPage";
import { ChatPage } from "./ChatPage";
import { ConfigPage } from "./ConfigPage";
import { TracesPage } from "./TracesPage";
import { SafeModePage } from "./SafeModePage";
import { ThreadSidebar } from "./ThreadSidebar";
import { PanelIcon } from "./components/ui/icons";
import { useStoredFlag } from "./components/ui/sidebar";
import { pathForView, viewForPath, type View } from "./routing";

type Status = "checking" | "authenticated" | "unauthenticated";

export function AppNavigation({ view, onNavigate }: { view: View; onNavigate: (view: View) => void }) {
  return (
    <header className="flex h-14 shrink-0 items-center gap-6 border-b bg-card px-4 sm:px-6">
      <span className="text-base font-semibold tracking-tight">
        Eggy<span className="ml-1 text-primary">.</span>
      </span>
      <nav aria-label="Main navigation" className="flex h-full gap-1 sm:gap-3">
        {(
          [
            ["chat", "Chat"],
            ["traces", "Traces"],
            ["config", "Settings"],
          ] as const
        ).map(([destination, label]) => (
          <a
            key={destination}
            href={pathForView(destination)}
            aria-current={view === destination ? "page" : undefined}
            onClick={(event) => {
              if (event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) {
                event.preventDefault();
                onNavigate(destination);
              }
            }}
            className={`flex items-center border-b-2 px-3 text-sm transition-colors ${view === destination ? "border-primary font-medium text-foreground" : "border-transparent text-muted-foreground hover:text-foreground"}`}
          >
            {label}
          </a>
        ))}
      </nav>
    </header>
  );
}

export function App() {
  const [status, setStatus] = useState<Status>("checking");
  const [mode, setMode] = useState<Mode>("normal");
  const [theme, setTheme] = useState<Theme>("dark");
  const [view, setView] = useState<View>(() =>
    viewForPath(typeof window === "undefined" ? "/" : window.location.pathname),
  );
  const [activeThreadId, setActiveThreadId] = useState<string | null>(null);
  const [activeThreadTitle, setActiveThreadTitle] = useState("New chat");
  const [draftChatOpen, setDraftChatOpen] = useState(false);
  const [sidebarReloadKey, setSidebarReloadKey] = useState(0);
  // Whether the chat rail is showing. It is one flag across both layouts,
  // remembered per device: below md the rail is an off-canvas overlay (there
  // is no room for a static column beside the transcript on a phone), and at
  // md and up it is an inline column that the owner can still close to give
  // the transcript the whole window. The stored default is open, so a first
  // visit lands on the list rather than on an empty pane.
  const [sidebarOpen, setSidebarOpen] = useStoredFlag("eggy.chat.sidebar", true);

  useEffect(() => {
    const handlePopState = () => setView(viewForPath(window.location.pathname));
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  function navigate(next: View) {
    const path = pathForView(next);
    if (window.location.pathname !== path) window.history.pushState({}, "", path);
    setView(next);
  }

  useEffect(() => {
    // The mode probe decides which app this is before the session decides
    // which screen: in safe mode chat and settings do not exist, so rendering
    // them and letting each request fail would be noise around the one thing
    // the owner can act on. A probe that itself fails is treated as normal --
    // an old server that predates the route still runs the agent.
    getMode()
      .then((probe) => {
        setMode(probe.mode);
        setTheme(probe.theme);
        applyTheme(probe.theme);
      })
      .catch(() => {
        setMode("normal");
        applyTheme("dark");
      })
      .finally(() => {
        checkSession()
          .then(() => setStatus("authenticated"))
          .catch(() => setStatus("unauthenticated"));
      });
  }, []);

  if (status === "checking") {
    return (
      <div className="app-canvas flex min-h-screen flex-col items-center justify-center gap-3 text-muted-foreground">
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
    <div className="flex h-dvh flex-col overflow-hidden bg-background">
      <AppNavigation view={view} onNavigate={navigate} />
      <div className="relative flex min-h-0 flex-1 overflow-hidden">
        {view === "chat" ? (
          <>
            {/* The reopen control, in the corner the collapse control just left.
                It is only rendered while the rail is closed, so the two never
                sit in the same place at the same time. */}
            {!sidebarOpen && (
              <button
                type="button"
                onClick={() => setSidebarOpen(true)}
                className="absolute left-2 top-1.5 z-40 flex h-11 w-11 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                aria-label="Open sidebar"
                title="Open sidebar"
              >
                <PanelIcon />
              </button>
            )}
            {sidebarOpen && (
              <div
                className="absolute inset-0 z-20 bg-foreground/25 backdrop-blur-[2px] md:hidden"
                onClick={() => setSidebarOpen(false)}
                aria-hidden="true"
              />
            )}
            {/* Closing the rail removes it from the flow at md and up rather
                than sliding it behind the transcript, so the chat column
                actually reclaims the width. Below md it stays an overlay. */}
            <div
              className={`absolute inset-y-0 left-0 z-30 shadow-lift transition-transform duration-200 ease-out md:static md:shadow-none md:translate-x-0 ${
                sidebarOpen ? "translate-x-0" : "-translate-x-full md:hidden"
              }`}
            >
              <ThreadSidebar
                activeThreadId={activeThreadId}
                onSelect={(id) => {
                  setActiveThreadId(id);
                  setDraftChatOpen(false);
                  // Only the overlay layout needs dismissing on a pick; at md
                  // and up the rail is a column the owner chose to keep open.
                  if (window.matchMedia("(max-width: 767px)").matches) setSidebarOpen(false);
                }}
                onStartNew={() => {
                  setActiveThreadId(null);
                  setDraftChatOpen(true);
                }}
                onActiveTitleChange={setActiveThreadTitle}
                onCollapse={() => setSidebarOpen(false)}
                onDeleted={(id) => {
                  // Only the open chat needs clearing; deleting some other row
                  // should leave the current conversation alone.
                  if (activeThreadId === id) setDraftChatOpen(true);
                  setActiveThreadId((current) => (current === id ? null : current));
                }}
                reloadKey={sidebarReloadKey}
                draftOpen={draftChatOpen}
              />
            </div>
            {activeThreadId || draftChatOpen ? (
              <div className="min-w-0 flex-1">
                <ChatPage
                  threadId={activeThreadId}
                  title={activeThreadTitle}
                  sidebarOpen={sidebarOpen}
                  onSessionExpired={onSessionExpired}
                  onMessageResolved={() => setSidebarReloadKey((key) => key + 1)}
                  onThreadCreated={(id) => {
                    setActiveThreadId(id);
                    setDraftChatOpen(false);
                  }}
                />
              </div>
            ) : (
              <div className="app-canvas flex flex-1 items-center justify-center px-6 text-center">
                <p className="text-sm text-muted-foreground">Select a chat or start a new one.</p>
              </div>
            )}
          </>
        ) : view === "traces" ? (
          <div className="min-h-0 flex-1">
            <TracesPage onSessionExpired={onSessionExpired} />
          </div>
        ) : (
          <div className="min-h-0 flex-1">
            <ConfigPage theme={theme} onThemeChange={setTheme} onSessionExpired={onSessionExpired} />
          </div>
        )}
      </div>
    </div>
  );
}
