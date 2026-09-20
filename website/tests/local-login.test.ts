import { afterEach, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { clearSession, getMode, login, redeemWebLoginLink, setAccountPassword, revokeAccountSessions, addAccount, PendingAccountError } from "../src/api";
import { LoginPage } from "../src/LoginPage";
import { SetupPage } from "../src/SetupPage";

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
  clearSession();
});

type Call = { path: string; method: string; headers: Record<string, string>; body: string };

function fakeFetch(respond: (call: Call) => { status?: number; body?: unknown } | unknown): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (input, init) => {
    const headers: Record<string, string> = {};
    for (const [key, value] of Object.entries(init?.headers ?? {})) headers[key.toLowerCase()] = String(value);
    const call: Call = { path: String(input), method: init?.method ?? "GET", headers, body: String(init?.body ?? "") };
    calls.push(call);
    const answer = respond(call) as { status?: number; body?: unknown } | undefined;
    if (answer && typeof answer === "object" && "status" in answer) {
      return new Response(JSON.stringify(answer.body ?? { state: "error" }), { status: answer.status });
    }
    return new Response(JSON.stringify(answer ?? { state: "success" }), { status: 200 });
  }) as typeof fetch;
  return calls;
}

test("the login form asks for a username and password and sends username", async () => {
  const html = renderToStaticMarkup(createElement(LoginPage, { login: "password", onLoggedIn: () => {} }));
  expect(html).toContain('id="username"');
  expect(html).toContain('autoComplete="username"');
  expect(html).toContain('autoComplete="current-password"');
  expect(html).toContain('type="password"');
  expect(html).not.toContain("Google");
  expect(html).not.toContain('type="email"');
  const calls = fakeFetch(() => ({ state: "success" }));
  await login("partner", "partner-password-long");
  expect(calls[0].path).toBe("/api/login");
  expect(JSON.parse(calls[0].body)).toEqual({ username: "partner", password: "partner-password-long" });
});

test("a safe mode that cannot identify anyone renders no form", () => {
  const html = renderToStaticMarkup(createElement(LoginPage, { login: "unavailable", onLoggedIn: () => {} }));
  expect(html).not.toContain('type="password"');
  expect(html).toContain("safe mode");
});

test("the mode probe reports password or unavailable, never google", async () => {
  fakeFetch(() => ({ mode: "normal", theme: "dark", login: "unavailable" }));
  expect((await getMode()).login).toBe("unavailable");
  fakeFetch(() => ({ mode: "normal", theme: "dark", login: "google" }));
  expect((await getMode()).login).toBe("password");
});

test("link redemption and credential routes call the server as specified", async () => {
  const calls = fakeFetch(() => ({ state: "success" }));
  await redeemWebLoginLink("t".repeat(43));
  await setAccountPassword("third", "new-password-long");
  await setAccountPassword("me", "new-password-long", "old-password-long");
  await revokeAccountSessions("third");
  expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "POST /api/login/link",
    "POST /api/config/accounts/third/password",
    "POST /api/config/accounts/me/password",
    "POST /api/config/accounts/third/revoke-sessions",
  ]);
  expect(calls[0].headers["x-eggy-login"]).toBe("1");
  expect(JSON.parse(calls[1].body)).toEqual({ password: "new-password-long" });
  expect(JSON.parse(calls[2].body)).toEqual({ password: "new-password-long", current_password: "old-password-long" });
  expect(calls[3].body).toBe("{}");
});

test("a pending creation is reported as such, distinct from a refusal", async () => {
  fakeFetch(() => ({ status: 503, body: { state: "error", title: "Account is pending.", detail: "set it again", account_created: true, password_state: "pending" } }));
  await expect(addAccount({ id: "third", telegram_user_id: 0, password: "third-password-long" })).rejects.toBeInstanceOf(PendingAccountError);
  fakeFetch(() => ({ status: 400, body: { state: "error", title: "that username was used before" } }));
  const refused = addAccount({ id: "third", telegram_user_id: 0, password: "third-password-long" });
  await expect(refused).rejects.not.toBeInstanceOf(PendingAccountError);
  await expect(refused).rejects.toThrow("used before");
});

test("setup binds the first account to the environment login and asks for no Google client", () => {
  const html = renderToStaticMarkup(createElement(SetupPage));
  for (const name of ["account_id", "public_base_url", "provider_name", "provider_base_url", "provider_api_key_env", "model_alias", "model_id"]) {
    expect(html).toContain(`name="${name}"`);
  }
  for (const gone of ["google_email", "login_client_id", "login_client_secret_env"]) {
    expect(html).not.toContain(`name="${gone}"`);
  }
  expect(html).toContain("EGGY_UI_PASSWORD");
  expect(html).not.toContain('type="password"');
});
