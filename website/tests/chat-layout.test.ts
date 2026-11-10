import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ChatPage } from "../src/chat/ChatPage";
import { Composer, fieldHeight } from "../src/chat/Composer";

test("an empty conversation uses one direct title and instruction", () => {
  const html = renderToStaticMarkup(
    createElement(ChatPage, {
      threadId: "thread-1",
      title: "Deploy the daemon",
      onSessionExpired: () => {},
    }),
  );

  expect(html).toContain("Deploy the daemon");
  expect(html).toContain("Start by asking a question or describing a task.");
  expect(html).not.toContain("Conversation");
  expect(html).not.toContain("🥚");
});

test("the composer is one box with the send control inside, and respects the phone safe area", () => {
  const html = renderToStaticMarkup(
    createElement(Composer, {
      onSend: () => {},
      onSessionExpired: () => {},
    }),
  );

  // No upload control until the web channel can take attachments: a control
  // that accepts a file and then refuses to send it is worse than none.
  expect(html).not.toContain("Add files or photos");
  expect(html).not.toContain("Eggy can make mistakes.");
  expect(html).not.toContain("Run settings");
  expect(html).toContain("safe-area-inset-bottom");
  expect(html).toContain('aria-label="Send message"');
  // 36 px to the eye, 44 px under a finger.
  expect(html).toContain("[@media(pointer:coarse)]:h-11");
  expect(html).toContain('aria-label="Message"');
  expect(html).not.toContain("Ask Eggy anything");
});

test("the message box grows with its text up to a bound, smaller when the keyboard is up", () => {
  expect(fieldHeight(40, 800)).toBe(40);
  expect(fieldHeight(1000, 800)).toBe(240);
  // A phone with the keyboard open shows ~350 px: the box stops at 40% of it.
  expect(fieldHeight(1000, 350)).toBe(140);
  expect(fieldHeight(1000, 100)).toBe(96);
});
