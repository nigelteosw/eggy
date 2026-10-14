import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentRunDetailView, AgentRunsList } from "../../../src/extensions/agent-runner/AgentRunsView";
import type { AgentRun } from "../../../src/api";

const base: AgentRun = { id: "r1", repository: "eggy", state: "running", draft: false, created_at: "2026-10-13T12:59:40Z" };
const failed: AgentRun = {
  ...base,
  id: "r2",
  state: "failed",
  stage: "",
  failure: ".github/workflows/ci.yml: workflow and CI files are not changed by Eggy",
};

test("the runs list shows each run's headline, with a count on every filter", () => {
  const markup = renderToStaticMarkup(
    createElement(AgentRunsList, { runs: [base, failed], filter: "all", onFilterChange: () => {}, onOpen: () => {} }),
  );
  expect(markup).toContain(failed.failure!.replace(/'/g, "&#x27;"));
  expect(markup).toContain("Needs you");
  expect(markup).toContain('aria-pressed="true"');
});

test("a filter keeps only the runs it names, and says so when none match", () => {
  const markup = renderToStaticMarkup(
    createElement(AgentRunsList, { runs: [base, failed], filter: "needs_you", onFilterChange: () => {}, onOpen: () => {} }),
  );
  expect(markup).toContain("Nothing is waiting for you.");
  expect(markup).not.toContain(failed.failure!.replace(/'/g, "&#x27;"));
});

test("with no runs at all the list says how to start one", () => {
  const markup = renderToStaticMarkup(
    createElement(AgentRunsList, { runs: [], filter: "all", onFilterChange: () => {}, onOpen: () => {} }),
  );
  expect(markup).toContain("No runs yet. Start one from the Agent Runner tab.");
});

test("a run waiting for an answer shows the question and the answer form first", () => {
  const waiting: AgentRun = { ...base, state: "waiting_for_user", question: "Which file?" };
  const markup = renderToStaticMarkup(
    createElement(AgentRunDetailView, { run: waiting, busy: false, onBack: () => {}, onCancel: () => {}, onAnswer: () => {} }),
  );
  expect(markup).toContain("The agent is asking you");
  expect(markup).toContain("Which file?");
  expect(markup).toContain('aria-label="Your answer"');
});

test("a completed run leads with its pull request title and link, and its checkpoints", () => {
  const done: AgentRun = {
    ...base,
    state: "completed",
    stage: "delivered",
    pr_url: "https://github.com/o/eggy/pull/9",
    pr_title: "Paginate finance entries",
  };
  const markup = renderToStaticMarkup(
    createElement(AgentRunDetailView, { run: done, busy: false, onBack: () => {}, onCancel: () => {}, onAnswer: () => {} }),
  );
  expect(markup).toContain("Paginate finance entries");
  expect(markup).toContain("https://github.com/o/eggy/pull/9");
  expect(markup).toContain("Pull request opened");
  expect(markup).not.toContain("Cancel run");
});

test("model calls are hidden by default, with a count to show them", () => {
  const steps = [
    { seq: 1, at: "2026-10-13T12:59:40Z", kind: "started", text: "coding run started" },
    { seq: 2, at: "2026-10-13T12:59:41Z", kind: "model", text: "model call: 900 new tokens" },
    { seq: 3, at: "2026-10-13T12:59:42Z", kind: "tool", text: "read: ok" },
  ];
  const markup = renderToStaticMarkup(
    createElement(AgentRunDetailView, { run: base, steps, busy: false, onBack: () => {}, onCancel: () => {}, onAnswer: () => {} }),
  );
  expect(markup).toContain("Show 1 model call");
  expect(markup).not.toContain("model call: 900 new tokens");
  expect(markup).toContain("read: ok");
});

test("the run page shows the task that was asked, and a row leads with its first line", () => {
  const withTask: AgentRun = { ...base, brief: "Paginate the finance list\nkeep the filters" };
  const page = renderToStaticMarkup(
    createElement(AgentRunDetailView, { run: withTask, busy: false, onBack: () => {}, onCancel: () => {}, onAnswer: () => {} }),
  );
  expect(page).toContain("Paginate the finance list");
  const rows = renderToStaticMarkup(
    createElement(AgentRunsList, { runs: [withTask], filter: "all", onFilterChange: () => {}, onOpen: () => {} }),
  );
  expect(rows).toContain("Paginate the finance list");
  expect(rows).not.toContain("keep the filters");
});

test("a failed run's page leads with why it stopped and what it stopped at", () => {
  const markup = renderToStaticMarkup(
    createElement(AgentRunDetailView, {
      run: { ...failed, stage: "pushed", output: "Committed the change." },
      busy: false,
      onBack: () => {},
      onCancel: () => {},
      onAnswer: () => {},
    }),
  );
  expect(markup.indexOf("Why it stopped")).toBeLessThan(markup.indexOf("Committed the change."));
  expect(markup).toContain(failed.failure!.replace(/'/g, "&#x27;"));
  expect(markup).toContain("Stopped at");
  expect(markup).not.toContain("Cancel run");
});

test("a run still going offers cancel and no reason it has not got yet", () => {
  const markup = renderToStaticMarkup(
    createElement(AgentRunDetailView, { run: base, busy: false, onBack: () => {}, onCancel: () => {}, onAnswer: () => {} }),
  );
  expect(markup).toContain("Cancel run");
  expect(markup).not.toContain("Why it stopped");
});

test("the run page shows what happened, step by step, in order", () => {
  const steps = [
    { seq: 1, at: "2026-10-13T12:59:40Z", kind: "started", text: "coding run started on eggy from main" },
    { seq: 2, at: "2026-10-13T13:00:02Z", kind: "tool", text: "run_check: go test failed with exit 1" },
    { seq: 3, at: "2026-10-13T13:00:09Z", kind: "failed", text: "publish: the change touches .github/workflows" },
  ];
  const markup = renderToStaticMarkup(
    createElement(AgentRunDetailView, { run: { ...failed, stage: "" }, steps, busy: false, onBack: () => {}, onCancel: () => {}, onAnswer: () => {} }),
  );
  const first = markup.indexOf("coding run started");
  const second = markup.indexOf("run_check: go test failed");
  const third = markup.indexOf("touches .github/workflows");
  expect(first).toBeGreaterThan(-1);
  expect(first).toBeLessThan(second);
  expect(second).toBeLessThan(third);
  expect(markup).toContain("What happened");
});

test("a run with no steps yet says so instead of showing an empty section", () => {
  const markup = renderToStaticMarkup(
    createElement(AgentRunDetailView, { run: base, steps: [], busy: false, onBack: () => {}, onCancel: () => {}, onAnswer: () => {} }),
  );
  expect(markup).toContain("No steps recorded yet.");
});

test("a finished run offers delete, and a run in progress does not", () => {
  const props = { busy: false, onBack: () => {}, onCancel: () => {}, onAnswer: () => {}, onDelete: () => {} };
  const finished = renderToStaticMarkup(createElement(AgentRunDetailView, { ...props, run: { ...failed, stage: "" } }));
  expect(finished).toContain("Delete run");
  const moving = renderToStaticMarkup(createElement(AgentRunDetailView, { ...props, run: base }));
  expect(moving).not.toContain("Delete run");
});
