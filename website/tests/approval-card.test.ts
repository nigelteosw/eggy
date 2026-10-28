import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ApprovalCard } from "../src/ChatPage";

test("the card leads with the sentence, and the arguments stay behind Show details", () => {
  const html = renderToStaticMarkup(
    createElement(ApprovalCard, {
      approval: { id: "a1", summary: "Send an email to \"sam@example.com\".", details: "google_gmail\n{\n  \"to\": \"sam@example.com\"\n}" },
      onDecide: () => {},
    }),
  );
  expect(html).toContain("Send an email to");
  expect(html).toContain("Show details");
  expect(html).toContain('aria-expanded="false"');
  expect(html).not.toContain("<pre");
});

test("the two answers are Approve and Decline, and a card with no details has no disclosure", () => {
  const html = renderToStaticMarkup(
    createElement(ApprovalCard, { approval: { id: "a2", summary: "Cancel the schedule \"s1\"." }, onDecide: () => {} }),
  );
  expect(html).toContain(">Approve<");
  expect(html).toContain(">Decline<");
  expect(html).not.toContain("Show details");
});
