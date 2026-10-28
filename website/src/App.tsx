import { useEffect, useState } from "react";
import { applyTheme, checkSession, clearSession, getMode, logout, type Account, type Login, type Mode, type Theme } from "./api";
import { LoginPage } from "./LoginPage";
import { WebLoginLinkPage, afterWebLoginLink, currentUsernameFor, takeWebLoginToken } from "./WebLoginLinkPage";
import { ChatPage } from "./ChatPage";
import { SettingsDialog } from "./SettingsDialog";
import { TracesPage } from "./TracesPage";
import { SafeModePage } from "./SafeModePage";
import { runningExtensions } from "./extensions";
import { SchedulesPage } from "./SchedulesPage";
import { CustomizePage } from "./CustomizePage";
import { SetupPage } from "./SetupPage";
import { HomePage } from "./HomePage";
import { RestartBar } from "./RestartBar";
import { ThreadSidebar } from "./ThreadSidebar";
import { MenuIcon, PanelIcon } from "./components/ui/icons";
import { useStoredFlag } from "./components/ui/sidebar";
import { isSettingsPath, pathForView, viewForPath, type View } from "./routing";
import { isEdgeSwipe, useVisibleHeight } from "./lib/viewport";

type Status = "checking" | "setup" | "authenticated" | "unauthenticated";

export function App() {
  const [status, setStatus] = useState<Status>("checking");
  const [mode, setMode] = useState<Mode>("normal");
  const [theme, setTheme] = useState<Theme>("system");
  const [loginKind, setLoginKind] = useState<Login>("password");
  const [account, setAccount] = useState<Account | undefined>(undefined);
  // What the server says is switched on. Null until the session answers, so the
  // rail does not offer Finance before the page knows it exists.
  const [features, setFeatures] = useState<string[] | null>(null);
  // Whether config.yaml has changed since this process started. The panel
  // shows one restart bar for it, refreshed each time settings close.
  const [restartPending, setRestartPending] = useState(false);
  // A Telegram /web link lands on /auth/link#token=…: the token is taken
  // out of the address bar once, on the first render, and held in memory
  // until the person clicks Continue. A refresh afterwards finds no token
  // and has to ask Telegram for a new link, which is the intended cost.
  const [webLoginToken, setWebLoginToken] = useState<string | null>(() => {
    if (typeof window === "undefined" || window.location.pathname !== "/auth/link") return null;
    return takeWebLoginToken(window.location, window.history);
  });
  const [view, setView] = useState<View>(() =>
    viewForPath(typeof window === "undefined" ? "/" : window.location.pathname),
  );
  // Settings is a popup over the view, addressable at /settings. Opening on that
  // path shows the popup over the chat behind it.
  const [settingsOpen, setSettingsOpen] = useState(() =>
    typeof window !== "undefined" && isSettingsPath(window.location.pathname),
  );
  const [activeThreadId, setActiveThreadId] = useState<string | null>(null);
  const [activeThreadTitle, setActiveThreadTitle] = useState("New chat");
  const [draftChatOpen, setDraftChatOpen] = useState(false);
  const [sidebarReloadKey, setSidebarReloadKey] = useState(0);
  // The extension window open over the current view, by extension id. An extension opens
  // as a popup rather than replacing the view, so the owner returns to the same
  // chat or page when it closes.
  const [activeExtension, setActiveExtension] = useState<string | null>(null);
  // Whether the rail is showing. It is one flag across both layouts, remembered
  // per device: below md the rail is an off-canvas overlay (there is no room for
  // a static column beside the content on a phone), and at md and up it is an
  // inline column that the owner can close to give the content the whole
  // window. The stored default is open, so a first visit lands on the list.
  // The first-visit default depends on the screen: open on a desktop, where the
  // rail sits beside the content, but closed on a phone, where it would cover
  // the one thing the owner came for. An explicit choice is remembered after that.
  const [sidebarOpen, setSidebarOpen] = useStoredFlag(
    "eggy.chat.sidebar",
    typeof window === "undefined" || window.matchMedia("(min-width: 768px)").matches,
  );

  // The phone's gestures for the rail: a swipe from the left edge opens it, and
  // a swipe left closes it. Only a touch starts either, so a mouse is unaffected.
  const visibleH = useVisibleHeight();
  useEffect(() => {
    let start: { x: number; y: number } | null = null;
    const down = (event: TouchEvent) => {
      const touch = event.touches[0];
      start = touch ? { x: touch.clientX, y: touch.clientY } : null;
    };
    const up = (event: TouchEvent) => {
      const touch = event.changedTouches[0];
      if (!start || !touch || window.matchMedia("(min-width: 768px)").matches) return;
      const end = { x: touch.clientX, y: touch.clientY };
      if (!sidebarOpen && isEdgeSwipe(start, end)) setSidebarOpen(true);
      else if (sidebarOpen && start.x - end.x >= 60 && Math.abs(end.y - start.y) <= (start.x - end.x) / 2) setSidebarOpen(false);
      start = null;
    };
    window.addEventListener("touchstart", down, { passive: true });
    window.addEventListener("touchend", up, { passive: true });
    return () => {
      window.removeEventListener("touchstart", down);
      window.removeEventListener("touchend", up);
    };
  }, [sidebarOpen, setSidebarOpen]);

  useEffect(() => {
    const handlePopState = () => {
      setView(viewForPath(window.location.pathname));
      setSettingsOpen(isSettingsPath(window.location.pathname));
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  function navigate(next: View) {
    const path = pathForView(next);
    if (window.location.pathname !== path) window.history.pushState({}, "", path);
    setView(next);
  }

  // Settings opens over the current view and keeps its address, so the popup
  // can be shared. Closing it puts back the address of the view underneath.
  function openSettings() {
    if (!isSettingsPath(window.location.pathname)) window.history.pushState({}, "", "/settings");
    setSettingsOpen(true);
    if (window.matchMedia("(max-width: 767px)").matches) setSidebarOpen(false);
  }

  function closeSettings() {
    if (isSettingsPath(window.location.pathname)) window.history.replaceState({}, "", pathForView(view));
    setSettingsOpen(false);
    // Settings is where config changes are made, so closing it is when the
    // restart bar should learn whether one is now waiting.
    checkSession()
      .then((session) => setRestartPending(session.restartPending === true))
      .catch(() => {});
  }

  // Choosing a destination on a phone also dismisses the rail, which is an
  // overlay there. At md and up the rail stays as the owner left it.
  function goTo(next: View) {
    // Picking a destination leaves any extension that was filling the work area.
    setActiveExtension(null);
    navigate(next);
    if (window.matchMedia("(max-width: 767px)").matches) setSidebarOpen(false);
  }

  function openExtension(extensionId: string) {
    setActiveExtension(extensionId);
    if (window.matchMedia("(max-width: 767px)").matches) setSidebarOpen(false);
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
        applyTheme("system");
        return true;
      })
      .then((needsSession) => {
        if (!needsSession) return;
        checkSession()
          .then((session) => {
            setAccount(session.account);
            setFeatures(session.features ?? []);
            setRestartPending(session.restartPending === true);
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
    setFeatures(null);
    setActiveThreadId(null);
    setActiveThreadTitle("New chat");
    setDraftChatOpen(false);
    setActiveExtension(null);
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

  if (webLoginToken !== null) {
    return (
      <WebLoginLinkPage
        token={webLoginToken}
        currentUsername={account?.username ?? currentUsernameFor()}
        onSignedIn={() => {
          setWebLoginToken(null);
          afterWebLoginLink()
            .then((session) => {
              setAccount(session.account);
              setFeatures(session.features ?? []);
            setRestartPending(session.restartPending === true);
              setStatus("authenticated");
            })
            .catch(() => setStatus("unauthenticated"));
          navigate("chat");
        }}
      />
    );
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
        onLoggedIn={() => {
          checkSession()
            .then((session) => {
              setAccount(session.account);
              setFeatures(session.features ?? []);
            setRestartPending(session.restartPending === true);
            })
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

  // Only an extension whose feature is running can be open; a stale id from before
  // a restart finds nothing and the view underneath shows.
  const activeExtensionDef = runningExtensions(features ?? []).find((extension) => extension.id === activeExtension);
  // A chat with a thread or draft open draws its own header, which carries the
  // menu control on a phone; every other view gets the top bar instead.
  const chatHeaderVisible = !activeExtensionDef && view === "chat" && (activeThreadId !== null || draftChatOpen);

  return (
    <div className="relative flex h-dvh flex-col overflow-hidden bg-background md:flex-row" style={visibleH ? { height: visibleH } : undefined}>
      {sidebarOpen && (
        <div
          className="absolute inset-0 z-20 bg-foreground/25 backdrop-blur-[2px] md:hidden"
          onClick={() => setSidebarOpen(false)}
          aria-hidden="true"
        />
      )}
      {/* Closing the rail removes it from the flow at md and up rather than
          sliding it behind the content, so the content reclaims the width.
          Below md it stays an overlay. */}
      <div
        className={`absolute inset-y-0 left-0 z-30 transition-transform duration-200 ease-out md:static md:translate-x-0 ${
          sidebarOpen ? "translate-x-0 shadow-lift md:shadow-none" : "-translate-x-full md:hidden"
        }`}
      >
        <ThreadSidebar
          activeThreadId={activeThreadId}
          onSelect={(id) => {
            setActiveThreadId(id);
            setDraftChatOpen(false);
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
          view={view}
          onNavigate={goTo}
          features={features ?? []}
          onOpenExtension={openExtension}
          onOpenSettings={openSettings}
          account={account}
          onLogout={signOut}
        />
      </div>

      {/* At md and up, the reopen control holds its own gutter, so no page has to
          leave room for it under its heading. On a phone that gutter would be
          empty width, so the control becomes a menu button in a top bar instead,
          and a chat's own header carries it. */}
      {!sidebarOpen && (
        <div className="hidden w-14 shrink-0 justify-center pt-1.5 md:flex">
          <button
            type="button"
            onClick={() => setSidebarOpen(true)}
            className="flex h-11 w-11 items-center justify-center rounded-xl text-muted-foreground transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            aria-label="Open sidebar"
            title="Open sidebar"
          >
            <PanelIcon />
          </button>
        </div>
      )}

      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {restartPending && <RestartBar onSessionExpired={onSessionExpired} />}
        {!sidebarOpen && !chatHeaderVisible && (
          <div className="flex h-14 shrink-0 items-center px-2 md:hidden">
            <button
              type="button"
              onClick={() => setSidebarOpen(true)}
              className="flex h-11 w-11 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              aria-label="Open sidebar"
              title="Open sidebar"
            >
              <MenuIcon />
            </button>
          </div>
        )}
        <div className="flex min-h-0 min-w-0 flex-1">
        {activeExtensionDef ? (
          // An extension takes the whole work area beside the rail. It draws its
          // own heading and close control, so there is no shared top bar.
          <div className="min-h-0 min-w-0 flex-1 overflow-hidden">
            <activeExtensionDef.Panel onSessionExpired={onSessionExpired} onClose={() => setActiveExtension(null)} />
          </div>
        ) : view === "home" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <HomePage
              onSessionExpired={onSessionExpired}
              onNavigate={(next) => goTo(next)}
              onOpenSettings={openSettings}
              onNewChat={() => {
                setActiveThreadId(null);
                setDraftChatOpen(true);
                goTo("chat");
              }}
            />
          </div>
        ) : view === "chat" ? (
          activeThreadId || draftChatOpen ? (
            <div className="min-w-0 flex-1">
              <ChatPage
                threadId={activeThreadId}
                title={activeThreadTitle}
                onSessionExpired={onSessionExpired}
                onMessageResolved={() => setSidebarReloadKey((key) => key + 1)}
                onRenamed={(next) => {
                  setActiveThreadTitle(next);
                  setSidebarReloadKey((key) => key + 1);
                }}
                onThreadCreated={(id) => {
                  setActiveThreadId(id);
                  setDraftChatOpen(false);
                }}
                onOpenSidebar={sidebarOpen ? undefined : () => setSidebarOpen(true)}
                onNewChat={() => {
                  setActiveThreadId(null);
                  setDraftChatOpen(true);
                }}
              />
            </div>
          ) : (
            <div className="app-canvas flex flex-1 items-center justify-center px-6 text-center">
              <p className="text-sm text-muted-foreground">Select a chat or start a new one.</p>
            </div>
          )
        ) : view === "scheduled" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <SchedulesPage onSessionExpired={onSessionExpired} />
          </div>
        ) : view === "customize" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <CustomizePage onSessionExpired={onSessionExpired} features={features ?? []} />
          </div>
        ) : view === "traces" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <TracesPage onSessionExpired={onSessionExpired} />
          </div>
        ) : null}
        </div>
      </div>
      <SettingsDialog
        open={settingsOpen}
        onClose={closeSettings}
        theme={theme}
        onThemeChange={setTheme}
        features={features ?? []}
        onSessionExpired={onSessionExpired}
      />

    </div>
  );
}
