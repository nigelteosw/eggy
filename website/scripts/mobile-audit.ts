// Checks the panel at a phone width in headless Chrome and fails on any of the
// three rules a phone screen must keep: no sideways page scroll, no hit area
// under 44 px, and no editable field under 16 px (iOS zooms on focus below it).
// It needs a running panel (bun run dev, or a built binary) and Chrome, so it
// is a script rather than part of bun test.
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
const pending = new Map<number, (value: { result?: { value?: string; data?: string } }) => void>();
ws.onmessage = (event) => {
  const message = JSON.parse(String(event.data));
  pending.get(message.id)?.(message);
};
await new Promise((resolve) => (ws.onopen = resolve));
const send = (method: string, params: Record<string, unknown> = {}) =>
  new Promise<{ result?: { value?: string; data?: string } }>((resolve) => {
    const id = ++nextId;
    pending.set(id, resolve);
    ws.send(JSON.stringify({ id, method, params }));
  });

await send("Emulation.setDeviceMetricsOverride", { width, height: 844, deviceScaleFactor: 2, mobile: true });
await send("Page.navigate", { url });
await Bun.sleep(4000);

// The page measures itself: wide elements, small hit areas and small fields.
const measure = `(() => {
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

const result = await send("Runtime.evaluate", { expression: measure, returnByValue: true });
ws.close();
chrome.kill();

const { problems } = JSON.parse(result.result?.value ?? '{"problems":["the page could not be measured"]}') as { problems: string[] };
if (problems.length > 0) {
  console.error(`${problems.length} phone problem(s) at ${width}px on ${url}:`);
  for (const problem of problems.slice(0, 60)) console.error(`  ${problem}`);
  process.exit(1);
}
console.log(`No phone problems at ${width}px on ${url}`);
