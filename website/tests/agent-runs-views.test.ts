import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentRunDetailView, AgentRunsGrid } from "../src/AgentRunsView";
import type { AgentRun } from "../src/api";

const base: AgentRun = { id: "r1", repository: "eggy", state: "running", draft: false, created_at: "2026-10-13T12:59:40Z" };
const failed: AgentRun = {
  ...base,
  id: "r2",
  state: "failed",
  stage: "",
  failure: ".github/workflows/ci.yml: workflow and CI files are not changed by Eggy",
};

test("runs are cards in a three-wide grid, each leading with its issue", () => {
  const markup = renderToStaticMarkup(
    createElement(AgentRunsGrid, { runs: [base, failed], onBack: () => {}, onOpen: () => {} }),
  );
  expect(markup).toContain("md:grid-cols-3");
  expect(markup).toContain(failed.failure!.replace(/'/g, "&#x27;"));
  expect(markup.split("<button").length - 1).toBeGreaterThanOrEqual(3);
});

test("with no runs the grid says so rather than rendering an empty page", () => {
  const markup = renderToStaticMarkup(createElement(AgentRunsGrid, { runs: [], onBack: () => {}, onOpen: () => {} }));
  expect(markup).toContain("No runs yet.");
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
