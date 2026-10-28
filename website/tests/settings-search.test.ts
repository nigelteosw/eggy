import { expect, test } from "bun:test";
import { SECTIONS, sectionMatches } from "../src/ConfigPage";

// Search answers "where do I change X". A section that talks about X is an
// answer even when its name does not say so.
test("a search finds a section by what it talks about, not only by its name", () => {
  const personal = SECTIONS.find((section) => section.id === "personal")!;
  expect(sectionMatches(personal, "telegram")).toBe(true);
  expect(sectionMatches(personal, "Model")).toBe(true);
  expect(sectionMatches(personal, "zzz-nothing")).toBe(false);
});

test("an empty search matches every section", () => {
  for (const section of SECTIONS) expect(sectionMatches(section, "")).toBe(true);
});
