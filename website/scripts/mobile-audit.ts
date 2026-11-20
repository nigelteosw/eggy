import { auditPages } from "./audit-pages";
import { chromeSession, signIn } from "./chrome";

// Checks the panel at a phone width in headless Chrome and fails on any of the
// rules a small screen must keep: no sideways page scroll, no hit area under
// 44 px, no editable field under 16 px (iOS zooms on focus below it), and no
// text spilling out of the box it sits in. Every screen is measured in both
// themes. It needs a running panel and Chrome; tests/mobile-audit.test.ts runs
// it under bun test when EGGY_AUDIT_URL names one, and checks on every run that
// the page list covers every screen.
//
// With EGGY_AUDIT_USER and EGGY_AUDIT_PASSWORD set it signs in first, so the
// pages behind the session are measured too.
//
//   bun run audit:mobile [url] [width]

const url = process.argv[2] ?? "http://localhost:5173/";
const width = Number(process.argv[3] ?? 390);

// The page measures itself: wide elements, small hit areas, small fields, and
// boxes whose text is wider than they are.
//
// Every disclosure is opened first: what sits behind "Configure" or "Advanced
// options" is as much a screen as what shows on arrival, and the boxes in a
// closed one are not the ones a person sees.
const MEASURE = `(() => {
  document.querySelectorAll('details').forEach((details) => { details.open = true; });
  const viewport = document.documentElement.clientWidth;
  const rect = (el) => el.getBoundingClientRect();
  const label = (el) => el.tagName.toLowerCase() + (el.getAttribute('aria-label') ? '[' + el.getAttribute('aria-label') + ']' : '') + (el.textContent ? ' "' + el.textContent.trim().slice(0, 40) + '"' : '');
  const visible = (el) => rect(el).width > 0 && rect(el).height > 0;
  const wide = [...document.querySelectorAll('body *')].filter((el) => visible(el) && rect(el).right > viewport + 1).map((el) => 'overflows: ' + label(el));
  // A checkbox or radio is hit through the label around it, which is what a
  // switch's visually hidden input relies on, so the label is what is measured.
  const target = (el) => (el.matches('input') && el.closest('label')) || el;
  const small = [...document.querySelectorAll('button, a[href], [role="button"], input[type="checkbox"], input[type="radio"]')]
    .filter((el) => { const hit = target(el); return visible(hit) && (rect(hit).width < 44 || rect(hit).height < 44); })
    .map((el) => 'hit area under 44px: ' + label(el) + ' ' + Math.round(rect(el).width) + 'x' + Math.round(rect(el).height));
  const fields = [...document.querySelectorAll('input:not([type=checkbox]):not([type=radio]):not([type=range]), textarea, select')]
    .filter((el) => visible(el) && parseFloat(getComputedStyle(el).fontSize) < 16)
    .map((el) => 'field under 16px: ' + label(el) + ' ' + getComputedStyle(el).fontSize);
  // Spilled text: a box that lets its own text run past its edge. A box that
  // clips (truncate, line-clamp, a scroller) chose to, and an inline element
  // has no box of its own to spill out of, so neither is counted. Only boxes
  // holding text directly are measured, so a wide child is reported once, as
  // itself, rather than again by every ancestor.
  const spilled = [...document.querySelectorAll('body *')]
    .filter((el) => {
      if (!visible(el) || el.clientWidth === 0) return false;
      const style = getComputedStyle(el);
      if (style.display === 'inline' || style.overflowX !== 'visible') return false;
      if (![...el.childNodes].some((node) => node.nodeType === 3 && node.textContent.trim())) return false;
      return el.scrollWidth > el.clientWidth + 1;
    })
    .map((el) => 'text spills out of its box: ' + label(el) + ' ' + el.scrollWidth + '>' + el.clientWidth);
  const sideways = document.documentElement.scrollWidth > viewport;
  return JSON.stringify({ sideways, problems: [...(sideways ? ['page scrolls sideways: ' + document.documentElement.scrollWidth + 'px wide'] : []), ...wide, ...small, ...fields, ...spilled] });
})()`;

const origin = url.replace(/\/$/, "");
const pages = auditPages();
const chrome = await chromeSession(width, 844, true);
await signIn(chrome, origin);

const problems: string[] = [];
for (const theme of ["light", "dark"]) {
  await chrome.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: theme }] });
  for (const page of pages) {
    await chrome.send("Page.navigate", { url: origin + page });
    await Bun.sleep(2500);
    const measured = (await chrome.evaluate(MEASURE)) as string | undefined;
    const found: string[] = JSON.parse(measured ?? '{"problems":["the page could not be measured"]}').problems;
    for (const problem of found) problems.push(`${page} (${theme}): ${problem}`);
  }
}
chrome.close();

if (problems.length > 0) {
  console.error(`${problems.length} problem(s) at ${width}px on ${origin}:`);
  for (const problem of problems.slice(0, 80)) console.error(`  ${problem}`);
  process.exit(1);
}
console.log(`No problems at ${width}px on ${origin} across ${pages.length} pages in both themes`);
