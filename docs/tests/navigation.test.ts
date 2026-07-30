import { describe, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { join } from "node:path";
import {
  flatNavigation,
  getAdjacentItems,
} from "../src/data/navigation";

describe("documentation navigation", () => {
  test("has unique routes and labels", () => {
    expect(new Set(flatNavigation.map((item) => item.path)).size).toBe(
      flatNavigation.length,
    );
    expect(new Set(flatNavigation.map((item) => item.title)).size).toBe(
      flatNavigation.length,
    );
  });

  test("starts at the introduction and covers every content entry", () => {
    expect(flatNavigation[0]?.path).toBe("/");
    for (const item of flatNavigation) {
      const file =
        item.path === "/"
          ? "index.md"
          : `${item.path.replace(/^\//, "")}.md`;
      expect(
        existsSync(join(import.meta.dir, "../src/content/docs", file)),
      ).toBeTrue();
    }
  });

  test("provides previous and next article links", () => {
    expect(getAdjacentItems("/")).toEqual({
      next: flatNavigation[1],
    });
    expect(getAdjacentItems(flatNavigation.at(-1)!.path)).toEqual({
      previous: flatNavigation.at(-2),
    });
  });
});
