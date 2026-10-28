// Checks the panel at a phone width in headless Chrome and fails on any of the
// three rules a phone screen must keep: no sideways page scroll, no hit area
// under 44 px, and no editable field under 16 px (iOS zooms on focus below it).
// It needs a running panel (bun run dev, or a built binary) and Chrome, so it
// is a script rather than part of bun test.
//
// With EGGY_AUDIT_USER and EGGY_AUDIT_PASSWORD set it signs in first, so the
// pages behind the session are measured too.
//
//   bun run audit:mobile [url] [width]

const url = process.argv[2] ?? "http://localhost:5173/";
const width = Number(process.argv[3] ?? 390);
const port = 9333;
const chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

const chrome = Bun.spawn([chromePath, "--headless=new", "--disable-gpu", "--no-first-run", `--remote-debugging-port=${port}`, `--window-size=${width},844`, "about:blank"], {
  stdout: "ignore",
  stderr: "ignore",
});

let targets: { type: string; webSocketDebuggerUrl: string }[] = [];
for (let attempt = 0; attempt < 50 && targets.length === 0; attempt++) {
  await Bun.sleep(200);
  try {
    targets = (await (await fetch(`http://127.0.0.1:${port}/json`)).json()).filter((target: { type: string }) => target.type === "page");
  } catch {
    // Chrome is still starting.
  }
}
if (targets.length === 0) {
  chrome.kill();
  console.error(`Could not reach Chrome at ${chromePath}`);
  process.exit(2);
}

const ws = new WebSocket(targets[0].webSocketDebuggerUrl);
let nextId = 0;
const pending = new Map<number, (message: unknown) => void>();
ws.onmessage = (event) => {
  const message = JSON.parse(String(event.data));
  pending.get(message.id)?.(message);
};
await new Promise((resolve) => (ws.onopen = resolve));
const send = (method: string, params: Record<string, unknown> = {}) =>
  new Promise<unknown>((resolve) => {
    const id = ++nextId;
    pending.set(id, resolve);
    ws.send(JSON.stringify({ id, method, params }));
  });

// The page measures itself: wide elements, small hit areas and small fields.
const MEASURE = `(() => {
  const viewport = document.documentElement.clientWidth;
  const rect = (el) => el.getBoundingClientRect();
  const label = (el) => el.tagName.toLowerCase() + (el.getAttribute('aria-label') ? '[' + el.getAttribute('aria-label') + ']' : '') + (el.textContent ? ' "' + el.textContent.trim().slice(0, 40) + '"' : '');
  const visible = (el) => rect(el).width > 0 && rect(el).height > 0;
  const wide = [...document.querySelectorAll('body *')].filter((el) => visible(el) && rect(el).right > viewport + 1).map((el) => 'overflows: ' + label(el));
  const small = [...document.querySelectorAll('button, a[href], [role="button"], input[type="checkbox"], input[type="radio"]')]
    .filter((el) => visible(el) && (rect(el).width < 44 || rect(el).height < 44))
    .map((el) => 'hit area under 44px: ' + label(el) + ' ' + Math.round(rect(el).width) + 'x' + Math.round(rect(el).height));
  const fields = [...document.querySelectorAll('input:not([type=checkbox]):not([type=radio]):not([type=range]), textarea, select')]
    .filter((el) => visible(el) && parseFloat(getComputedStyle(el).fontSize) < 16)
    .map((el) => 'field under 16px: ' + label(el) + ' ' + getComputedStyle(el).fontSize);
  const sideways = document.documentElement.scrollWidth > viewport;
  return JSON.stringify({ sideways, problems: [...(sideways ? ['page scrolls sideways: ' + document.documentElement.scrollWidth + 'px wide'] : []), ...wide, ...small, ...fields] });
})()`;

await send("Emulation.setDeviceMetricsOverride", { width, height: 844, deviceScaleFactor: 2, mobile: true });
// A phone's pointer is a finger, so the touch-only rules (the 44 px hit areas
// in index.css) apply to what this measures.
await send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 5 });

const origin = url.replace(/\/$/, "");
// Each page the phone reaches from the rail, plus the settings popup.
const pages = ["/", "/scheduled", "/customize", "/traces", "/settings"];

async function evaluate(expression: string): Promise<unknown> {
  return send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
}

const user = process.env.EGGY_AUDIT_USER;
const password = process.env.EGGY_AUDIT_PASSWORD;
if (user && password) {
  await send("Page.navigate", { url: origin + "/" });
  await Bun.sleep(3000);
  const fill = `(async () => {
    const set = (el, value) => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
      setter.call(el, value);
      el.dispatchEvent(new Event('input', { bubbles: true }));
    };
    const username = document.querySelector('#username');
    if (!username) return 'no sign-in form';
    set(username, ${JSON.stringify(user)});
    set(document.querySelector('#password'), ${JSON.stringify(password)});
    document.querySelector('button[type=submit]').click();
    return 'submitted';
  })()`;
  await evaluate(fill);
  await Bun.sleep(4000);
}

const problems: string[] = [];
for (const page of pages) {
  await send("Page.navigate", { url: origin + page });
  await Bun.sleep(2500);
  const result = (await evaluate(MEASURE)) as { result?: { result?: { value?: string } } };
  // The reply nests the evaluated value one level down: message.result.result.value.
  const measured = result.result?.result?.value;
  const found: string[] = JSON.parse(measured ?? '{"problems":["the page could not be measured"]}').problems;
  for (const problem of found) problems.push(page + ": " + problem);
}
ws.close();
chrome.kill();

if (problems.length > 0) {
  console.error(`${problems.length} phone problem(s) at ${width}px on ${origin}:`);
  for (const problem of problems.slice(0, 80)) console.error(`  ${problem}`);
  process.exit(1);
}
console.log(`No phone problems at ${width}px on ${origin} across ${pages.length} pages`);
