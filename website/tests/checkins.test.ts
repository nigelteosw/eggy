import { expect, test } from "bun:test";
import { cadenceLabel, durationOf, minutesOf, windowOf } from "../src/lib/checkins";

test("the stored interval reads back as minutes", () => {
  expect(minutesOf("off")).toBe(0);
  expect(minutesOf("3h0m0s")).toBe(180);
  expect(minutesOf("45m0s")).toBe(45);
  expect(minutesOf("1h30m0s")).toBe(90);
  expect(minutesOf("soon")).toBeNull();
});

test("a cadence saves as minutes, and off as blank", () => {
  expect(durationOf(180)).toBe("180m");
  expect(durationOf(0)).toBe("");
});

test("a cadence is named in words, including one the picker does not offer", () => {
  expect(cadenceLabel(60)).toBe("Every hour");
  expect(cadenceLabel(1440)).toBe("Once a day");
  expect(cadenceLabel(45)).toBe("Every 45 minutes");
  expect(cadenceLabel(240)).toBe("Every 4 hours");
  expect(cadenceLabel(90)).toBe("Every 1 h 30 min");
});

test("the active window reads into two time fields", () => {
  expect(windowOf("8:00-22:00")).toEqual({ start: "08:00", end: "22:00" });
  expect(windowOf("any hour")).toBeNull();
});
