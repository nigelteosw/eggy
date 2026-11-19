import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { cn } from "../../lib/utils";
import { KEY_BAR, applyModifier, attachURL, composeBytes } from "./terminal";

// The terminal's own palette. A terminal draws programs' colours, which assume
// a dark ground, so it stays dark in either panel mode.
const PALETTE = {
  background: "#131619",
  foreground: "#e6e7e9",
  cursor: "#e6e7e9",
  selectionBackground: "#323844",
  black: "#454c59",
  red: "#e06c75",
  green: "#98c379",
  yellow: "#e5c07b",
  blue: "#61afef",
  magenta: "#c678dd",
  cyan: "#56b6c2",
  white: "#abb2bf",
  brightBlack: "#767676",
  brightRed: "#e06c75",
  brightGreen: "#98c379",
  brightYellow: "#e5c07b",
  brightBlue: "#61afef",
  brightMagenta: "#c678dd",
  brightCyan: "#56b6c2",
  brightWhite: "#ffffff",
};

type Connection = "connecting" | "open" | "closed";

// TerminalView attaches to one terminal. Closing the view detaches; the
// program keeps running on the server.
export function TerminalView({ address, onEnded }: { address: string; onEnded?: (reason: string) => void }) {
  const host = useRef<HTMLDivElement>(null);
  const socket = useRef<WebSocket | null>(null);
  const [connection, setConnection] = useState<Connection>("connecting");
  const [ended, setEnded] = useState<string | null>(null);
  const [modifier, setModifier] = useState<"ctrl" | "alt" | null>(null);
  const modifierRef = useRef<"ctrl" | "alt" | null>(null);
  const [composing, setComposing] = useState(false);
  const [draft, setDraft] = useState("");
  const [attempt, setAttempt] = useState(0);
  modifierRef.current = modifier;

  const send = (text: string) => {
    const ws = socket.current;
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(text));
  };

  useEffect(() => {
    if (!host.current) return;
    const term = new Terminal({
      fontFamily: "var(--font-mono), ui-monospace, SFMono-Regular, Menlo, monospace",
      fontSize: 15,
      theme: PALETTE,
      cursorBlink: true,
      scrollback: 5000,
      allowProposedApi: false,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host.current);
    try {
      fit.fit();
    } catch {
      // A hidden container has no size yet; the resize observer fits it later.
    }
    const ws = new WebSocket(attachURL(address, term.cols, term.rows));
    ws.binaryType = "arraybuffer";
    socket.current = ws;
    setConnection("connecting");
    setEnded(null);
    ws.onopen = () => {
      setConnection("open");
      ws.send(JSON.stringify({ resize: [term.cols, term.rows] }));
      term.focus();
    };
    ws.onmessage = (event) => {
      if (typeof event.data === "string") {
        try {
          const msg = JSON.parse(event.data) as { closed?: { reason: string; exit_code?: number } };
          if (msg.closed) {
            const reason = msg.closed.exit_code !== undefined ? `${msg.closed.reason} (${msg.closed.exit_code})` : msg.closed.reason;
            setEnded(reason);
            onEnded?.(reason);
          }
        } catch {
          // Not a control message; nothing else is sent as text.
        }
        return;
      }
      term.write(new Uint8Array(event.data as ArrayBuffer));
    };
    ws.onclose = () => setConnection("closed");
    const input = term.onData((data) => {
      const held = modifierRef.current;
      if (held) setModifier(null);
      send(applyModifier(data, held));
    });
    const resize = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ resize: [cols, rows] }));
    });
    const observer = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        // Still hidden.
      }
    });
    observer.observe(host.current);
    return () => {
      observer.disconnect();
      input.dispose();
      resize.dispose();
      ws.close();
      socket.current = null;
      term.dispose();
    };
    // attempt reconnects on demand; onEnded is a notification only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [address, attempt]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-[#131619]">
      <div ref={host} className="min-h-0 flex-1 overflow-hidden px-2 pt-2" />
      {(connection !== "open" || ended) && (
        <div role="status" className="flex items-center gap-3 border-t border-white/10 px-3 py-2 text-small text-neutral-300">
          <span>{ended ? `Program ended: ${ended}.` : connection === "connecting" ? "Connecting..." : "Disconnected. The program is still running."}</span>
          {!ended && connection === "closed" && (
            <button type="button" className="rounded-control px-2 py-1 text-[#e8e9eb] underline" onClick={() => setAttempt((n) => n + 1)}>
              Reconnect
            </button>
          )}
        </div>
      )}
      {composing && (
        <form
          className="flex gap-2 border-t border-white/10 p-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (draft.length > 0) send(composeBytes(draft));
            setDraft("");
            setComposing(false);
          }}
        >
          <textarea
            autoFocus
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            rows={3}
            aria-label="Compose text to send to the terminal"
            placeholder="Write here, then send it to the terminal"
            className="min-w-0 flex-1 resize-none rounded-control bg-[#17191c] p-2 text-field text-[#e8e9eb] outline-none ring-1 ring-white/10 focus:ring-[#3b82f6]"
          />
          <button type="submit" className="self-end rounded-control bg-[#3b82f6] px-3 py-2 text-small font-medium text-white">
            Send
          </button>
        </form>
      )}
      <KeyBar
        modifier={modifier}
        composing={composing}
        onKey={(key) => {
          if (key.modifier) {
            setModifier((held) => (held === key.modifier ? null : key.modifier!));
            return;
          }
          if (key.send) send(key.send);
        }}
        onCompose={() => setComposing((open) => !open)}
      />
    </div>
  );
}

// KeyBar is the row above a phone's keyboard: the keys a terminal needs that a
// phone keyboard lacks, and the compose button. Hidden where a hardware
// keyboard is the norm.
export function KeyBar({
  modifier,
  composing,
  onKey,
  onCompose,
}: {
  modifier: "ctrl" | "alt" | null;
  composing: boolean;
  onKey: (key: (typeof KEY_BAR)[number]) => void;
  onCompose: () => void;
}) {
  return (
    <div className="scrollbar-slim flex items-center gap-1 overflow-x-auto border-t border-white/10 px-2 py-1.5 md:hidden" aria-label="Terminal keys">
      {KEY_BAR.map((key) => (
        <button
          key={key.label}
          type="button"
          // Keep focus in the terminal so the phone keyboard stays up.
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => onKey(key)}
          aria-pressed={key.modifier ? modifier === key.modifier : undefined}
          className={cn(
            "h-11 min-w-11 shrink-0 rounded-control px-2.5 font-mono text-small text-[#e8e9eb]",
            key.modifier && modifier === key.modifier ? "bg-[#3b82f6] text-white" : "bg-[#17191c]",
          )}
        >
          {key.label}
        </button>
      ))}
      <button
        type="button"
        onClick={onCompose}
        aria-pressed={composing}
        aria-label="Compose"
        className={cn("ml-auto h-11 shrink-0 rounded-control px-3 text-small", composing ? "bg-[#3b82f6] text-white" : "bg-[#17191c] text-[#e8e9eb]")}
      >
        Compose
      </button>
    </div>
  );
}
