import { auditPages } from "./audit-pages";
import { chromeSession, signIn } from "./chrome";

// Screenshots of every screen the phone audit measures, at a phone, a tablet
// and two desktop widths, in both themes. It is for looking, not for passing:
// the audit catches what can be measured, and this shows what a change looks
// like before and after. Needs a running panel and Chrome, like the audit.
//
//   bun run screens [url] [outdir]
//
// With EGGY_AUDIT_USER and EGGY_AUDIT_PASSWORD set it signs in first.

const url = (process.argv[2] ?? "http://localhost:5173/").replace(/\/$/, "");
const outdir = process.argv[3] ?? "screens";

const SIZES = [
  { name: "phone", width: 390, height: 844, mobile: true },
  { name: "tablet", width: 820, height: 1180, mobile: true },
  { name: "desktop", width: 1440, height: 900, mobile: false },
  { name: "wide", width: 2000, height: 1100, mobile: false },
];

const slug = (page: string) => page.replace(/^\//, "").replace(/[/?=]/g, "-") || "chat";

for (const size of SIZES) {
  const chrome = await chromeSession(size.width, size.height, size.mobile);
  await signIn(chrome, url);
  for (const theme of ["light", "dark"]) {
    await chrome.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: theme }] });
    for (const page of auditPages()) {
      await chrome.send("Page.navigate", { url: url + page });
      await Bun.sleep(2500);
      const shot = (await chrome.send("Page.captureScreenshot", { format: "png" })) as { result: { data: string } };
      await Bun.write(`${outdir}/${size.name}/${theme}/${slug(page)}.png`, Buffer.from(shot.result.data, "base64"));
    }
  }
  chrome.close();
}
console.log(`Wrote ${SIZES.length * 2 * auditPages().length} screenshots to ${outdir}/`);
