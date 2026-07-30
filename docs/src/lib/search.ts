export function normalizeSearchText(markdown: string): string {
  return markdown
    .replace(/^---[\s\S]*?---\s*/u, "")
    .replace(/```[\w-]*\n?/gu, "")
    .replace(/<[^>]+>/gu, " ")
    .replace(/!\[([^\]]*)\]\([^)]+\)/gu, "$1")
    .replace(/\[([^\]]+)\]\([^)]+\)/gu, "$1")
    .replace(/[`*_>#|~-]+/gu, " ")
    .replace(/^\s*\d+\.\s+/gmu, "")
    .replace(/\s+/gu, " ")
    .trim();
}
