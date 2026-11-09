import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { CodeEditor, highlight, splitFrontMatter, type Segment } from "../src/components/ui/code-editor";

// Each line as "kind:text" pairs, dropping plain text, so a test names only
// what is highlighted.
function marked(lines: Segment[][]): string[][] {
  return lines.map((line) => line.filter((segment) => segment.kind !== "plain").map((segment) => `${segment.kind}:${segment.text}`));
}

test("the highlighted copy keeps every character, so it lines up with the textarea", () => {
  const text = "---\nname: x\n---\n# Title\n\n- **bold** and `code`\n```\nraw *text*\n```\n";
  for (const language of ["markdown", "yaml"] as const) {
    expect(highlight(text, language).map((line) => line.map((segment) => segment.text).join("")).join("\n")).toBe(text);
  }
});

test("markdown marks headings, list markers, quotes, emphasis, code and links", () => {
  expect(marked(highlight("## Plan\n- one\n> said\n**strong** _em_ `x` [docs](https://e.g)", "markdown"))).toEqual([
    ["marker:## ", "heading:Plan"],
    ["marker:- "],
    ["marker:> "],
    ["strong:**strong**", "emphasis:_em_", "code:`x`", "link:[docs](https://e.g)"],
  ]);
});

test("a fenced block is code throughout, with no markdown read inside it", () => {
  expect(marked(highlight("```yaml\n# not a heading\n```", "markdown"))).toEqual([
    ["marker:```yaml"],
    ["code:# not a heading"],
    ["marker:```"],
  ]);
});

test("a skill's front matter is read as YAML", () => {
  expect(marked(highlight("---\nname: hdb-design # mine\n---\n# Body", "markdown"))).toEqual([
    ["punctuation:---"],
    ["key:name", "punctuation::", "comment:# mine"],
    ["punctuation:---"],
    ["marker:# ", "heading:Body"],
  ]);
});

test("yaml marks keys, list dashes, literals, quoted strings and comments", () => {
  expect(marked(highlight("# top\nserver:\n  port: 8080\n  - name: \"a # b\"\n  on: true", "yaml"))).toEqual([
    ["comment:# top"],
    ["key:server", "punctuation::"],
    ["key:port", "punctuation::", "literal:8080"],
    ["punctuation:- ", "key:name", "punctuation::", "string:\"a # b\""],
    ["key:on", "punctuation::", "literal:true"],
  ]);
});

test("a url in a yaml value is not cut at its colon", () => {
  expect(marked(highlight("base_url: https://api.example.com/v1", "yaml"))).toEqual([["key:base_url", "punctuation::"]]);
});

test("the editor is one labelled textarea over a hidden highlighted copy", () => {
  const html = renderToStaticMarkup(
    createElement(CodeEditor, { value: "# Hi", onChange: () => {}, language: "markdown", label: "Soul" }),
  );
  expect(html).toContain('aria-label="Soul"');
  expect(html).toContain('spellcheck="false"');
  expect(html).toMatch(/<pre aria-hidden="true"/);
  expect(html.match(/<textarea/g)).toHaveLength(1);
});

// The skill preview shows front matter as YAML and renders only the body as
// Markdown; rendered, the --- lines became a rule and the keys a heading.
test("front matter is split from the body it heads", () => {
  expect(splitFrontMatter("---\nname: x\n---\n# Body")).toEqual({ frontMatter: "name: x", body: "# Body" });
  expect(splitFrontMatter("# Body\n---\n")).toEqual({ frontMatter: null, body: "# Body\n---\n" });
  expect(splitFrontMatter("---\nunclosed")).toEqual({ frontMatter: null, body: "---\nunclosed" });
});
