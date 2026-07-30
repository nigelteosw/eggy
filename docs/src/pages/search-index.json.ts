import { getCollection, render } from "astro:content";
import { flatNavigation } from "../data/navigation";
import { docUrl } from "../lib/urls";
import { normalizeSearchText } from "../lib/search";

export const prerender = true;

export async function GET() {
  const entries = await getCollection("docs");
  const byID = new Map(entries.map((entry) => [entry.id, entry]));
  const items = await Promise.all(
    flatNavigation.map(async (item) => {
      const id = item.path === "/" ? "index" : item.path.slice(1);
      const entry = byID.get(id);
      if (!entry) throw new Error(`Missing search entry: ${id}`);
      const { headings } = await render(entry);
      return {
        title: entry.data.title,
        description: entry.data.description,
        path: docUrl(item.path),
        headings: headings.map((heading) => heading.text),
        text: normalizeSearchText(entry.body ?? ""),
      };
    }),
  );

  return new Response(JSON.stringify(items), {
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
}
