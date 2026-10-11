import { useEffect, useState } from "react";
import { applyTheme, checkSession, clearSession, getMode, logout, type Account, type Login, type Mode, type Theme } from "./api";
import { LoginPage } from "./LoginPage";
import { WebLoginLinkPage, afterWebLoginLink, currentUsernameFor, takeWebLoginToken } from "./WebLoginLinkPage";
import { ChatPage } from "./ChatPage";
import { ConfigPage } from "./ConfigPage";
import { TracesPage } from "./TracesPage";
import { SafeModePage } from "./SafeModePage";
import { runningPlugins } from "./plugins";
import { SchedulesPage } from "./SchedulesPage";
import { SetupPage } from "./SetupPage";
import { ThreadSidebar } from "./ThreadSidebar";
import { CloseIcon, PanelIcon } from "./components/ui/icons";
import { useStoredFlag } from "./components/ui/sidebar";
import { pathForView, viewForPath, type View } from "./routing";

type Status = "checking" | "setup" | "authenticated" | "unauthenticated";

export function App() {
  const [status, setStatus] = useState<Status>("checking");
  const [mode, setMode] = useState<Mode>("normal");
  const [theme, setTheme] = useState<Theme>("dark");
  const [loginKind, setLoginKind] = useState<Login>("password");
  const [account, setAccount] = useState<Account | undefined>(undefined);
  // What the server says is switched on. Null until the session answers, so the
  // rail does not offer Finance before the page knows it exists.
  const [features, setFeatures] = useState<string[] | null>(null);
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
  const [activeThreadId, setActiveThreadId] = useState<string | null>(null);
  const [activeThreadTitle, setActiveThreadTitle] = useState("New chat");
  const [draftChatOpen, setDraftChatOpen] = useState(false);
  const [sidebarReloadKey, setSidebarReloadKey] = useState(0);
  // The plugin window open over the current view, by plugin id. A plugin opens
  // as a popup rather than replacing the view, so the owner returns to the same
  // chat or page when it closes.
  const [activePlugin, setActivePlugin] = useState<string | null>(null);
  // Whether the rail is showing. It is one flag across both layouts, remembered
  // per device: below md the rail is an off-canvas overlay (there is no room for
  // a static column beside the content on a phone), and at md and up it is an
  // inline column that the owner can close to give the content the whole
  // window. The stored default is open, so a first visit lands on the list.
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

  // Choosing a destination on a phone also dismisses the rail, which is an
  // overlay there. At md and up the rail stays as the owner left it.
  function goTo(next: View) {
    // Picking a destination leaves any plugin that was filling the work area.
    setActivePlugin(null);
    navigate(next);
    if (window.matchMedia("(max-width: 767px)").matches) setSidebarOpen(false);
  }

  function openPlugin(pluginId: string) {
    setActivePlugin(pluginId);
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
        applyTheme("dark");
        return true;
      })
      .then((needsSession) => {
        if (!needsSession) return;
        checkSession()
          .then((session) => {
            setAccount(session.account);
            setFeatures(session.features ?? []);
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
    setActivePlugin(null);
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

  // Only a plugin whose feature is running can be open; a stale id from before
  // a restart finds nothing and the view underneath shows.
  const activePluginDef = runningPlugins(features ?? []).find((plugin) => plugin.id === activePlugin);

  return (
    <div className="relative flex h-dvh overflow-hidden bg-background">
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
        className={`absolute inset-y-0 left-0 z-30 shadow-lift transition-transform duration-200 ease-out md:static md:shadow-none md:translate-x-0 ${
          sidebarOpen ? "translate-x-0" : "-translate-x-full md:hidden"
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
          onOpenPlugin={openPlugin}
          account={account}
          onLogout={signOut}
        />
      </div>

      {/* With the rail closed, the reopen control holds its own gutter, so no
          page has to leave room for it under its heading. */}
      {!sidebarOpen && (
        <div className="flex w-14 shrink-0 justify-center pt-1.5">
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

      <div className="flex min-h-0 min-w-0 flex-1">
        {activePluginDef ? (
          // A plugin takes the whole work area beside the rail, with its own
          // header and a close control that returns to the view underneath.
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            <div className="flex h-14 shrink-0 items-center gap-2 bg-background pl-5 pr-2 shadow-[inset_0_-1px_0_hsl(var(--border))]">
              <h2 className="flex-1 truncate text-[14.5px] font-semibold tracking-tight">{activePluginDef.label}</h2>
              <button
                type="button"
                onClick={() => setActivePlugin(null)}
                aria-label={`Close ${activePluginDef.label}`}
                title="Close"
                className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              >
                <CloseIcon />
              </button>
            </div>
            <div className="min-h-0 flex-1 overflow-hidden">
              <activePluginDef.Panel onSessionExpired={onSessionExpired} />
            </div>
          </div>
        ) : view === "chat" ? (
          activeThreadId || draftChatOpen ? (
            <div className="min-w-0 flex-1">
              <ChatPage
                threadId={activeThreadId}
                title={activeThreadTitle}
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
          )
        ) : view === "scheduled" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <SchedulesPage onSessionExpired={onSessionExpired} />
          </div>
        ) : view === "traces" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <TracesPage onSessionExpired={onSessionExpired} />
          </div>
        ) : (
          <div className="min-h-0 min-w-0 flex-1">
            <ConfigPage
              theme={theme}
              onThemeChange={setTheme}
              onSessionExpired={onSessionExpired}
              features={features ?? []}
            />
          </div>
        )}
      </div>

    </div>
  );
}
