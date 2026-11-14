import { afterAll, beforeEach, expect, test } from "bun:test";
import { Window } from "happy-dom";
import type { Root } from "react-dom/client";
import type { Approval, Plan } from "../src/extensions/marketplace/marketplace";

// The DOM globals go in before React DOM is first imported, because it reads
// them once at load.
const window = new Window();
let fetchStub: (path: string, init?: RequestInit) => Promise<Response> = async () => new Response("{}");
const globals = {
  window,
  document: window.document,
  Element: window.Element,
  IS_REACT_ACT_ENVIRONMENT: true,
  fetch: (path: string, init?: RequestInit) => fetchStub(path, init),
};
const previous = new Map(Object.keys(globals).map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
for (const [key, value] of Object.entries(globals)) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
const { act, createElement } = await import("react");
const { createRoot } = await import("react-dom/client");
const { RequestError } = await import("../src/lib/api");
const { InstallReview } = await import("../src/extensions/marketplace/InstallReview");

// Component tests for the review every install and update goes through. The
// plan comes from a stubbed fetch, so what is asserted is what the owner sees
// and what the approval carries.

const basePlan: Plan = {
  source: "team",
  repo: "o/team",
  sha: "2222222222222222222222222222222222222222",
  catalog_sha: "1111111111111111111111111111111111111111",
  item: "deploy-tools",
  digest: "d1g3st",
  skills: [{ name: "pdf", upstream_name: "pdf", description: "Fill PDF forms", body: "Fill forms.", files: [] }],
  mcp: [{ name: "deploy-tools-api", url: "https://mcp.example.com/mcp" }],
  skipped: [{ path: "plugins/deploy/skills/docx/SKILL.md", reason: "you deleted this; updates leave it out" }],
  conflicts: [],
  index_bytes: 20,
};

let container: HTMLElement;
let root: Root | undefined;
let plans: Plan[];
let planRequests: unknown[];

beforeEach(async () => {
  if (root) await act(async () => root!.unmount());
  plans = [];
  planRequests = [];
  fetchStub = async (_path, init) => {
    planRequests.push(JSON.parse(String(init?.body ?? "{}")));
    const next = plans.length > 1 ? plans.shift()! : plans[0];
    return new Response(JSON.stringify(next), { status: 200, headers: { "Content-Type": "application/json" } });
  };
  document.body.innerHTML = "";
  container = document.createElement("div") as unknown as HTMLElement;
  document.body.append(container as never);
  root = createRoot(container);
});

afterAll(async () => {
  if (root) await act(async () => root!.unmount());
  for (const [key, descriptor] of previous) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else delete (globalThis as Record<string, unknown>)[key];
  }
  await window.happyDOM.close();
});

async function render(props: Partial<Parameters<typeof InstallReview>[0]> = {}) {
  await act(async () => {
    root!.render(
      createElement(InstallReview, {
        request: { source: "team", item: "deploy-tools" },
        approveLabel: "Install",
        onApprove: async () => {},
        onBack: () => {},
        onSessionExpired: () => {},
        ...props,
      }),
    );
  });
}

const button = (label: string) =>
  Array.from(container.querySelectorAll("button")).find((b) => b.textContent?.trim() === label) as HTMLButtonElement | undefined;

test("the review shows what will be written, what is left out, and approves exactly that plan", async () => {
  plans = [basePlan];
  const approvals: Approval[] = [];
  await render({ onApprove: async (approval) => void approvals.push(approval) });
  const text = container.textContent ?? "";
  expect(text).toContain("From o/team at 2222222");
  expect(text).toContain("Skills (1)");
  expect(text).toContain("Fill PDF forms");
  expect(text).toContain("Remote servers (added disabled)");
  expect(text).toContain("deploy-tools-api");
  expect(text).toContain("you deleted this; updates leave it out");
  const install = button("Install")!;
  expect(install.disabled).toBe(false);
  await act(async () => install.click());
  expect(approvals).toEqual([{ source: "team", item: "deploy-tools", renames: {}, sha: basePlan.catalog_sha, digest: "d1g3st" }]);
});

test("a conflict blocks install and asks for a rename of each conflicting name", async () => {
  plans = [{ ...basePlan, conflicts: ["pdf"], skills: [] }];
  await render();
  expect(container.textContent).toContain("Rename them to install");
  expect(container.querySelector('input[aria-label="Rename pdf"]')).not.toBeNull();
  expect(button("Install")!.disabled).toBe(true);
});

test("a refusal reloads the plan and keeps Install off until the owner has read it again", async () => {
  plans = [basePlan, { ...basePlan, digest: "changed" }];
  let calls = 0;
  await render({
    onApprove: async () => {
      calls++;
      if (calls === 1) throw new RequestError("the content changed since you reviewed it; review again", 409, {});
    },
  });
  await act(async () => button("Install")!.click());
  expect(container.textContent).toContain("I have read the new version");
  expect(button("Install")!.disabled).toBe(true);
  const reread = Array.from(container.querySelectorAll('input[type="checkbox"]')).at(-1) as unknown as HTMLInputElement;
  await act(async () => reread.click());
  expect(button("Install")!.disabled).toBe(false);
});

test("an update offers to replace local edits only for the skills the owner edited", async () => {
  plans = [{ ...basePlan, skills: [...basePlan.skills, { name: "docx", upstream_name: "docx", description: "Docs", body: "b", files: [] }] }];
  const replaced: string[][] = [];
  await render({ approveLabel: "Update", editedSkills: ["pdf"], onApprove: async (_a, replace) => void replaced.push(replace) });
  const text = container.textContent ?? "";
  expect(text).toContain("Replace my local edits to pdf");
  expect(text).not.toContain("Replace my local edits to docx");
  const box = container.querySelector('input[type="checkbox"]') as unknown as HTMLInputElement;
  await act(async () => box.click());
  await act(async () => button("Update")!.click());
  expect(replaced).toEqual([["pdf"]]);
});
