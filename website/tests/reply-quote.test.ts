import { expect, test } from "bun:test";
import { replyQuote } from "../src/lib/reply";

// A reply quotes what it answers. On a phone that is the Reply button under a
// message, so the quote is the message itself, bounded so a long answer cannot
// fill the composer.
test("a reply quotes the message, and marks whether it was Eggy's own", () => {
  expect(replyQuote("Ship on Friday?", false)).toEqual({ text: "Ship on Friday?", ownMessage: false });
  expect(replyQuote("I can do that.", true)).toEqual({ text: "I can do that.", ownMessage: true });
});

test("a long message is quoted in part, and trailing space is dropped", () => {
  const quoted = replyQuote("  " + "a".repeat(900) + "  ", true);
  expect(quoted.text.length).toBeLessThanOrEqual(601);
  expect(quoted.text.endsWith("…")).toBe(true);
});
