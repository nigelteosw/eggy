import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ChatPage } from "../src/ChatPage";
import { Composer } from "../src/Composer";

test("an empty conversation uses one direct title and instruction", () => {
  const html = renderToStaticMarkup(
    createElement(ChatPage, {
      threadId: "thread-1",
      title: "Deploy the daemon",
      sidebarOpen: true,
      onSessionExpired: () => {},
    }),
  );

  expect(html).toContain("Deploy the daemon");
  expect(html).toContain("Start by asking a question or describing a task.");
  expect(html).not.toContain("Conversation");
  expect(html).not.toContain("🥚");
});

test("the composer names run settings and respects the phone safe area", () => {
  const html = renderToStaticMarkup(
    createElement(Composer, {
      onSend: () => {},
      onSessionExpired: () => {},
    }),
  );

  expect(html).toContain("Run settings");
  expect(html).toContain("safe-area-inset-bottom");
  expect(html).toContain('aria-label="Send message"');
  expect(html).toContain("h-11 w-11");
});
