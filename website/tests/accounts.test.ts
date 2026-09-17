import { afterEach, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  addAccount,
  checkSession,
  clearSession,
  currentSession,
  getMode,
  logout,
  removeAccount,
  resetAccountBinding,
  sendChatMessage,
  setExpectedGoogleEmail,
  setLoginClient,
  createTelegramPairing,
  unlinkTelegram,
  createDiscordLink,
  unlinkDiscord,
  getDiscord,
  setDiscord,
  clearDiscordToken,
} from "../src/api";
import { LoginPage } from "../src/LoginPage";
import { AccountsCard, AccountsList, ConvertForm, DiscordLinkControl, ResetBindingConfirm, TelegramLinkControl } from "../src/AccountsCard";
import { DiscordCard, describeDiscord } from "../src/DiscordCard";
import { GoogleIdentity } from "../src/GoogleCard";
import { ConfigPage } from "../src/ConfigPage";
import { AppNavigation } from "../src/App";

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
  clearSession();
});

type Call = { path: string; method: string; headers: Record<string, string>; body: string };

function fakeFetch(respond: (call: Call) => unknown): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (input, init) => {
    const headers: Record<string, string> = {};
    for (const [key, value] of Object.entries(init?.headers ?? {})) headers[key.toLowerCase()] = String(value);
    const call: Call = { path: String(input), method: init?.method ?? "GET", headers, body: String(init?.body ?? "") };
    calls.push(call);
    return new Response(JSON.stringify(respond(call) ?? { state: "success" }), { status: 200 });
  }) as typeof fetch;
  return calls;
}

test("the mode probe says which way in exists", async () => {
  fakeFetch(() => ({ mode: "normal", theme: "dark", login: "google" }));
  const probe = await getMode();
  expect(probe.login).toBe("google");
  fakeFetch(() => ({ mode: "normal", theme: "dark" }));
  expect((await getMode()).login).toBe("password");
});

test("google login renders an ordinary link and a failed-login notice, never a password form", () => {
  const html = renderToStaticMarkup(createElement(LoginPage, { login: "google", failed: true, onLoggedIn: () => {} }));
  expect(html).toContain('href="/auth/google/start"');
  expect(html).toContain("Sign in with Google");
  expect(html).not.toContain('type="password"');
  expect(html).toContain("Sign-in was not completed");

  const password = renderToStaticMarkup(createElement(LoginPage, { login: "password", failed: false, onLoggedIn: () => {} }));
  expect(password).toContain('type="password"');
  expect(password).not.toContain("/auth/google/start");
});

test("the session carries the account and its csrf token, and mutating requests send it", async () => {
  const calls = fakeFetch((call) =>
    call.path === "/api/session" ? { state: "success", account: { id: "nigel", email: "nigel@example.com" }, csrf: "tok-123" } : { state: "success" },
  );
  const session = await checkSession();
  expect(session.account?.email).toBe("nigel@example.com");
  expect(currentSession()?.account?.id).toBe("nigel");
  await sendChatMessage("t1", "hi");
  const send = calls.find((call) => call.path.endsWith("/send"))!;
  expect(send.headers["x-eggy-csrf"]).toBe("tok-123");
  expect(send.body).not.toContain("nigel");
});

test("logout posts with the csrf token and clears the cached session", async () => {
  const calls = fakeFetch((call) =>
    call.path === "/api/session" ? { state: "success", account: { id: "nigel", email: "nigel@example.com" }, csrf: "tok-123" } : { state: "success" },
  );
  await checkSession();
  await logout();
  const out = calls.find((call) => call.path === "/api/logout")!;
  expect(out.method).toBe("POST");
  expect(out.headers["x-eggy-csrf"]).toBe("tok-123");
  expect(currentSession()).toBeNull();
});

test("a 401 clears the cached session so the next user starts clean", async () => {
  fakeFetch((call) =>
    call.path === "/api/session" ? { state: "success", account: { id: "a", email: "a@example.com" }, csrf: "a" } : { state: "success" },
  );
  await checkSession();
  globalThis.fetch = (async () => new Response(JSON.stringify({ state: "error", title: "not authenticated" }), { status: 401 })) as typeof fetch;
  await expect(sendChatMessage("t1", "hi")).rejects.toThrow();
  expect(currentSession()).toBeNull();
});

test("the navigation shows who is signed in with a sign-out control", () => {
  const html = renderToStaticMarkup(
    createElement(AppNavigation, { view: "chat", onNavigate: () => {}, account: { id: "nigel", email: "nigel@example.com" }, onLogout: () => {} }),
  );
  expect(html).toContain("nigel@example.com");
  expect(html).toContain("Sign out");
});

test("the accounts list shows enrollment and session state, with edit, remove and reset actions", () => {
  const html = renderToStaticMarkup(
    createElement(AccountsList, {
      accounts: [
        { id: "nigel", email: "nigel@example.com", telegram_user_id: 42, enrolled: true, signed_in: true, self: true },
        { id: "partner", email: "partner@example.com", enrolled: false, signed_in: false, self: false },
      ],
      onEdit: () => {},
      onRemove: () => {},
      onReset: () => {},
    }),
  );
  expect(html).toContain("nigel@example.com");
  expect(html).toContain("Enrolled");
  expect(html).toContain("Signed in");
  expect(html).toContain("Not enrolled");
  expect(html).toContain("Edit partner");
  expect(html).toContain("Remove partner");
  expect(html).toContain("Reset binding for nigel");
  // Your own row cannot be removed from here.
  expect(html).not.toContain("Remove nigel");
});

test("resetting a binding names its consequence before it happens", () => {
  const html = renderToStaticMarkup(createElement(ResetBindingConfirm, { account: "partner", onConfirm: () => {}, onCancel: () => {} }));
  expect(html).toContain("signed out");
  expect(html).toContain("enroll again");
});

test("the accounts card covers the login client and expected email, naming the secret only by variable", () => {
  const html = renderToStaticMarkup(createElement(AccountsCard, { onSessionExpired: () => {} }));
  expect(html).toContain("Google sign-in client");
  expect(html).toContain("client_secret_env");
  expect(html).toContain("Expected Google account");
  expect(html).not.toContain("config.yaml");
});

test("a single-owner deployment is offered conversion from the panel, not a file edit", () => {
  const html = renderToStaticMarkup(
    createElement(ConvertForm, { legacyOwner: "42", legacyTelegramId: 42, onConverted: () => {}, onSessionExpired: () => {} }),
  );
  expect(html).toContain("Convert to accounts");
  expect(html).toContain("migration");
  expect(html).not.toContain("config.yaml");
  expect(html).not.toContain("YAML");
});

test("the google card shows the verified identity against the expected one and says it is shared", () => {
  const ok = renderToStaticMarkup(createElement(GoogleIdentity, { connected: "eggy@example.com", expected: "eggy@example.com" }));
  expect(ok).toContain("eggy@example.com");
  expect(ok).toContain("Shared with all Eggy users");
  expect(ok).not.toContain("not Eggy");
  const mismatch = renderToStaticMarkup(createElement(GoogleIdentity, { connected: "nigel@example.com", expected: "eggy@example.com" }));
  expect(mismatch).toContain("not Eggy");
});

test("account management calls the config routes", async () => {
  const calls = fakeFetch(() => ({ state: "success" }));
  await addAccount({ id: "third", email: "third@example.com", telegram_user_id: 7 });
  await removeAccount("th/ird");
  await resetAccountBinding("partner");
  await setLoginClient("client", "ENV_NAME");
  await setExpectedGoogleEmail("eggy@example.com");
  expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "POST /api/config/accounts",
    "DELETE /api/config/accounts/th%2Fird",
    "POST /api/config/accounts/partner/reset-binding",
    "POST /api/config/login",
    "POST /api/config/google/expected-email",
  ]);
});

test("Telegram linking uses authenticated deep links instead of numeric ID input", async () => {
  const html = renderToStaticMarkup(createElement(TelegramLinkControl, {
    account: { id: "nigel", email: "nigel@example.com", enrolled: true, signed_in: true, self: true },
    available: true,
    onError: () => {},
  }));
  expect(html).toContain("Link Telegram");
  expect(html).not.toContain("Telegram sender ID");
  const calls = fakeFetch((call) => call.method === "POST" ? { url: "https://t.me/eggy_bot?start=credential", expires_at: "2026-09-13T00:10:00Z" } : { state: "success" });
  await createTelegramPairing("nigel");
  await unlinkTelegram("nigel");
  expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "POST /api/accounts/nigel/telegram/pairing",
    "DELETE /api/accounts/nigel/telegram",
  ]);
});

test("settings navigation includes accounts", () => {
  const html = renderToStaticMarkup(createElement(ConfigPage, { theme: "dark", onThemeChange: () => {}, onSessionExpired: () => {} }));
  const labels = [...html.matchAll(/<option[^>]*>([^<]+)<\/option>/g)].map((match) => match[1]);
  expect(labels).toContain("Accounts");
});

test("Discord linking is a single-use command sent to the bot, never an ID typed in", async () => {
  const html = renderToStaticMarkup(createElement(DiscordLinkControl, {
    account: { id: "nigel", email: "nigel@example.com", enrolled: true, signed_in: true, self: true },
    available: true,
    onError: () => {},
  }));
  expect(html).toContain("Link Discord");
  expect(html).not.toContain("Discord user ID");
  const other = renderToStaticMarkup(createElement(DiscordLinkControl, {
    account: { id: "partner", email: "partner@example.com", enrolled: true, signed_in: false, self: false },
    available: true,
    onError: () => {},
  }));
  expect(other).toBe("");
  const calls = fakeFetch((call) => call.method === "POST" ? { command: "/link token", expires_at: "2026-09-13T00:10:00Z" } : { state: "success" });
  expect((await createDiscordLink("nigel")).command).toBe("/link token");
  await unlinkDiscord("nigel");
  expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "POST /api/accounts/nigel/discord/link",
    "DELETE /api/accounts/nigel/discord",
  ]);
});

test("the accounts list shows Discord status only when Discord is enabled", () => {
  const props = {
    accounts: [{ id: "nigel", email: "nigel@example.com", discord_user_id: "1001", enrolled: true, signed_in: true, self: true }],
    telegramEnabled: false,
    pairingAvailable: false,
    onEdit: () => {},
    onRemove: () => {},
    onReset: () => {},
    onShowSteps: () => {},
    onError: () => {},
  };
  expect(renderToStaticMarkup(createElement(AccountsList, props))).not.toContain("Discord");
  const enabled = renderToStaticMarkup(createElement(AccountsList, { ...props, discordEnabled: true, discordLinkingAvailable: true }));
  expect(enabled).toContain("Discord 1001");
  expect(enabled).toContain("Unlink Discord");
});

test("the Discord card writes the token once and reads back only whether it is set", async () => {
  expect(describeDiscord({ enabled: false, application_id: "", bot_token_set: false, running: false })).toEqual(["disabled", "—", "not set"]);
  expect(describeDiscord({ enabled: true, application_id: "42", bot_token_set: true, bot_token_source: "environment", running: false })).toEqual(["enabled (restart to start)", "42", "from environment"]);
  expect(describeDiscord({ enabled: true, application_id: "42", bot_token_set: true, bot_token_source: "stored", running: true })).toEqual(["running", "42", "stored"]);
  const calls = fakeFetch((call) => call.method === "GET" ? { enabled: false, application_id: "", bot_token_set: false, running: false } : { state: "success" });
  await getDiscord();
  await setDiscord({ enabled: true, application_id: "42", bot_token: "secret" });
  await clearDiscordToken();
  expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "GET /api/config/discord",
    "POST /api/config/discord",
    "DELETE /api/config/discord/token",
  ]);
  expect(calls[1].body).toBe(JSON.stringify({ enabled: true, application_id: "42", bot_token: "secret" }));
  const html = renderToStaticMarkup(createElement(DiscordCard, { onSessionExpired: () => {} }));
  expect(html).toContain('type="password"');
  expect(html).toContain("Bot token");
});
