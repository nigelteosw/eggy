import { expect, test } from "bun:test";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import config from "../tailwind.config.js";
import { greeting } from "../src/components/ui/voice";
import { PageHeader } from "../src/components/ui/page-header";

// The serif is Eggy's voice: the greeting, the sign-in line and empty states.
// It reads as written by someone only while it stays rare, so the places that
// use it are listed here and a new one has to be argued for.
const VOICE_FILES = [
  "auth/LoginPage.tsx",
  "chat/ChatPage.tsx",
  "chat/ThreadSidebar.tsx",
  "components/ui/page-header.tsx",
  "components/ui/voice.tsx",
];

async function sources(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = await Promise.all(entries.map((entry) => (entry.isDirectory() ? sources(join(dir, entry.name)) : [join(dir, entry.name)])));
  return files.flat().filter((file) => /\.tsx?$/.test(file));
}

test("the voice serif is a token, and only the voice moments use it", async () => {
  expect(config.theme.extend.fontFamily.voice[0]).toBe("Fraunces");
  const users: string[] = [];
  for (const file of await sources(join(import.meta.dir, "../src"))) {
    const text = await readFile(file, "utf8");
    if (text.includes("font-voice")) users.push(file.split("/src/")[1]);
  }
  expect(users.sort()).toEqual([...VOICE_FILES].sort());
});

test("no control is set in the voice serif", async () => {
  for (const file of VOICE_FILES) {
    const text = await readFile(join(import.meta.dir, "../src", file), "utf8");
    expect(text).not.toMatch(/<button[^>]*font-voice/);
  }
});

test("Home greets the reader by the hour and by name", () => {
  expect(greeting(8, "Nigel")).toBe("Good morning, Nigel.");
  expect(greeting(14)).toBe("Good afternoon.");
  expect(greeting(21, " ")).toBe("Good evening.");
  expect(greeting(2, "Sam")).toBe("Good evening, Sam.");
  const html = renderToStaticMarkup(createElement(PageHeader, { title: greeting(9, "Sam"), voice: true }));
  expect(html).toContain("font-voice");
});
