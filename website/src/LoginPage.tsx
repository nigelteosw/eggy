import { useState } from "react";
import { login, type Login } from "./api";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD } from "./components/ui/form";
import { errorMessage } from "./lib/utils";

// The page is one of two: the single owner's password form, or, for an
// accounts deployment, an ordinary link to the server's Google Sign-In start
// route. The link is deliberately not a script -- the browser leaves for
// Google and comes back with a session cookie; nothing here touches a token.
// failed is the callback's one generic outcome: which check refused the
// sign-in is logged on the server, never shown.
export function LoginPage({ login: loginKind, failed, onLoggedIn }: { login: Login; failed: boolean; onLoggedIn: () => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await login(email, password);
      onLoggedIn();
    } catch (err) {
      setError(errorMessage(err, "Login failed"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="app-canvas relative flex min-h-screen items-center justify-center overflow-hidden px-4">
      {/* A soft wash of the primary behind the card, so the otherwise empty
          login screen isn't a flat expanse of off-white. */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute -top-40 left-1/2 h-[32rem] w-[32rem] -translate-x-1/2 rounded-full bg-primary/10 blur-3xl"
      />
      <div className="relative w-full max-w-sm animate-fade-in-up">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-accent-100 text-2xl">🥚</div>
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">Welcome back</h1>
            <p className="mt-1 text-sm text-neutral-700">Sign in to the assistant running on your machine.</p>
          </div>
        </div>

        <div className="rounded-3xl bg-neutral-100 p-6 shadow-lift">
          {loginKind === "google" ? (
            <div className="flex flex-col gap-4">
              {failed && (
                <ErrorBanner>
                  Sign-in was not completed. Use the Google account you were invited with, and try again.
                </ErrorBanner>
              )}
              <a
                href="/auth/google/start"
                className="inline-flex h-12 w-full items-center justify-center rounded-xl bg-primary px-4 text-sm font-semibold text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              >
                Sign in with Google
              </a>
              <p className="text-center text-xs text-neutral-700">Only invited Google accounts can sign in.</p>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="email">Email</Label>
                <Input
                  id="email"
                  type="email"
                  autoComplete="username"
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                  required
                  className={FIELD}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="password">Password</Label>
                <Input
                  id="password"
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  required
                  className={FIELD}
                />
              </div>
              {error && (
                <ErrorBanner>
                  {error}
                </ErrorBanner>
              )}
              <Button type="submit" disabled={submitting} className="mt-1 h-12 w-full rounded-xl text-base font-semibold">
                {submitting ? "Signing in..." : "Sign in"}
              </Button>
            </form>
          )}
        </div>
        <p className="mt-4 text-center text-xs text-neutral-700">
          This panel only talks to the machine it runs on.
          {loginKind !== "google" && " The password is the one in your config.yaml."}
        </p>
      </div>
    </div>
  );
}
