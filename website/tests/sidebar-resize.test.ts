import { expect, test } from "bun:test";
import {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  clampSidebarWidth,
  sidebarWidthForKey,
} from "../src/ThreadSidebar";

test("sidebar width stays within its usable desktop range", () => {
  expect(clampSidebarWidth(180)).toBe(240);
  expect(clampSidebarWidth(336)).toBe(336);
  expect(clampSidebarWidth(500)).toBe(420);
});

test("sidebar keyboard resizing uses small steps and exact bounds", () => {
  expect(sidebarWidthForKey(300, "ArrowLeft")).toBe(292);
  expect(sidebarWidthForKey(300, "ArrowRight")).toBe(308);
  expect(sidebarWidthForKey(300, "Home")).toBe(SIDEBAR_MIN_WIDTH);
  expect(sidebarWidthForKey(300, "End")).toBe(SIDEBAR_MAX_WIDTH);
  expect(sidebarWidthForKey(300, "Escape")).toBe(300);
  expect(SIDEBAR_DEFAULT_WIDTH).toBe(288);
});
