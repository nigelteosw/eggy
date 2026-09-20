import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { Window } from "happy-dom";
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { WebLoginLinkPage, takeWebLoginToken } from "../src/WebLoginLinkPage";

test("takes token out of browser history", () => {
  const urls: unknown[] = [];
  const token = "a".repeat(43);
  expect(takeWebLoginToken({ hash: `#token=${token}`, pathname: "/auth/link" },
    { replaceState: (_data, _unused, url) => { urls.push(url); } })).toBe(token);
  expect(urls).toEqual(["/auth/link"]);
});

test("discards tokens that are not the shape the server mints, still stripping the fragment", () => {
  const urls: unknown[] = [];
  const history = { replaceState: (_data: unknown, _unused: string, url?: string | URL | null) => { urls.push(url); } };
  expect(takeWebLoginToken({ hash: "#token=short", pathname: "/auth/link" }, history)).toBe("");
  expect(takeWebLoginToken({ hash: "#token=" + "a".repeat(42) + "$", pathname: "/auth/link" }, history)).toBe("");
  expect(takeWebLoginToken({ hash: "", pathname: "/auth/link" }, history)).toBe("");
  expect(takeWebLoginToken({ hash: "#other=1", pathname: "/auth/link" }, history)).toBe("");
  expect(urls).toHaveLength(4);
});

const dom = new Window({ url: "http://localhost/auth/link" });
const previous = { window: globalThis.window, document: globalThis.document, fetch: globalThis.fetch, localStorage: globalThis.localStorage, sessionStorage: globalThis.sessionStorage };
Object.assign(globalThis, { window: dom, document: dom.document, IS_REACT_ACT_ENVIRONMENT: true });
let root: Root;
let container: HTMLDivElement;
const token = "b".repeat(43);

function continueButton(): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll("button")).find((button) => button.textContent?.startsWith("Continue") || button.textContent?.startsWith("Signing in"));
}

async function render(props: Partial<Parameters<typeof WebLoginLinkPage>[0]> = {}) {
  await act(async () =>
    root.render(createElement(WebLoginLinkPage, { token, onSignedIn: () => {}, redeem: async () => ({}), ...props })),
  );
}

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  globalThis.fetch = previous.fetch;
});
afterAll(() => {
  Object.assign(globalThis, previous);
});

test("mounting fetches nothing, and one click posts exactly once with the marker header", async () => {
  const calls: { path: string; method: string; headers: Record<string, string>; body: string }[] = [];
  globalThis.fetch = (async (input, init) => {
    const headers: Record<string, string> = {};
    for (const [key, value] of Object.entries(init?.headers ?? {})) headers[key.toLowerCase()] = String(value);
    calls.push({ path: String(input), method: init?.method ?? "GET", headers, body: String(init?.body ?? "") });
    return new Response(JSON.stringify({ state: "success" }), { status: 200 });
  }) as typeof fetch;
  const storageReads: string[] = [];
  const spyStorage = new Proxy({}, { get: (_target, key) => { storageReads.push(String(key)); return () => null; } });
  Object.assign(globalThis, { localStorage: spyStorage, sessionStorage: spyStorage });
  let signedIn = 0;
  await act(async () =>
    root.render(createElement(WebLoginLinkPage, { token, onSignedIn: () => { signedIn++; } })),
  );
  expect(calls).toHaveLength(0);
  expect(container.textContent).toContain("Continue using the account that requested this Telegram link");
  expect(container.textContent).toContain("This may replace your current sign-in");
  await act(async () => continueButton()!.click());
  expect(calls).toHaveLength(1);
  expect(calls[0].method).toBe("POST");
  expect(calls[0].path).toBe("/api/login/link");
  expect(calls[0].headers["x-eggy-login"]).toBe("1");
  expect(JSON.parse(calls[0].body)).toEqual({ token });
  expect(signedIn).toBe(1);
  // A second click after success sends nothing more.
  await act(async () => continueButton()?.click());
  expect(calls).toHaveLength(1);
  expect(storageReads).toHaveLength(0);
  Object.assign(globalThis, { localStorage: previous.localStorage, sessionStorage: previous.sessionStorage });
});

test("the button is disabled while the redemption is pending", async () => {
  let release: () => void = () => {};
  const redeem = () => new Promise<unknown>((resolve) => { release = () => resolve({}); });
  await render({ redeem });
  await act(async () => continueButton()!.click());
  expect(continueButton()!.disabled).toBe(true);
  expect(continueButton()!.textContent).toContain("Signing in");
  await act(async () => { release(); });
});

test("an expired or used link shows one generic message and does not retry", async () => {
  let attempts = 0;
  const redeem = async () => { attempts++; throw new Error("this sign-in link is invalid, expired, or already used -- send /web again"); };
  await render({ redeem });
  await act(async () => continueButton()!.click());
  expect(attempts).toBe(1);
  expect(container.textContent).toContain("invalid, expired, or already used");
  expect(continueButton()).toBeUndefined();
  expect(container.textContent).toContain("Go to the sign-in page");
});

test("an ambiguous network failure is not retried automatically", async () => {
  let attempts = 0;
  const redeem = async () => { attempts++; throw new TypeError("Failed to fetch"); };
  await render({ redeem });
  await act(async () => continueButton()!.click());
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(attempts).toBe(1);
  expect(continueButton()).toBeUndefined();
});

test("an existing sign-in is named so switching is never silent, and a missing token explains itself", async () => {
  await render({ currentUsername: "partner" });
  expect(container.textContent).toContain("currently signed in as");
  expect(container.textContent).toContain("partner");
  // The token is read once at mount, as the app mounts the page once per
  // link; a fresh mount with none explains itself.
  await act(async () => root.unmount());
  root = createRoot(container);
  await render({ token: "" });
  expect(continueButton()).toBeUndefined();
  expect(container.textContent).toContain("Send /web to Eggy in Telegram again");
});
