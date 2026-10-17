import { describe, expect, test } from "bun:test";
import { approvalPayload, initialApprovals } from "../src/McpEnableDialog";

const tools = [
  { name: "read", description: "Reads" },
  { name: "write", description: "Writes" },
  { name: "delete", description: "Deletes" },
];

describe("MCP enable review", () => {
  test("every tool starts ticked, so the owner is asked about all of them", () => {
    expect(approvalPayload(tools, initialApprovals(tools))).toEqual(["read", "write", "delete"]);
  });

  test("unticking a tool leaves it out of require_approval", () => {
    const ticked = initialApprovals(tools);
    ticked.delete("read");
    expect(approvalPayload(tools, ticked)).toEqual(["write", "delete"]);
  });

  test("no ticked tools means no approvals, which the owner chose on purpose", () => {
    expect(approvalPayload(tools, new Set())).toEqual([]);
  });

  test("payload follows the server's order, not the order of ticking", () => {
    const ticked = new Set(["delete", "read"]);
    expect(approvalPayload(tools, ticked)).toEqual(["read", "delete"]);
  });
});
