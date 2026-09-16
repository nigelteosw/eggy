import { useEffect, useState } from "react";
import { applyTheme, checkSession, clearSession, getMode, logout, type Account, type Login, type Mode, type Theme } from "./api";
import { LoginPage } from "./LoginPage";
import { ChatPage } from "./ChatPage";
import { ConfigPage } from "./ConfigPage";
import { TracesPage } from "./TracesPage";
import { SafeModePage } from "./SafeModePage";
import { SetupPage } from "./SetupPage";
import { ThreadSidebar } from "./ThreadSidebar";
import { PanelIcon } from "./components/ui/icons";
import { useStoredFlag } from "./components/ui/sidebar";
import { cn } from "./lib/utils";
import { pathForView, viewForPath, type View } from "./routing";

type Status = "checking" | "setup" | "authenticated" | "unauthenticated";

export function AppNavigation({
  view,
  onNavigate,
  account,
  onLogout,
}: {
  view: View;
  onNavigate: (view: View) => void;
  // account is who is signed in, when the deployment has accounts. Shown
  // beside a sign-out control so two people sharing a browser can tell whose
  // panel this is before typing into it.
  account?: Account;
  onLogout?: () => void;
}) {
  return (
    <header className="flex h-14 min-w-0 shrink-0 items-center gap-3 bg-background sm:gap-6 px-4 shadow-[inset_0_-1px_0_hsl(var(--border))] sm:px-6">
      <span className="text-base font-semibold tracking-tight">
        Eggy<span className="ml-1 text-primary">.</span>
      </span>
      <nav aria-label="Main navigation" className="flex h-full shrink-0 items-center gap-1">
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
            className={cn(
              "flex h-9 items-center rounded-xl px-2.5 text-sm sm:px-3.5 font-medium transition-colors",
              view === destination
                ? "bg-accent-100 text-accent-700"
                : "text-muted-foreground hover:bg-neutral-100 hover:text-foreground",
            )}
          >
            {label}
          </a>
        ))}
      </nav>
      {account && (
        <div className="ml-auto flex min-w-0 items-center gap-2.5 text-sm">
          <span className="hidden truncate text-muted-foreground sm:inline" title={account.email}>
            {account.email}
          </span>
          <div
            aria-hidden="true"
            className="hidden h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-accent-100 text-xs font-semibold text-accent-700 sm:flex"
          >
            {account.email.charAt(0).toUpperCase()}
          </div>
          <button
            type="button"
            onClick={onLogout}
            className="shrink-0 whitespace-nowrap rounded-xl px-2.5 py-1.5 text-muted-foreground transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            Sign out
          </button>
        </div>
      )}
    </header>
  );
}

export function App() {
  const [status, setStatus] = useState<Status>("checking");
  const [mode, setMode] = useState<Mode>("normal");
  const [theme, setTheme] = useState<Theme>("dark");
  const [loginKind, setLoginKind] = useState<Login>("password");
  const [account, setAccount] = useState<Account | undefined>(undefined);
  // The callback's generic failure lands on "/?login=failed"; read once and
  // then taken out of the address bar so a reload does not repeat it.
  const [loginFailed] = useState(() => {
    if (typeof window === "undefined") return false;
    const failed = new URLSearchParams(window.location.search).get("login") === "failed";
    if (failed) window.history.replaceState({}, "", window.location.pathname);
    return failed;
  });
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
        setLoginKind(probe.login);
        applyTheme(probe.theme);
        if (probe.mode === "setup") {
          setStatus("setup");
          return false;
        }
        return true;
      })
      .catch(() => {
        setMode("normal");
        applyTheme("dark");
        return true;
      })
      .then((needsSession) => {
        if (!needsSession) return;
        checkSession()
          .then((session) => {
            setAccount(session.account);
            setStatus("authenticated");
          })
          .catch(() => setStatus("unauthenticated"));
      });
  }, []);

  // Leaving a session -- by choice or because the server said it is gone --
  // forgets everything the previous person had open: the thread, the draft,
  // the cached session. The next sign-in on this tab starts from nothing.
  function endSession() {
    clearSession();
    setAccount(undefined);
    setActiveThreadId(null);
    setActiveThreadTitle("New chat");
    setDraftChatOpen(false);
    setSidebarReloadKey((key) => key + 1);
    setStatus("unauthenticated");
  }

  async function signOut() {
    try {
      await logout();
    } finally {
      endSession();
    }
  }

  if (status === "checking") {
    return (
      <div className="app-canvas flex min-h-screen flex-col items-center justify-center gap-3 text-muted-foreground">
        <span className="h-6 w-6 animate-spin rounded-full border-2 border-border border-t-primary" />
        <span className="text-sm">Loading...</span>
      </div>
    );
  }
  if (status === "setup") return <SetupPage />;
  if (status === "unauthenticated") {
    return (
      <LoginPage
        login={loginKind}
        failed={loginFailed}
        onLoggedIn={() => {
          checkSession()
            .then((session) => setAccount(session.account))
            .catch(() => {});
          setStatus("authenticated");
        }}
      />
    );
  }

  const onSessionExpired = endSession;

  if (mode === "safe") {
    return <SafeModePage onSessionExpired={onSessionExpired} />;
  }

  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-background">
      <AppNavigation view={view} onNavigate={navigate} account={account} onLogout={signOut} />
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
                className="absolute left-2 top-1.5 z-40 flex h-11 w-11 items-center justify-center rounded-xl text-muted-foreground transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
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
          <div className="min-h-0 min-w-0 flex-1">
            <TracesPage onSessionExpired={onSessionExpired} />
          </div>
        ) : (
          <div className="min-h-0 min-w-0 flex-1">
            <ConfigPage theme={theme} onThemeChange={setTheme} onSessionExpired={onSessionExpired} />
          </div>
        )}
      </div>
    </div>
  );
}
