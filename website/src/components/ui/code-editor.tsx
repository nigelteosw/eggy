import { cn } from "../../lib/utils";

// CodeEditor is the editing box for the documents and files the owner writes by
// hand: SOUL.md, WATCH.md, a skill's SKILL.md, and config.yaml. It is a native
// textarea -- undo, selection, mobile keyboards and autofill all behave as they
// do everywhere else -- whose text is transparent over a highlighted copy of the
// same characters. Both sit in one grid cell with the same font, padding and
// wrapping, so they line up character for character and the box grows with its
// content instead of scrolling inside the page.
//
// The highlighting is line-based and deliberately shallow: enough to tell a key
// from its value or a heading from body text, not a parser. It never changes
// the text, so a tokenizer mistake costs a colour, never a character.

export type Language = "markdown" | "yaml";

export type Kind =
  | "plain"
  | "marker"
  | "heading"
  | "strong"
  | "emphasis"
  | "code"
  | "link"
  | "comment"
  | "key"
  | "punctuation"
  | "literal"
  | "string";

export type Segment = { text: string; kind: Kind };

const KIND_CLASS: Record<Kind, string> = {
  plain: "",
  marker: "text-accent-700",
  heading: "font-semibold",
  strong: "font-semibold",
  emphasis: "italic",
  code: "rounded-mark bg-neutral-200/70",
  link: "text-accent-700 underline decoration-accent-700/40",
  comment: "text-neutral-600",
  key: "text-accent-700",
  punctuation: "text-neutral-500",
  literal: "text-foreground",
  string: "text-foreground",
};

function push(segments: Segment[], text: string, kind: Kind) {
  if (text !== "") segments.push({ text, kind });
}

// Bold, italic, inline code and links inside one line of Markdown. Emphasis
// needs a non-word character on each side, so snake_case names stay plain.
const INLINE = /(`[^`]+`)|(\*\*[^*\n]+\*\*|__[^_\n]+__)|((?<![\w*])\*[^*\s][^*]*\*(?![\w*])|(?<![\w_])_[^_\s][^_]*_(?![\w_]))|(\[[^\]]+\]\([^)\s]+\))/g;

function inline(text: string): Segment[] {
  const segments: Segment[] = [];
  let at = 0;
  for (const match of text.matchAll(INLINE)) {
    push(segments, text.slice(at, match.index), "plain");
    const kind: Kind = match[1] ? "code" : match[2] ? "strong" : match[3] ? "emphasis" : "link";
    push(segments, match[0], kind);
    at = match.index! + match[0].length;
  }
  push(segments, text.slice(at), "plain");
  return segments;
}

function markdownLine(line: string): Segment[] {
  const segments: Segment[] = [];
  const heading = line.match(/^(#{1,6} )(.*)$/);
  if (heading) {
    push(segments, heading[1], "marker");
    push(segments, heading[2], "heading");
    return segments;
  }
  const item = line.match(/^(\s*)([-*+] |\d+[.)] |> ?)(.*)$/);
  if (item) {
    push(segments, item[1], "plain");
    push(segments, item[2], "marker");
    return [...segments, ...inline(item[3])];
  }
  return inline(line);
}

// The index of a YAML comment's "#": one at the start of the value or after a
// space, and outside quotes.
function commentStart(value: string): number {
  let quote: string | null = null;
  for (let i = 0; i < value.length; i++) {
    const char = value[i];
    if (quote) {
      if (char === quote) quote = null;
    } else if (char === '"' || char === "'") {
      quote = char;
    } else if (char === "#" && (i === 0 || /\s/.test(value[i - 1]))) {
      return i;
    }
  }
  return -1;
}

function yamlValue(value: string): Segment[] {
  const segments: Segment[] = [];
  const hash = commentStart(value);
  const body = hash < 0 ? value : value.slice(0, hash);
  const trimmed = body.trim();
  const lead = body.slice(0, body.length - body.trimStart().length);
  const trail = body.slice(lead.length + trimmed.length);
  push(segments, lead, "plain");
  const kind: Kind = /^(["']).*\1$/.test(trimmed)
    ? "string"
    : /^(true|false|yes|no|on|off|null|~|-?\d+(\.\d+)?)$/i.test(trimmed)
      ? "literal"
      : "plain";
  push(segments, trimmed, kind);
  push(segments, trail, "plain");
  if (hash >= 0) push(segments, value.slice(hash), "comment");
  return segments;
}

function yamlLine(line: string): Segment[] {
  const segments: Segment[] = [];
  const comment = line.match(/^(\s*)(#.*)$/);
  if (comment) {
    push(segments, comment[1], "plain");
    push(segments, comment[2], "comment");
    return segments;
  }
  const prefix = line.match(/^(\s*)(- )?/)!;
  push(segments, prefix[1], "plain");
  push(segments, prefix[2] ?? "", "punctuation");
  const rest = line.slice(prefix[0].length);
  const key = rest.match(/^([\w.\-"' ]+?)(:)(?=\s|$)/);
  if (!key) return [...segments, ...yamlValue(rest)];
  push(segments, key[1], "key");
  push(segments, key[2], "punctuation");
  return [...segments, ...yamlValue(rest.slice(key[0].length))];
}

// splitFrontMatter separates a skill's YAML front matter -- the lines between
// two "---" lines at the very top -- from the Markdown it heads.
export function splitFrontMatter(text: string): { frontMatter: string | null; body: string } {
  const lines = text.split("\n");
  const end = lines[0] === "---" ? lines.indexOf("---", 1) : -1;
  if (end < 0) return { frontMatter: null, body: text };
  return { frontMatter: lines.slice(1, end).join("\n"), body: lines.slice(end + 1).join("\n") };
}

// highlight splits text into lines of segments whose texts, joined, are the
// input exactly.
export function highlight(text: string, language: Language): Segment[][] {
  const lines = text.split("\n");
  if (language === "yaml") return lines.map(yamlLine);

  // A skill's front matter is YAML between two "---" lines at the very top,
  // the rule splitFrontMatter also follows.
  const frontEnd = lines[0] === "---" ? lines.indexOf("---", 1) : -1;
  let fence: string | null = null;
  return lines.map((line, index) => {
    if (frontEnd > 0 && index <= frontEnd) {
      return index === 0 || index === frontEnd ? [{ text: line, kind: "punctuation" }] : yamlLine(line);
    }
    const opener = line.match(/^\s*(`{3,}|~{3,})/);
    if (fence) {
      if (opener && opener[1][0] === fence[0] && opener[1].length >= fence.length) {
        fence = null;
        return [{ text: line, kind: "marker" }];
      }
      return line === "" ? [] : [{ text: line, kind: "code" }];
    }
    if (opener) {
      fence = opener[1];
      return [{ text: line, kind: "marker" }];
    }
    return markdownLine(line);
  });
}

// Highlighted is the coloured text alone, for the editor's copy layer and for
// a read-only view of the same file.
export function Highlighted({ value, language }: { value: string; language: Language }) {
  return (
    <>
      {highlight(value, language).map((line, index) => (
        <span key={index}>
          {index > 0 && "\n"}
          {line.map((segment, at) =>
            segment.kind === "plain" ? (
              segment.text
            ) : (
              <span key={at} className={KIND_CLASS[segment.kind]}>
                {segment.text}
              </span>
            ),
          )}
        </span>
      ))}
    </>
  );
}

// The two layers must share every property that moves a character: font,
// size, line height, padding, wrapping and tab width.
const LAYER = "col-start-1 row-start-1 m-0 whitespace-pre-wrap break-words px-4 py-3.5 font-mono text-small leading-6 [tab-size:2]";

export function CodeEditor({
  value,
  onChange,
  language,
  label,
  placeholder,
  disabled = false,
  minHeight = "14rem",
  className,
}: {
  value: string;
  onChange: (value: string) => void;
  language: Language;
  label: string;
  placeholder?: string;
  disabled?: boolean;
  minHeight?: string;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "grid min-w-0 rounded-card bg-neutral-100 focus-within:ring-2 focus-within:ring-ring/40",
        disabled && "opacity-60",
        className,
      )}
      style={{ minHeight }}
    >
      <pre aria-hidden="true" className={cn(LAYER, "pointer-events-none text-foreground")}>
        <Highlighted value={value} language={language} />
        {/* A trailing newline in the textarea opens a line the copy must also have. */}
        {" "}
      </pre>
      <textarea
        aria-label={label}
        spellCheck={false}
        autoCapitalize="off"
        autoCorrect="off"
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        className={cn(
          LAYER,
          "resize-none overflow-hidden bg-transparent text-transparent caret-foreground selection:bg-accent-300/40 placeholder:text-neutral-500 focus-visible:outline-none disabled:cursor-not-allowed",
        )}
      />
    </div>
  );
}
