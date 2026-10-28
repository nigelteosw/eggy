import { expect, test } from "bun:test";
import { SECTIONS, sectionMatches } from "../src/settings/ConfigPage";

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

// The index names the cards and fields inside a section, so a person searching
// for the thing they want to change lands on the section that holds it.
test("a search finds a section by a card title or field label inside it", () => {
  const find = (id: string) => SECTIONS.find((section) => section.id === id)!;
  expect(sectionMatches(find("automation"), "watch list")).toBe(true);
  expect(sectionMatches(find("automation"), "active from")).toBe(true);
  expect(sectionMatches(find("models"), "api key")).toBe(true);
  expect(sectionMatches(find("connections"), "bot token")).toBe(true);
  expect(sectionMatches(find("personal"), "match system")).toBe(true);
  expect(sectionMatches(find("soul"), "watch list")).toBe(false);
});
