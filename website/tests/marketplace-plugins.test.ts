import { describe, expect, test } from "bun:test";
import { RequestError } from "../src/lib/api";
import {
  approvalFor,
  canInstall,
  editedSkillNames,
  fileKey,
  installBlocked,
  latestOnly,
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
  catalog_sha: "cat456",
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
    // The approval names the catalog commit, never the content commit.
    expect(approval.sha).toBe("cat456");
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
  const skill = (target: string) => ({ kind: "skill", target });
  const mcp = (target: string) => ({ kind: "mcp_server", target });

  test("deletes only the edits the owner ticked", () => {
    const edited = [skill("pdf"), skill("docx"), skill("xlsx")];
    expect(removePayload(edited, new Set([fileKey(skill("docx"))]))).toEqual([skill("docx")]);
  });

  test("ticking a skill does not tick an MCP server of the same name", () => {
    const edited = [mcp("api"), skill("api")];
    expect(removePayload(edited, new Set([fileKey(skill("api"))]))).toEqual([skill("api")]);
  });

  test("ticking nothing keeps every edit", () => {
    expect(removePayload([skill("pdf")], new Set())).toEqual([]);
  });

  test("an update review is offered only the edited skills", () => {
    expect(editedSkillNames([mcp("api"), skill("pdf")])).toEqual(["pdf"]);
  });
});

describe("install review", () => {
  test("Install is blocked after a refusal until the owner confirms the re-read", () => {
    expect(installBlocked(false, plan, true)).toBe(true);
    expect(installBlocked(false, plan, false)).toBe(false);
  });

  test("Install is blocked while names conflict or a request is in flight", () => {
    expect(installBlocked(false, { ...plan, conflicts: ["pdf"] }, false)).toBe(true);
    expect(installBlocked(true, plan, false)).toBe(true);
  });

  test("a slow reply for an older rename is ignored", () => {
    const latest = latestOnly();
    const older = latest.start();
    const newer = latest.start();
    expect(latest.isLatest(older)).toBe(false);
    expect(latest.isLatest(newer)).toBe(true);
  });

  test("leaving the review ignores a reply still in flight", () => {
    const latest = latestOnly();
    const pending = latest.start();
    latest.invalidate();
    expect(latest.isLatest(pending)).toBe(false);
  });
});
