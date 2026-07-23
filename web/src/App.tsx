import { useEffect, useState } from "react";
import { checkSession } from "./api";
import { LoginPage } from "./LoginPage";
import { ChatPage } from "./ChatPage";
import { ConfigPage } from "./ConfigPage";

type Status = "checking" | "authenticated" | "unauthenticated";
type View = "chat" | "config";

export function App() {
  const [status, setStatus] = useState<Status>("checking");
  const [view, setView] = useState<View>("chat");

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
    <div className="relative min-h-screen">
      <button
        type="button"
        onClick={() => setView(view === "chat" ? "config" : "chat")}
        className="absolute right-4 top-4 z-10 rounded-full bg-white p-2 text-slate-500 shadow hover:text-slate-900"
        aria-label={view === "chat" ? "Open settings" : "Back to chat"}
      >
        {view === "chat" ? "⚙" : "💬"}
      </button>
      {view === "chat" ? <ChatPage onSessionExpired={onSessionExpired} /> : <ConfigPage onSessionExpired={onSessionExpired} />}
    </div>
  );
}
