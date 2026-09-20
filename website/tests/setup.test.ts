import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { SetupPage } from "../src/SetupPage";
import { consumeSetupFragment } from "../src/api";

test("consumes the fragment before exchanging the setup credential", async () => {
  const events: string[] = [];
  await consumeSetupFragment(
    { hash: "#setup=secret-token", pathname: "/", search: "" },
    { replaceState: () => events.push("removed") },
    async (token) => {
      expect(token).toBe("secret-token");
      events.push("exchanged");
    },
  );
  expect(events).toEqual(["removed", "exchanged"]);
});

test("ignores unrelated fragments", async () => {
  let exchanged = false;
  await consumeSetupFragment(
    { hash: "#section=models", pathname: "/", search: "" },
    { replaceState: () => { throw new Error("fragment was removed"); } },
    async () => { exchanged = true; },
  );
  expect(exchanged).toBe(false);
});

test("setup form requests settings and credential names, never credential values", () => {
  const html = renderToStaticMarkup(createElement(SetupPage));
  for (const name of [
    "account_id",
    "public_base_url",
    "provider_name",
    "provider_base_url",
    "provider_api_key_env",
    "model_alias",
    "model_id",
  ]) {
    expect(html).toContain(`name="${name}"`);
  }
  expect(html).not.toContain('name="google_email"');
  expect(html).not.toContain('name="provider_api_key"');
  expect(html).not.toContain('type="password"');
  expect(html).toContain("Account");
  expect(html).toContain("Sign-in");
  expect(html).toContain("Model");
});
