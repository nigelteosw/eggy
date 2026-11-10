import { expect, test } from "bun:test";
import { VIEW_PATHS } from "../src/app/routing";
import { TAB_IDS } from "../src/customize/CustomizePage";
import { auditPages } from "../scripts/audit-pages";

// The phone audit measures every screen, so a screen added later cannot skip it.
test("the phone audit covers every view and every Customize tab", () => {
  const pages = auditPages();
  for (const path of Object.values(VIEW_PATHS)) expect(pages).toContain(path);
  for (const tab of TAB_IDS.filter((tab) => tab !== "skills")) expect(pages).toContain(`/customize?tab=${tab}`);
});

// With a running panel named, the audit itself is part of the run: 390 px, both
// themes, no sideways scroll, no hit area under 44 px, no field under 16 px.
const url = process.env.EGGY_AUDIT_URL;
test.skipIf(!url)(
  "every screen passes the phone audit at 390 px in both themes",
  async () => {
    const run = Bun.spawn(["bun", "scripts/mobile-audit.ts", url!, "390"], { cwd: `${import.meta.dir}/..`, stdout: "pipe", stderr: "pipe", env: process.env });
    const [code, out, err] = await Promise.all([run.exited, new Response(run.stdout).text(), new Response(run.stderr).text()]);
    if (code !== 0) console.error(err || out);
    expect(code).toBe(0);
  },
  180_000,
);
