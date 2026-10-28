import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { PageHeader } from "../src/components/ui/page-header";
import { SaveBar } from "../src/components/ui/save-bar";

// A page's heading carries the rail's menu button on a phone only, so the
// menu and the title share one row and the work starts at the top.
test("the page heading carries the phone menu button beside the title", () => {
  const html = renderToStaticMarkup(createElement(PageHeader, { title: "Scheduled", onOpenSidebar: () => {}, action: createElement("button", null, "Refresh") }));
  expect(html).toContain('aria-label="Open sidebar"');
  expect(html).toContain("md:hidden");
  expect(html).toContain("Scheduled");
  expect(html).toContain("Refresh");
  expect(renderToStaticMarkup(createElement(PageHeader, { title: "Home" }))).not.toContain("Open sidebar");
});

// The phone's save bar appears only once a form has changes to keep, and never
// on a wide screen where the form's own button is in reach.
test("the save bar shows only while a form has changes, and only below md", () => {
  expect(renderToStaticMarkup(createElement(SaveBar, { dirty: false, saving: false, label: "Save", onSave: () => {} }))).toBe("");
  const html = renderToStaticMarkup(createElement(SaveBar, { dirty: true, saving: false, label: "Save soul", onSave: () => {} }));
  expect(html).toContain("Save soul");
  expect(html).toContain("md:hidden");
  expect(html).toContain("env(safe-area-inset-bottom)");
});
