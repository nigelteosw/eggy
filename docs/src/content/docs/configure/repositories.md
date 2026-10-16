---
title: Repository inspection
description: Give Eggy trusted, read-only access to configured Git repositories and GitHub metadata.
eyebrow: Configure
---

Repositories are startup configuration, not model-selected remotes. Eggy verifies each configured repository and base branch before serving.

## Configure a repository

```yaml
repositories:
  - name: eggy
    clone_url: https://github.com/nigelteosw/eggy.git
    base_branch: main
    protected_branches: [main]
    self: true
```

Set `GITHUB_TOKEN` when at least one repository is configured. Credentials stay inside the GitHub adapter.

At most one repository can set `self: true`. The flag grants nothing; it tells Eggy which checkout contains its own `AGENTS.md` and architecture documentation.

## Available tools

- `repository_list` — list configured repositories.
- `repository_github` — read provider metadata.
- `workspace_open` — create or attach a read-only checkout to a conversation.
- `read_file` — read a file inside the attached workspace.
- `workspace_close` — detach and clean up the workspace.

That is the whole repository surface. There is no tree listing, no content search, and no shell.

Changing code is not part of this surface. The [Agent Runner](/eggy/configure/agent-runner/) extension makes changes in separate, sandboxed runs on repositories you allow. It has its own switch, its own GitHub token, and its own checks, and it is off by default.

## Restrictions

Paths must remain inside the trusted checkout. Processes keep timeouts, output caps, environment allowlisting, and process-group termination.

The read-only agent tool surface cannot edit repository files, run a general shell, commit, push, create pull requests, or choose a new remote from Telegram.
