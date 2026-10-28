import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ConfirmDialog } from "../src/components/ui/confirm";

test("a destructive confirm names the action and its consequence, and is an alert dialog", () => {
  const html = renderToStaticMarkup(
    createElement(ConfirmDialog, {
      title: "Delete this chat?",
      body: "Its messages are deleted too, and this cannot be undone.",
      confirmLabel: "Delete chat",
      destructive: true,
      onAnswer: () => {},
    }),
  );
  expect(html).toContain('role="alertdialog"');
  expect(html).toContain("Delete this chat?");
  expect(html).toContain("cannot be undone");
  expect(html).toContain("Delete chat");
  expect(html).toContain("bg-destructive");
  expect(html).toContain(">Cancel<");
});

test("the confirm sheet anchors to the bottom edge on a phone", () => {
  const html = renderToStaticMarkup(
    createElement(ConfirmDialog, { title: "Restart Eggy?", confirmLabel: "Restart", onAnswer: () => {} }),
  );
  expect(html).toContain("items-end");
  expect(html).toContain("rounded-t-3xl");
  expect(html).not.toContain("bg-destructive");
});
