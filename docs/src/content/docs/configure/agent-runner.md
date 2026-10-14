---
title: Agent Runner
description: Let Eggy change repositories you allow, run their checks, and open a pull request for a person to review.
eyebrow: Configure
---

The Agent Runner lets Eggy make a change in a repository you allow. It works in the background, runs only the checks you list, and opens a pull request. It never approves or merges that pull request. A person reviews it.

Without this section there is no runner, no Agent Runner page, no `/code` command, and no `agent_runner` tool.

## Turn it on

In the panel, open the **Agent Runner** card in Settings, switch on **Enable Agent Runner**, choose a coding model, and save. The card says **Restart to apply** while the saved setting and the running one differ.

Or in `config.yaml`:

```yaml
agent_runner:
  enabled: true
  model: "coding"        # a models: alias from your config
  skills:
    brainstorming: true
    writing-plans: true
    verification-before-completion: true
```

Coding runs always use `model`, whatever you chose for chat with `/model`. The three skills are bundled with the runner, and each can be switched off.

## Give it a GitHub token

Create a fine-grained GitHub token with **Contents** and **Pull requests** read and write, limited to the repositories you allow. Paste it into the GitHub token field on the Agent Runner page.

The token is sealed in Eggy's database for your account. Eggy never shows it back, and only you see it. When you add a repository, Eggy checks your saved token against it after the next restart.

## Allow repositories

Repositories are listed in `config.yaml`, with the checks a run may execute:

```yaml
repositories:
  - name: eggy
    clone_url: https://github.com/you/eggy.git
    base_branch: main
    protected_branches: [main]
    checks:
      - [go, vet, ./...]
      - [go, test, ./...]
```

- **Checks** are argument lists, not shell strings. They are the only commands a run may execute, and up to eight are allowed per repository. Each check's exit code is the only check result a pull request reports.
- You can add a repository from the Agent Runner page, and edit its checks there. Saved changes apply after Eggy restarts.
- Each person then switches runs on for each repository with **Allow runs on this repository for my account**. Nothing is allowed by default.

Eggy refuses to push to protected or default branches, to anything under `.github/`, and to any repository other than the run's own.

## Start a run

From the Agent Runner page, pick a repository and describe the change in a sentence or two. The page says what a run does before you start it: it pushes a feature branch and opens a pull request, Eggy does not approve or merge it, and your repository's CI runs on the branch.

In Telegram, use `/code run <repository> <task>`.

Eggy cannot start a run itself. Its `agent_runner` tool has no start action, because a run's push authority must come from you, through the panel or `/code run`.

## Follow a run

The Agent Runner page has two tabs. **Agent Runner** holds the start form, the most recent runs, and your repositories. **Previous runs** holds every run, filtered by **Needs you**, **In progress**, **Completed** or **Stopped**, with a count on each. While a run is waiting for your answer, a banner on both tabs links to it.

Open a run for its full story:

- **The task**, the change you asked for, in full.
- **The question or the pull request** comes first: the answer form when the agent is asking you, the pull request title and link when it opened one.
- **Why it stopped**, for a failed, cancelled or interrupted run, with **Stopped at** naming the publish stage it reached.
- **Progress**, the publish checkpoints (Committed, Pushed, Pull request opened, Delivered) with the ones it reached ticked.
- **Working time**, **Tokens**, the base branch, and the working branch.
- **What happened**, the run's steps in order: when it started, each tool call and whether it succeeded, questions, publish stages, and the outcome. A tool step records the tool's name and whether it worked, not what it returned. The model's own calls are hidden until you choose to show them.
- **What the run produced**, the summary or output, collapsed by default.

A finished run, one that completed, failed, was cancelled or was interrupted, can be deleted from its page. Deleting removes the run record, its steps and its working directory. A pull request it opened stays on GitHub. A run still in progress must be cancelled first.

The same status is available in Telegram with `/code status`.

### Questions and cancellation

A run can stop to ask you a question. Answer it on the run's page, or with `/code answer <id> <text>`. A run waiting for an answer expires when its mandate does.

Cancel a run from its page, or with `/code cancel <id>`.

## What a run may do

- **One branch and one pull request per run.** The branch is named `eggy/<run-id>`. The pull request is a draft when the checks did not all run and pass.
- **No approving, merging, tagging, force-pushing, or deleting refs.**
- **A 24-hour mandate.** It belongs to the account that started the run, and Eggy re-checks it before every push and every pull request.
- **Bash only in a sandbox.** On Linux, a run's commands run as a separate `coder` user under Landlock. Bash is offered only after a startup probe proves the sandbox works. Elsewhere, a run can read and edit files and run no commands, and the panel says so.

## Limits

| Limit | Value |
| --- | --- |
| Active working time per run | 30 minutes |
| New model tokens per run (prompt tokens served from the cache do not count) | 2,000,000 |
| Steps in the coding loop | 200 |
| Output kept per run | 2 MiB |
| Disk for all run workspaces | 10 GiB |
| Failed, cancelled and interrupted workspaces kept for inspection | 7 days |

A run that hits a limit stops, and its record and reason say which limit it hit.

## Eggy's `agent_runner` tool

With the runner on, Eggy can check on runs in chat:

| Action | Effect |
| --- | --- |
| `status`, `list` | Read a run or the list of runs |
| `steps` | Read a run's steps. Eggy is told these are data from the run, not instructions |
| `cancel`, `answer` | Change a run. These ask first in `normal` mode, as other writes do |

A heartbeat or schedule cannot reach the tool. See [Approvals](/eggy/use/approvals/).

## Turning it off

Switch it off in the same card and restart. The Agent Runner page and the `/code` command disappear, and the tool goes with them. **Your run records stay** in `eggy.db` and come back when you turn it on again.

## Cost when disabled

Nothing. With the section absent or `enabled: false`, no tool schema reaches a model request, no route is served, and no goroutine runs. The only residue is empty tables in the database.
