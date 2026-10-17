---
title: Marketplace
description: Install skills and remote MCP servers from Claude Code marketplaces and SKILL.md repositories on GitHub, after reviewing exactly what they write.
eyebrow: Configure
---

The marketplace lets you install skills and remote tool servers that other people publish, without copying files by hand. Every install is reviewed first: you see the exact files and servers that will be written, at one exact commit, and nothing changes until you approve that review.

Eggy reads existing catalogs. It does not publish one.

## Turn it on

The marketplace is off by default. Turn it on in **Settings → Marketplace**, or in `config.yaml`; the sources can then be edited in **Customize → Plugins → Sources**:

```yaml
marketplace:
  enabled: true
  sources:
    - name: anthropic-skills
      kind: skills_repo
      repo: anthropics/skills
      path: skills
    - name: team-plugins
      kind: claude_marketplace
      repo: example-org/claude-plugins
      ref: main
```

- `kind: skills_repo` reads a repository whose subfolders each hold a `SKILL.md`.
- `kind: claude_marketplace` reads a repository with `.claude-plugin/marketplace.json`.
- `repo` is `owner/name` on github.com. Only public repositories work in this version.
- `path` and `ref` are optional. `ref` names a branch or tag; the default is the repository's default branch.

Turning the feature on or off takes effect after a restart. Source edits made in the panel apply to the next check.

## Install something

1. Open **Customize → Plugins → Discover** and pick a source.
2. Choose an item and read the review. It lists:
   - every skill, with its full text and any reference files;
   - remote MCP servers, which are added **disabled**;
   - anything Eggy left out, with the reason.
3. If a skill's name already exists in your skills, or is not a valid Eggy name, the review asks you to rename it before you can install.
4. Approve. The install writes exactly what you reviewed. If the content at that commit changed while you were reading, the install is refused and you review it again.

Skills are shared by every account on this Eggy, and their instructions can reach the model on any turn where they match.

## What is never installed

- Scripts, binaries, and other files a skill's instructions might run. Eggy never executes skill files.
- Hooks, commands, agents, `bin/` executables, themes, and monitors. The review lists them as not supported.
- Stdio MCP servers, which run a downloaded program. They are listed as skipped.
- MCP servers that need headers or configuration variables Eggy cannot infer, use a plain `http://` URL, or have a name Eggy cannot use. They are listed so you can add them by hand in Connectors.
- An MCP server whose name is already configured. It is listed as left out rather than replacing yours.

## Updates

Nothing updates on its own. **Check for updates** compares the install with its source's current commit and shows the new review if there is one.

An update keeps any skill you have edited, unless you tick **Replace my local edits** for that skill. An MCP server you have changed is never replaced. A skill that the new version no longer has is removed, unless you edited it.

## Disable and remove

- **Disable** turns off the install's skills and MCP servers together. **Enable** turns the skills back on. A server stays off after enabling until you enable it in **Customize → Connectors**.
- **Remove** deletes what the install wrote. An edited skill is kept as a hand-placed skill unless you tick it for deletion.

## Limits

- GitHub allows 60 unauthenticated requests an hour. When the quota runs out, the panel shows a failure; try again later.
- A skill's `SKILL.md` is limited to 32 KiB, a reference file to 64 KiB, and a skill folder to 256 KiB and 64 files in total.
- A skill's description must be 1,024 bytes or less, or the skill is listed as left out. Its name must be lowercase letters, digits, and hyphens, at most 64 characters; otherwise the review asks you to rename it.
- Reference files must end in `.md`, `.txt`, `.json`, `.yaml`, `.yml`, or `.csv`. Other files are listed as left out.
- Downloads are capped at 20 MiB compressed, 64 MiB uncompressed, and 5,000 entries. An archive with a symlink, a hard link, a device, or a path that climbs out of the repository is refused whole.
- Eggy sends no credential with catalog requests, so it never sends your `GITHUB_TOKEN`.

See [Skills](/eggy/use/skills/) for how installed skills are used, and [MCP servers](/eggy/configure/mcp-servers/) for enabling remote servers.
