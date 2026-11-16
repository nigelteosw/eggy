import { useEffect, useState, type ReactNode } from "react";
import { applyLook, applyTheme, checkSession, clearLook, clearSession, getAgent, getMode, logout, writes, type Account, type Login, type Mode, type Theme } from "../lib/api";
import { isTheme } from "../lib/theme";
import { LoginPage } from "../auth/LoginPage";
import { WebLoginLinkPage, afterWebLoginLink, currentUsernameFor, takeWebLoginToken } from "../auth/WebLoginLinkPage";
import { ChatPage } from "../chat/ChatPage";
import { ConfigPage, type SectionId } from "../settings/ConfigPage";
import { SettingsDialog } from "../settings/SettingsDialog";
import { TracesPage } from "../traces/TracesPage";
import { SafeModePage } from "./SafeModePage";
import { runningExtensions } from "../extensions";
import { SchedulesPage } from "../schedules/SchedulesPage";
import { CustomizePage } from "../customize/CustomizePage";
import { SetupPage } from "../auth/SetupPage";
import { HomePage } from "../chat/HomePage";
import { RestartBar } from "./RestartBar";
import { ThreadSidebar } from "../chat/ThreadSidebar";
import { PanelIcon } from "../components/ui/icons";
import { useStoredFlag } from "../components/ui/sidebar";
import { pathForView, viewForPath, type View } from "./routing";
import { isEdgeSwipe, useVisibleFrame, useWide } from "../lib/viewport";

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
  // shows one restart bar for it, rechecked after each write.
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
  const visible = useVisibleFrame();
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
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  // On a wide screen Settings is a popup over the page the owner was on, so
  // that page is remembered and stays drawn underneath; closing the popup
  // returns to it. On a phone Settings is a page of its own.
  const wide = useWide();
  const [underView, setUnderView] = useState<View>(() => (view === "settings" ? "chat" : view));
  const shownView: View = view === "settings" && wide ? underView : view;

  function navigate(next: View) {
    const path = pathForView(next);
    if (window.location.pathname !== path) window.history.pushState({}, "", path);
    if (next === "settings" && view !== "settings") setUnderView(view);
    setView(next);
  }

  // Settings opens on My settings, or on the section a link elsewhere names.
  // The key remounts the page, so a second link to another section lands there.
  const [settingsSection, setSettingsSection] = useState<{ id: SectionId; key: number }>({ id: "personal", key: 0 });
  function openSettings(section: SectionId = "personal") {
    setSettingsSection((current) => ({ id: section, key: current.key + 1 }));
    goTo("settings");
  }

  // A write may have put config.yaml out of step with the running process, so
  // each one asks the server again. Several saves in a row cost one check.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const recheck = () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        checkSession()
          .then((session) => setRestartPending(session.restartPending === true))
          .catch(() => {});
      }, 300);
    };
    writes.addEventListener("write", recheck);
    return () => {
      clearTimeout(timer);
      writes.removeEventListener("write", recheck);
    };
  }, []);

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
          .catch(() => {
            // Nobody is signed in, so no one's look applies: the cached one
            // belonged to whoever used this browser last.
            clearLook();
            setStatus("unauthenticated");
          });
      });
  }, []);

  // Once signed in, the person's own palette replaces the deployment default
  // the probe applied, so a Dark choice follows them to every browser. An
  // account that has not chosen keeps the default.
  useEffect(() => {
    if (status !== "authenticated") return;
    getAgent()
      .then((selection) => {
        if (isTheme(selection.theme)) {
          setTheme(selection.theme);
          applyTheme(selection.theme);
        }
        if (selection.look) applyLook(selection.look);
      })
      .catch(() => {});
  }, [status]);

  // Leaving a session -- by choice or because the server said it is gone --
  // forgets everything the previous person had open: the thread, the draft,
  // the cached session. The next sign-in on this tab starts from nothing.
  function endSession() {
    clearSession();
    clearLook();
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
      <div className="app-canvas flex min-h-dvh flex-col items-center justify-center gap-3 text-muted-foreground">
        <span className="h-6 w-6 animate-spin rounded-full border-2 border-border border-t-primary" />
        <span className="text-small">Loading...</span>
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

  return (
    <div className="relative flex h-dvh flex-col overflow-hidden bg-background md:flex-row" style={visible ? { height: visible.height, top: visible.top } : undefined}>
      {sidebarOpen && (
        <div
          className="absolute inset-0 z-20 bg-foreground/25 backdrop-blur-[2px] md:hidden"
          onClick={() => setSidebarOpen(false)}
          aria-hidden="true"
        />
      )}
      {/* The rail slides in and out from the left; RailFrame says how. */}
      <RailFrame open={sidebarOpen}>
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
          view={shownView}
          onNavigate={goTo}
          features={features ?? []}
          onOpenExtension={openExtension}
          onOpenSettings={() => openSettings()}
          account={account}
          onLogout={signOut}
          theme={theme}
          onThemeChange={setTheme}
        />
      </RailFrame>

      {/* At md and up, the reopen control holds its own gutter, so no page has to
          leave room for it under its heading. On a phone that gutter would be
          empty width, so the control becomes a menu button in a top bar instead,
          and a chat's own header carries it. */}
      {/* The reopen control's gutter widens as the rail closes and narrows as
          it opens, on the rail's curve, so the page moves once, not twice. */}
      <div
        {...({ inert: sidebarOpen ? "" : undefined } as Record<string, string | undefined>)}
        className={`hidden shrink-0 justify-center overflow-hidden pt-1.5 transition-[width,opacity] duration-300 ease-[cubic-bezier(0.32,0.72,0,1)] motion-reduce:transition-none md:flex ${
          sidebarOpen ? "w-0 opacity-0" : "w-14 opacity-100"
        }`}
      >
        <button
          type="button"
          onClick={() => setSidebarOpen(true)}
          className="flex h-11 w-11 shrink-0 items-center justify-center rounded-control text-muted-foreground transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          aria-label="Open sidebar"
          title="Open sidebar"
        >
          <PanelIcon />
        </button>
      </div>

      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {restartPending && <RestartBar onSessionExpired={onSessionExpired} />}
        <div className="flex min-h-0 min-w-0 flex-1">
        {activeExtensionDef ? (
          // An extension takes the whole work area beside the rail. It draws its
          // own heading and close control, so there is no shared top bar.
          <div className="min-h-0 min-w-0 flex-1 overflow-hidden">
            <activeExtensionDef.Panel onSessionExpired={onSessionExpired} onClose={() => setActiveExtension(null)} onOpenSidebar={() => setSidebarOpen(true)} />
          </div>
        ) : shownView === "home" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <HomePage
              name={account?.name || account?.username}
              onOpenSidebar={() => setSidebarOpen(true)}
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
        ) : shownView === "chat" ? (
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
              <p className="text-small text-muted-foreground">Select a chat or start a new one.</p>
            </div>
          )
        ) : shownView === "scheduled" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <SchedulesPage onSessionExpired={onSessionExpired} onOpenSidebar={() => setSidebarOpen(true)} />
          </div>
        ) : shownView === "customize" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <CustomizePage onSessionExpired={onSessionExpired} features={features ?? []} onOpenSidebar={() => setSidebarOpen(true)} />
          </div>
        ) : shownView === "traces" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <TracesPage onSessionExpired={onSessionExpired} onOpenSidebar={() => setSidebarOpen(true)} />
          </div>
        ) : shownView === "settings" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <ConfigPage
              key={settingsSection.key}
              initialSection={settingsSection.id}
              theme={theme}
              onThemeChange={setTheme}
              onSessionExpired={onSessionExpired}
              onOpenSidebar={() => setSidebarOpen(true)}
            />
          </div>
        ) : null}
        </div>
      </div>

      {view === "settings" && wide && (
        <SettingsDialog
          section={settingsSection.id}
          sectionKey={settingsSection.key}
          onClose={() => goTo(underView)}
          theme={theme}
          onThemeChange={setTheme}
          restartPending={restartPending}
          onSessionExpired={onSessionExpired}
        />
      )}
    </div>
  );
}

// RailFrame holds the rail. Below md it is an overlay pinned to the viewport's
// height (absolute inset-y-0), and the inner wrapper passes that height on
// (h-full) so ThreadSidebar's own h-full resolves: without it the sidebar grows
// with its chat list and the account footer falls off the screen. At md and up
// its grid column grows from 0fr to 1fr while the rail inside translates from
// -100% to 0 on the same curve, so the rail's right edge rides the column's
// edge: the page beside it gives up and reclaims the width smoothly, and
// nothing overlaps. A closed rail stays mounted (so it can animate) but inert,
// out of the tab order and the accessibility tree.
export function RailFrame({ open, children }: { open: boolean; children: ReactNode }) {
  return (
    <div
      {...({ inert: open ? undefined : "" } as Record<string, string | undefined>)}
      data-rail-frame
      className={`absolute inset-y-0 left-0 z-30 shrink-0 transition-[transform,grid-template-columns] duration-300 ease-[cubic-bezier(0.32,0.72,0,1)] motion-reduce:transition-none md:static md:grid md:translate-x-0 ${
        open ? "translate-x-0 shadow-lift md:grid-cols-[1fr] md:shadow-none" : "-translate-x-full md:grid-cols-[0fr]"
      }`}
    >
      {/* w-max keeps the rail its own width while min-w-0 lets the column
          shrink under it, so -100% is the rail's width, not the column's. */}
      <div
        data-rail-inner
        className={`h-full w-max min-w-0 transition-transform duration-300 ease-[cubic-bezier(0.32,0.72,0,1)] motion-reduce:transition-none ${
          open ? "md:translate-x-0" : "md:-translate-x-full"
        }`}
      >
        {children}
      </div>
    </div>
  );
}
