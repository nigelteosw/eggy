import { useEffect, useRef, useState } from "react";
import { checkSession, clearSession, currentSession, redeemWebLoginLink } from "./api";
import { Button } from "./components/ui/button";
import { ErrorBanner } from "./components/ui/error-banner";
import { errorMessage } from "./lib/utils";

// The landing page for a Telegram /web link. The token rides in the URL
// fragment, which never reaches the server; this page takes it out of the
// address bar on first render, holds it in memory only, and spends it with
// one explicit click. Nothing is consumed by loading the page, so a link
// preview, a prefetch, or a curious tap on the wrong device does nothing.

// takeWebLoginToken reads the token out of the fragment and strips it from
// the browser's history entry in the same step, so a reload or a copied
// address no longer carries it. Anything that is not the shape the server
// mints is discarded.
export function takeWebLoginToken(location: Pick<Location, "hash" | "pathname">,
  history: Pick<History, "replaceState">): string {
  const token = new URLSearchParams(location.hash.slice(1)).get("token") ?? "";
  history.replaceState(null, "", location.pathname);
  return /^[A-Za-z0-9_-]{43}$/.test(token) ? token : "";
}

export type WebLoginLinkStatus = "ready" | "missing" | "pending" | "done" | "failed";

// WebLoginLinkPage is the confirmation screen. token is what the caller took
// from the fragment; currentUsername, when set, is who this browser is
// already signed in as, shown so switching accounts is never silent.
export function WebLoginLinkPage({
  token,
  currentUsername,
  onSignedIn,
  redeem = redeemWebLoginLink,
}: {
  token: string;
  currentUsername?: string;
  onSignedIn: () => void;
  redeem?: (token: string) => Promise<unknown>;
}) {
  const held = useRef(token);
  const [status, setStatus] = useState<WebLoginLinkStatus>(token ? "ready" : "missing");
  const [error, setError] = useState<string | null>(null);

  // The token leaves memory with the page, success or not.
  useEffect(() => () => { held.current = ""; }, []);

  async function proceed() {
    const current = held.current;
    if (!current || status === "pending" || status === "done") return;
    setStatus("pending");
    setError(null);
    try {
      await redeem(current);
      held.current = "";
      setStatus("done");
      onSignedIn();
    } catch (err) {
      // One click, one attempt: a network failure may or may not have
      // spent the link, and retrying blind could sign in twice or never.
      // The person asks Telegram for a new link instead.
      held.current = "";
      setStatus("failed");
      setError(errorMessage(err, "This sign-in link is invalid, expired, or already used."));
    }
  }

  return (
    <div className="app-canvas relative flex min-h-screen items-center justify-center overflow-hidden px-4">
      <div className="relative w-full max-w-sm animate-fade-in-up">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-accent-100 text-2xl">🥚</div>
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">Sign in from Telegram</h1>
            <p className="mt-1 text-sm text-neutral-700">This link was requested with /web in a Telegram chat.</p>
          </div>
        </div>
        <div className="flex flex-col gap-4 rounded-3xl bg-neutral-100 p-6 shadow-lift">
          {status === "missing" && (
            <ErrorBanner>This link is incomplete. Send /web to Eggy in Telegram again and open the new link.</ErrorBanner>
          )}
          {status === "failed" && error && <ErrorBanner>{error}</ErrorBanner>}
          {status === "done" && <p className="text-sm">Signed in. Opening the panel…</p>}
          {(status === "ready" || status === "pending") && (
            <>
              <p className="text-sm">
                Continue using the account that requested this Telegram link. This may replace your current sign-in.
              </p>
              {currentUsername && (
                <p className="text-sm text-neutral-700">
                  This browser is currently signed in as <strong>{currentUsername}</strong>.
                </p>
              )}
              <Button type="button" onClick={proceed} disabled={status === "pending"} className="h-12 w-full rounded-xl text-base font-semibold">
                {status === "pending" ? "Signing in…" : "Continue"}
              </Button>
              <p className="text-xs text-neutral-700">The link works once and expires five minutes after it was sent.</p>
            </>
          )}
          {(status === "failed" || status === "missing") && (
            <a href="/" className="text-center text-sm text-primary underline-offset-4 hover:underline">
              Go to the sign-in page
            </a>
          )}
        </div>
      </div>
    </div>
  );
}

// currentUsernameFor is the small bit of state the page shows about an
// existing session: nothing is fetched for it, the cached session answers.
export function currentUsernameFor(): string | undefined {
  return currentSession()?.account?.username;
}

// afterWebLoginLink forgets whatever identity was cached and re-reads the
// session the new cookie establishes before the panel renders as anyone.
export async function afterWebLoginLink() {
  clearSession();
  return checkSession();
}
