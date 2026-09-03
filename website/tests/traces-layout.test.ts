import { expect, test } from "bun:test";
import { timelineTicks } from "../src/TracesPage";

test("timeline omits a tick that would collide with the duration label", () => {
  expect(timelineTicks(15_200)).toEqual([5_000, 10_000]);
});

test("timeline keeps ticks with enough room before the duration label", () => {
  expect(timelineTicks(20_000)).toEqual([5_000, 10_000, 15_000]);
});
