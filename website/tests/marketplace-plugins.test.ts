import { describe, expect, test } from "bun:test";
import { RequestError } from "../src/api";
import {
  approvalFor,
  canInstall,
  planRequestFor,
  pluginsEnabled,
  refusalMessage,
  removePayload,
  type Plan,
} from "../src/extensions/marketplace/marketplace";

const plan: Plan = {
  source: "anthropic",
  repo: "anthropics/skills",
  sha: "abc123",
  catalog_sha: "abc123",
  item: "pdf",
  digest: "d1g3st",
  skills: [],
  mcp: [],
  skipped: [],
  conflicts: [],
  index_bytes: 0,
};

describe("plugins tab", () => {
  test("is hidden without the marketplace feature", () => {
    expect(pluginsEnabled(["finance", "agent_runner"])).toBe(false);
  });

  test("shows when the marketplace feature is running", () => {
    expect(pluginsEnabled(["marketplace"])).toBe(true);
  });
});

describe("install approval", () => {
  test("posts the digest and commit the owner was shown", () => {
    const approval = approvalFor(plan, {});
    expect(approval.digest).toBe("d1g3st");
    expect(approval.sha).toBe("abc123");
    expect(approval.source).toBe("anthropic");
    expect(approval.item).toBe("pdf");
  });

  test("a plan with conflicts cannot be installed", () => {
    expect(canInstall(plan)).toBe(true);
    expect(canInstall({ ...plan, conflicts: ["PDF Tools"] })).toBe(false);
  });
});

describe("rename changes", () => {
  test("a rename becomes part of the plan request, so the plan is fetched again", () => {
    const before = planRequestFor("anthropic", "pdf", {});
    const after = planRequestFor("anthropic", "pdf", { "PDF Tools": "pdf-tools" });
    expect(after.renames).toEqual({ "PDF Tools": "pdf-tools" });
    expect(JSON.stringify(after)).not.toBe(JSON.stringify(before));
  });

  test("install_id is sent only when reviewing an update", () => {
    expect(planRequestFor("a", "b", {}).install_id).toBeUndefined();
    expect(planRequestFor("a", "b", {}, "inst-1").install_id).toBe("inst-1");
  });
});

describe("digest refusal", () => {
  test("a stale review says to review again", () => {
    const message = refusalMessage(new RequestError("stale", 409, {}));
    expect(message).toContain("Review it again");
  });

  test("a name collision says to rename", () => {
    const message = refusalMessage(new RequestError("taken", 409, { conflicts: ["pdf"] }));
    expect(message).toContain("Rename");
  });

  test("other failures are not treated as a refusal", () => {
    expect(refusalMessage(new RequestError("nope", 500, {}))).toBeNull();
  });
});

describe("remove", () => {
  test("deletes only the edited skills the owner ticked", () => {
    expect(removePayload(["pdf", "docx", "xlsx"], new Set(["docx"]))).toEqual(["docx"]);
  });

  test("ticking nothing keeps every edit", () => {
    expect(removePayload(["pdf"], new Set())).toEqual([]);
  });
});
