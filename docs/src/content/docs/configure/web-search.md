---
title: Web search
description: Give Eggy reach into the open web with Tavily search and page extraction.
eyebrow: Configure
---

Without this section Eggy cannot read the open web at all. Everything it knows arrives from the model's weights, from you, from a configured Google product, or from an MCP server.

Enabling it adds two tools, backed by [Tavily](https://tavily.com):

| Tool | What it does |
| --- | --- |
| `web_search` | Finds pages. Returns ranked results with a **snippet** of each page. |
| `web_extract` | Reads pages. Returns the actual content as markdown, for up to 5 urls. |

> **They are two calls on purpose.** Search returns snippets, not pages. Reading a result means a follow-up `web_extract` on its url. The tool descriptions say so, so the model chains them rather than answering from a snippet while believing it read the page.

## Setup

Get an API key from [tavily.com](https://tavily.com), put it in the environment, and enable the section:

```bash
TAVILY_API_KEY=tvly-your-key
```

```yaml
tavily:
  enabled: true
  api_key_env: "TAVILY_API_KEY"
```

Every other field has a default. Restart Eggy and the two tools appear in the catalog.

## Options

```yaml
tavily:
  enabled: true
  api_key_env: "TAVILY_API_KEY"
  search_depth: "basic"      # basic | advanced | fast | ultra-fast
  extract_depth: "basic"     # basic | advanced
  max_results: 5
  max_output_bytes: 65536
  timeout: "30s"
```

- **`search_depth`** — `advanced` returns better-ranked results for two credits instead of one. `fast` and `ultra-fast` trade quality for latency at one credit.
- **`extract_depth`** — `advanced` handles harder pages, at two credits per five urls instead of one.
- **`max_results`** — used only when the model does not ask for a count itself. The model may request 1 to 20.
- **`max_output_bytes`** — bounds a whole response. Page text is unbounded at the source, so one long article would otherwise consume a turn's context by itself. The budget is split evenly across results, so a long page cannot starve the others, and anything cut is marked `truncated` so the model knows it read a fragment.

## Credits

Tavily bills in credits, so it is worth knowing what a call costs:

| Call | Cost |
| --- | --- |
| Search, `basic` / `fast` / `ultra-fast` | 1 credit |
| Search, `advanced` | 2 credits |
| Extract, `basic` | 1 credit per 5 pages |
| Extract, `advanced` | 2 credits per 5 pages |

Failed calls are not retried. One call is one credit, and a retry you did not ask for is a charge you did not authorize — the model decides whether to try again.

If the key runs out, Eggy says so specifically: a plan limit (`432`) and a pay-as-you-go limit (`433`) are reported differently from a rate limit (`429`), because waiting only helps for the last one.

## Approvals

Both tools are classified read-only: they change nothing anywhere. In `normal` mode they run without asking. In `strict` mode they are put to you like every other call. See [Approvals](/eggy/use/approvals/).

## Cost when disabled

Nothing. With the section absent or `enabled: false`, no client is built, no tool is registered, and the two schemas never reach a model request. An owner who does not want Eggy reaching the internet pays nothing for the owners who do.
