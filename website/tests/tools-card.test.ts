import { expect, test } from "bun:test";
import { firstSentence, sourceLabel } from "../src/settings/ToolsCard";

// A tool's description is written for the model, so it runs long. The owner
// reads the first sentence; the rest is one tap away.
test("the owner reads the first sentence of a tool's description", () => {
  expect(firstSentence("Refresh the trusted current time. The turn-start time is already in the context, so do not call this.")).toBe(
    "Refresh the trusted current time.",
  );
  expect(firstSentence("Read the calendar")).toBe("Read the calendar");
});

test("a very long first sentence is cut rather than shown whole", () => {
  const long = "a".repeat(400);
  expect(firstSentence(long).length).toBeLessThanOrEqual(161);
});

test("built-in tools say so, instead of showing the internal word kernel", () => {
  expect(sourceLabel("kernel")).toBe("built in");
  expect(sourceLabel("mcp")).toBe("mcp");
});
