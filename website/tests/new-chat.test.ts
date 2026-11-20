import { expect, test } from "bun:test";
import { canStartNewChat, newChatAction } from "../src/chat/ThreadSidebar";
import { ChatPage } from "../src/chat/ChatPage";
import type { Thread } from "../src/lib/api";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

function thread(id: string, title: string): Thread {
  return { id, title, updatedAt: "2026-09-05T12:00:00Z" };
}

test("refuses a new chat while the open one is still empty", () => {
  const threads = [thread("fresh", ""), thread("older", "Yesterday's chat")];

  expect(canStartNewChat(threads, "fresh")).toBe(false);
});

test("allows a new chat once the open one has been written in", () => {
  const threads = [thread("fresh", ""), thread("used", "Deploy the daemon")];

  expect(canStartNewChat(threads, "used")).toBe(true);
});

// The list arrives a moment after the first render, and a button that starts
// out disabled for that moment is a button that looks broken.
test("allows a new chat before the thread list has loaded", () => {
  expect(canStartNewChat([], null)).toBe(true);
  expect(canStartNewChat([], "not-loaded-yet")).toBe(true);
});

// On Home with the empty draft behind it, New chat used to grey out with
// nothing on screen to say why. Away from the chat it always goes somewhere.
test("new chat opens the empty draft from another view instead of doing nothing", () => {
  expect(newChatAction("home", true, [], null)).toBe("open");
  expect(newChatAction("chat", true, [], null)).toBe("none");
  expect(newChatAction("home", false, [thread("fresh", "")], "fresh")).toBe("open");
  expect(newChatAction("chat", false, [thread("used", "Deploy")], "used")).toBe("start");
  expect(newChatAction("home", false, [thread("used", "Deploy")], "used")).toBe("start");
});

test("a new chat draft can render before a server thread exists", () => {
  const html = renderToStaticMarkup(
    createElement(ChatPage, {
      threadId: null,
      title: "New chat",
      sidebarOpen: true,
      onSessionExpired: () => {},
      onThreadCreated: () => {},
    }),
  );

  expect(html).toContain('placeholder="Message Eggy"');
});
