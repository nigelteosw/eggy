import { expect, test } from "bun:test";
import { approvalBadge } from "../src/chat/ThreadSidebar";

// The badge is the one yolk mark in the rail: it tells the owner something is
// waiting without opening Home, and it goes away when nothing is.
test("the Home badge shows how many approvals are waiting, and nothing when none are", () => {
  expect(approvalBadge(0)).toBeNull();
  expect(approvalBadge(1)).toBe("1");
  expect(approvalBadge(12)).toBe("12");
  expect(approvalBadge(120)).toBe("99+");
});
