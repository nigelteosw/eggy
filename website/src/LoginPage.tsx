import { useState } from "react";
import { login, type Login } from "./api";
import { Button } from "./components/ui/button";
import { ClockIcon, LockIcon, ReplyIcon } from "./components/ui/icons";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { EggyBrand } from "./components/ui/brand";
import { ErrorBanner } from "./components/ui/error-banner";
import { FIELD } from "./components/ui/form";
import { errorMessage } from "./lib/utils";

const FEATURES = [
  { icon: ReplyIcon, label: "Chat", desc: "Conversations with Eggy on the web and in Telegram." },
  { icon: ClockIcon, label: "Scheduled", desc: "Tasks Eggy runs later, which you can review and cancel." },
  { icon: LockIcon, label: "Approvals", desc: "Changes that need your sign-off wait for your decision." },
];

// The username/password form. The username is the account ID -- or, for
// the account the deployment environment signs in, the alias the operator
// configured. When the server cannot identify anyone (a safe mode without
// its database) the form is replaced by that fact rather than a control that
// cannot work.
export function LoginPage({ login: loginKind, onLoggedIn }: { login: Login; onLoggedIn: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await login(username, password);
      setPassword("");
      onLoggedIn();
    } catch (err) {
      setError(errorMessage(err, "Login failed"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="login-pearl-bg flex min-h-dvh w-full flex-col overflow-y-auto text-foreground lg:flex-row">
      {/* Brand panel. Above the form on a phone, beside it on a wide screen. */}
      <div className="pearl-black order-2 flex shrink-0 flex-col justify-between gap-12 px-8 py-10 text-white lg:order-none lg:flex-1 lg:px-16 lg:py-14">
        <EggyBrand />

        <div className="max-w-xl">
          <h1 className="font-brand text-3xl font-semibold leading-tight tracking-tight sm:text-4xl">
            Your assistant, running
            <br />
            on your own machine.
          </h1>
          <p className="mt-4 max-w-md text-base leading-relaxed text-white/60">
            Chat with Eggy, set up what it does for you later, and approve the changes that matter.
          </p>

          <ul className="mt-10 space-y-5">
            {FEATURES.map(({ icon: Icon, label, desc }) => (
              <li key={label} className="flex items-start gap-3.5">
                <div className="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-white/[0.07] text-white/80">
                  <Icon className="h-4 w-4" />
                </div>
                <div>
                  <div className="text-sm font-medium text-white">{label}</div>
                  <div className="text-sm text-white/55">{desc}</div>
                </div>
              </li>
            ))}
          </ul>
        </div>

        <p className="hidden text-xs text-white/40 lg:block">Your conversations stay on the machine Eggy runs on.</p>
      </div>

      {/* Sign-in panel */}
      <div className="order-1 flex flex-1 items-center justify-center px-5 py-10 lg:order-none lg:max-w-[480px] lg:px-12">
        <div className="relative w-full max-w-sm animate-fade-in-up">
          <div className="mb-7">
            <h2 className="font-brand text-2xl font-semibold tracking-tight">Welcome back</h2>
            <p className="mt-1.5 text-sm text-neutral-700">Sign in to the assistant running on your machine.</p>
          </div>

          <div className="rounded-3xl bg-background/80 p-6 shadow-lift backdrop-blur-sm">
            {loginKind === "unavailable" ? (
              <ErrorBanner>
                Eggy is in safe mode and cannot identify anyone right now. Repair config.yaml on the host, then sign in.
              </ErrorBanner>
            ) : (
              <form onSubmit={handleSubmit} className="flex flex-col gap-4">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="username">Username</Label>
                  <Input
                    id="username"
                    type="text"
                    autoComplete="username"
                    autoCapitalize="none"
                    spellCheck={false}
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
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
                {error && <ErrorBanner>{error}</ErrorBanner>}
                <Button type="submit" disabled={submitting} className="mt-1 h-12 w-full rounded-xl text-base font-semibold">
                  {submitting ? "Signing in..." : "Sign in"}
                </Button>
              </form>
            )}
          </div>

          <p className="mt-4 text-center text-xs text-neutral-700">
            This panel only talks to the machine it runs on. On a phone, send /web to Eggy in Telegram for a one-tap
            sign-in link.
          </p>
        </div>
      </div>
    </div>
  );
}
