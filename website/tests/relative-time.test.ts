import { expect, test } from "bun:test";
import { relativeTime } from "../src/lib/time";

const now = Date.parse("2026-10-28T12:00:00Z");

// A schedule is read as "in 2 hours", which answers "when" without arithmetic.
// The exact moment stays available on hover.
test("a future time reads as how far away it is", () => {
  expect(relativeTime("2026-10-28T12:30:00Z", now)).toBe("in 30 minutes");
  expect(relativeTime("2026-10-28T14:00:00Z", now)).toBe("in 2 hours");
  expect(relativeTime("2026-10-28T12:00:20Z", now)).toBe("now");
  expect(relativeTime("2026-10-29T13:00:00Z", now)).toBe("tomorrow");
  expect(relativeTime("2026-10-29T09:00:00Z", now)).toBe("in 21 hours");
  expect(relativeTime("2026-11-01T12:00:00Z", now)).toBe("in 4 days");
});

test("a time that has passed without running reads as overdue, and a bad value is shown as it is", () => {
  expect(relativeTime("2026-10-28T11:00:00Z", now)).toBe("overdue");
  expect(relativeTime("not a time", now)).toBe("not a time");
});
