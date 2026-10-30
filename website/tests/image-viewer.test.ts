import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ImagePlaceholder, ImageViewer } from "../src/components/ui/image-viewer";
import { imageURL, savedImageName } from "../src/lib/images";
import { DRAWING_STEP, imageIdsIn, isDrawing } from "../src/chat/images";
import { TraceDetailPanel } from "../src/traces/TracesPage";
import type { TraceDetail } from "../src/lib/api";

test("an image is served by the panel and saved under a name that says where it came from", () => {
  expect(imageURL("0a1b2c")).toBe("/api/images/0a1b2c");
  expect(savedImageName("0a1b2c3d4e5f", "image/png")).toBe("eggy-0a1b2c3d.png");
  expect(savedImageName("0a1b2c3d4e5f", "image/jpeg")).toBe("eggy-0a1b2c3d.jpg");
  expect(savedImageName("0a1b2c3d4e5f", "image/webp")).toBe("eggy-0a1b2c3d.webp");
  // An unknown type still saves as something an image viewer opens.
  expect(savedImageName("0a1b2c3d4e5f", "")).toBe("eggy-0a1b2c3d.png");
});

test("a reply's images are found in the order it drew them", () => {
  expect(imageIdsIn("Two cats.\n\n[image:0a1b]\n[image:2c3d]")).toEqual(["0a1b", "2c3d"]);
  expect(imageIdsIn("[image generated, not kept]")).toEqual([]);
});

// The placeholder appears only while the newest step is the drawing one: an
// earlier drawing step followed by a search is not drawing any more.
test("the chat is drawing only while drawing is the newest step", () => {
  expect(isDrawing([DRAWING_STEP])).toBe(true);
  expect(isDrawing(["Searching the web", DRAWING_STEP])).toBe(true);
  expect(isDrawing([DRAWING_STEP, "Searching the web"])).toBe(false);
  expect(isDrawing([])).toBe(false);
});

test("the viewer is a full-screen dialog with save and close, and never a new tab", () => {
  const html = renderToStaticMarkup(createElement(ImageViewer, { ids: ["0a1b", "2c3d"], index: 1, onClose: () => {} }));
  expect(html).toContain('role="dialog"');
  expect(html).toContain('aria-modal="true"');
  expect(html).toContain('src="/api/images/2c3d"');
  expect(html).toContain("2 of 2");
  // Copy and Share appear only where the browser can do them; this render has neither.
  for (const label of ["Save image", "Close", "Previous image", "Next image"]) {
    expect(html).toContain(`aria-label="${label}"`);
  }
  expect(html).not.toContain("_blank");
});

test("a single image has no counter or arrows", () => {
  const html = renderToStaticMarkup(createElement(ImageViewer, { ids: ["0a1b"], index: 0, onClose: () => {} }));
  expect(html).not.toContain("1 of 1");
  expect(html).not.toContain("Next image");
});

test("the drawing placeholder is announced as busy", () => {
  const html = renderToStaticMarkup(createElement(ImagePlaceholder));
  expect(html).toContain('aria-busy="true"');
  expect(html).toContain("image-placeholder");
});

test("a trace shows the images its turn drew", () => {
  const detail: TraceDetail = {
    trace: {
      id: "t",
      conversation_id: "c",
      channel: "web",
      source: "web",
      kind: "owner",
      model: "m",
      input: "draw a cat",
      output: "",
      started_at: "2026-10-30T00:00:00Z",
      duration_ms: 1000,
      spans: 0,
      complete: true,
      total_tokens: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
    },
    spans: [],
    images: ["0a1b"],
  };
  const html = renderToStaticMarkup(createElement(TraceDetailPanel, { detail }));
  expect(html).toContain("Images");
  expect(html).toContain('src="/api/images/0a1b"');
  const without = renderToStaticMarkup(createElement(TraceDetailPanel, { detail: { ...detail, images: undefined } }));
  expect(without).not.toContain("/api/images/");
});
