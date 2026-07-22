import { useEffect, useState } from "react";
import { checkSession } from "./api";
import { LoginPage } from "./LoginPage";
import { ConfigPage } from "./ConfigPage";

type Status = "checking" | "authenticated" | "unauthenticated";

export function App() {
  const [status, setStatus] = useState<Status>("checking");

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
  return <ConfigPage onSessionExpired={() => setStatus("unauthenticated")} />;
}
