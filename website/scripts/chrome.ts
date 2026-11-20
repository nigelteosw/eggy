// A headless Chrome driven over the DevTools protocol, shared by the phone
// audit and the screenshot script. No browser library: the handful of calls
// both need is a WebSocket and a counter.

const chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

export type Chrome = {
  send: (method: string, params?: Record<string, unknown>) => Promise<unknown>;
  evaluate: (expression: string) => Promise<unknown>;
  close: () => void;
};

export async function chromeSession(width: number, height: number, mobile: boolean): Promise<Chrome> {
  const port = 9300 + Math.floor(Math.random() * 600);
  const profile = `${process.env.TMPDIR ?? "/tmp"}/eggy-chrome-${port}`;
  const chrome = Bun.spawn(
    [chromePath, "--headless=new", "--disable-gpu", "--no-first-run", `--user-data-dir=${profile}`, `--remote-debugging-port=${port}`, `--window-size=${width},${height}`, "about:blank"],
    { stdout: "ignore", stderr: "ignore" },
  );

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
  // The reply nests the evaluated value one level down: message.result.result.value.
  const evaluate = async (expression: string) =>
    ((await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true })) as { result?: { result?: { value?: unknown } } }).result?.result?.value;

  await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 2, mobile });
  if (mobile) {
    // A phone's pointer is a finger, so the touch-only rules (the 44 px hit
    // areas in index.css) apply to what is measured.
    await send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 5 });
  }

  return {
    send,
    evaluate,
    close: () => {
      ws.close();
      chrome.kill();
      Bun.spawnSync(["rm", "-rf", profile]);
    },
  };
}

// signIn signs in with EGGY_AUDIT_USER and EGGY_AUDIT_PASSWORD when both are
// set, so the pages behind the session are reached too.
export async function signIn(chrome: Chrome, origin: string): Promise<void> {
  const user = process.env.EGGY_AUDIT_USER;
  const password = process.env.EGGY_AUDIT_PASSWORD;
  if (!user || !password) return;
  await chrome.send("Page.navigate", { url: origin + "/" });
  await Bun.sleep(3000);
  await chrome.evaluate(`(async () => {
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
  })()`);
  await Bun.sleep(4000);
}
