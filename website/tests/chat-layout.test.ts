import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ChatPage } from "../src/chat/ChatPage";
import { Composer, fieldHeight } from "../src/chat/Composer";
import { attachmentOf, recordedText, splitAttachments, uploadPlaceholder, uploadProblem } from "../src/lib/uploads";

test("an empty conversation uses one direct title and instruction", () => {
  const html = renderToStaticMarkup(
    createElement(ChatPage, {
      threadId: "thread-1",
      title: "Deploy the daemon",
      onSessionExpired: () => {},
    }),
  );

  expect(html).toContain("Deploy the daemon");
  expect(html).toContain("What can I do for you?");
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

  // The send box takes images and PDFs, the same list the server sniffs for;
  // + is where they are added from, and nothing else waits behind it.
  expect(html).toContain('aria-label="Add"');
  expect(html).toContain('accept="image/jpeg,image/png,image/webp,image/gif,application/pdf"');
  expect(html).not.toContain("Eggy can make mistakes.");
  expect(html).not.toContain("Run settings");
  // The bottom inset comes from --safe-bottom, which the keyboard zeroes.
  expect(html).toContain("var(--safe-bottom)");
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

test("a message's files are recorded as marker lines the transcript draws as attachments", () => {
  const attachments = [attachmentOf({ name: "q3.pdf", type: "application/pdf" }), attachmentOf({ name: "a.png", type: "image/png" })];
  // turns.durableMessageText drops the prompt sent for a message of files alone.
  expect(recordedText(uploadPlaceholder(attachments), attachments)).toBe("[file attached: q3.pdf]\n[image attached]");
  expect(recordedText("look at these", attachments)).toBe("look at these\n[file attached: q3.pdf]\n[image attached]");
  expect(splitAttachments("look at these\n[file attached: q3.pdf]\n[image attached]")).toEqual({
    body: "look at these",
    attachments: [
      { kind: "file", name: "q3.pdf" },
      { kind: "image", name: "Image" },
    ],
  });
  expect(splitAttachments("no files here")).toEqual({ body: "no files here", attachments: [] });
});

test("only what a model can read, at most 20 MB, is accepted", () => {
  expect(uploadProblem({ name: "a.png", type: "image/png", size: 10 })).toBeNull();
  expect(uploadProblem({ name: "notes.txt", type: "text/plain", size: 10 })).toContain("image or a PDF");
  expect(uploadProblem({ name: "big.pdf", type: "application/pdf", size: 21 << 20 })).toContain("20 MB");
});
