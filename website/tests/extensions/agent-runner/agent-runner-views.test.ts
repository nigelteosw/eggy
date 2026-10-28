import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AddRepositoryForm, AgentRunStartForm, AgentRunnerPage } from "../../../src/extensions/agent-runner/AgentRunnerPage";
import { AgentRunnerSettingsView } from "../../../src/extensions/agent-runner/AgentRunnerCard";
import { GitHubTokenView } from "../../../src/settings/GitHubTokenField";
import { runningExtensions } from "../../../src/extensions";
import { AGENT_START_COPY } from "../../../src/extensions/agent-runner/agent-runner";

const repos = [{ name: "eggy", enabled: true, push_access: true, checks: [], pending: false, removed: false, needs_restart: false }];

test("the start form shows the spec's copy above its button", () => {
  const markup = renderToStaticMarkup(
    createElement(AgentRunStartForm, {
      repositories: repos,
      repository: "eggy",
      task: "add a line",
      busy: false,
      onRepositoryChange: () => {},
      onTaskChange: () => {},
      onStart: () => {},
    }),
  );
  expect(markup).toContain(AGENT_START_COPY.replace(/'/g, "&#x27;"));
  expect(markup.indexOf("open a pull request")).toBeLessThan(markup.indexOf("Start run"));
});

test("with no repository ready the start form says what to do instead of offering an empty list", () => {
  const markup = renderToStaticMarkup(
    createElement(AgentRunStartForm, {
      repositories: [{ ...repos[0], enabled: false }, { ...repos[0], name: "later", pending: true, needs_restart: true }],
      repository: "",
      task: "",
      busy: false,
      onRepositoryChange: () => {},
      onTaskChange: () => {},
      onStart: () => {},
    }),
  );
  expect(markup).toContain("No repository is ready");
  expect(markup).not.toContain("Start run");
});

test("the token field never shows a saved token, and reports only the login and access", () => {
  const markup = renderToStaticMarkup(
    createElement(GitHubTokenView, {
      status: { connected: true, login: "nigel", generation: 3, repositories: { eggy: true } },
      draft: "",
      saving: false,
      error: null,
      onDraftChange: () => {},
      onSave: () => {},
      onDisconnect: () => {},
    }),
  );
  expect(markup).toContain('type="password"');
  expect(markup).toContain("Connected as nigel");
  expect(markup).toContain("eggy: push access");
  expect(markup).not.toContain("ghp_");
  expect(markup).not.toMatch(/value="[^"]+"/);
});

test("the settings card asks for a restart exactly when saved and running differ", () => {
  const base = {
    enabled: true,
    model: "deepseek-pro",
    skills: { brainstorming: true },
    saving: false,
    error: null,
    onEnabledChange: () => {},
    onModelChange: () => {},
    onSkillChange: () => {},
    onSave: () => {},
  };
  const differs = renderToStaticMarkup(createElement(AgentRunnerSettingsView, { ...base, savedEnabled: true, running: false }));
  expect(differs).toContain("Restart to apply");
  const same = renderToStaticMarkup(createElement(AgentRunnerSettingsView, { ...base, savedEnabled: true, running: true }));
  expect(same).not.toContain("Restart to apply");
});

test("the Extensions menu lists Agent Runner only while its feature is running", () => {
  expect(runningExtensions([]).map((e) => e.id)).not.toContain("agent_runner");
  expect(runningExtensions(["agent_runner"]).map((e) => e.id)).toContain("agent_runner");
});

test("the add-repository form refuses an empty name or URL before sending", () => {
  const markup = renderToStaticMarkup(
    createElement(AddRepositoryForm, {
      name: "",
      cloneURL: "",
      baseBranch: "",
      busy: false,
      onChange: () => {},
      onAdd: () => {},
      onCancel: () => {},
    }),
  );
  expect(markup).toContain("Add repository");
  expect(markup).toContain("https://github.com/owner/repo.git");
  expect(markup).toContain("disabled");
});

// The extension window has a fixed height, so the page must scroll itself. The
// finance page does it with this wrapper; without it, content is clipped.
test("the Agent Runner page owns its scrolling, as the finance page does", async () => {
  const source = await Bun.file(new URL("../../../src/extensions/agent-runner/AgentRunnerPage.tsx", import.meta.url)).text();
  expect(source).toContain("h-full min-h-0 overflow-y-auto");
});

test("the Agent Runner window opens on its start tab, with the previous runs tab beside it", () => {
  const markup = renderToStaticMarkup(createElement(AgentRunnerPage, { onClose: () => {}, onSessionExpired: () => {} }));
  expect(markup).toContain('role="tablist"');
  expect(markup).toContain("Previous runs");
  expect(markup).toContain("Start a run");
  expect(markup).toContain('aria-selected="true"');
});
