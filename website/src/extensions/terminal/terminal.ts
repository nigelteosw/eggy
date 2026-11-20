import { request } from "../../lib/api";

// The terminal extension's client: its API calls and the pure rules the page
// renders. The components draw these; they do not decide them.

export type TerminalInfo = {
  address: string;
  space: string;
  name: string;
  title: string;
  live: boolean;
  status?: string;
  message?: string;
  app?: string;
  changed: string;
  exit_code?: number;
};

// repo marks a configured repository's working copy; preparing, one still
// being copied from its mirror, which has no folder yet.
export type TerminalSpace = { name: string; repo?: boolean; preparing?: boolean; error?: string; terminals: TerminalInfo[] };

export async function listSpaces(): Promise<{ spaces: TerminalSpace[]; commands: string[] }> {
  return request("/api/terminal/spaces");
}

export async function createSpace(name: string): Promise<void> {
  await request("/api/terminal/spaces", { method: "POST", body: JSON.stringify({ name }) });
}

export async function deleteSpace(name: string): Promise<void> {
  await request(`/api/terminal/spaces/${encodeURIComponent(name)}`, { method: "DELETE" });
}

export async function updateRepo(name: string): Promise<void> {
  await request(`/api/terminal/spaces/${encodeURIComponent(name)}/update`, { method: "POST", body: "{}" });
}

export async function startTerminal(space: string, command: string, cols: number, rows: number): Promise<TerminalInfo> {
  return request("/api/terminal/terminals", { method: "POST", body: JSON.stringify({ space, command, cols, rows }) });
}

export async function killTerminal(address: string): Promise<void> {
  await request(`/api/terminal/terminals/${encodeURIComponent(address)}`, { method: "DELETE" });
}

export function attachURL(address: string, cols: number, rows: number, location: Location = window.location): string {
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${location.host}/api/terminal/terminals/${encodeURIComponent(address)}/attach?cols=${cols}&rows=${rows}`;
}

// The Inbox's sections, in the order a person should look at them. A status
// word the program made up lands in Idle rather than being dropped.
export type InboxSection = "needs_you" | "done" | "working" | "idle";
export const INBOX_ORDER: InboxSection[] = ["needs_you", "done", "working", "idle"];
export const INBOX_LABEL: Record<InboxSection, string> = {
  needs_you: "Needs you",
  done: "Done",
  working: "Working",
  idle: "Idle",
};

export function sectionOf(t: TerminalInfo): InboxSection {
  if (!t.live) return "done";
  switch (t.status) {
    case "blocked":
      return "needs_you";
    case "done":
    case "error":
      return "done";
    case "working":
      return "working";
    default:
      return "idle";
  }
}

// inbox groups every terminal by section, newest change first within each.
export function inbox(spaces: TerminalSpace[]): { section: InboxSection; terminals: TerminalInfo[] }[] {
  const all = spaces.flatMap((space) => space.terminals);
  return INBOX_ORDER.map((section) => ({
    section,
    terminals: all.filter((t) => sectionOf(t) === section).sort((a, b) => Date.parse(b.changed) - Date.parse(a.changed)),
  })).filter((group) => group.terminals.length > 0);
}

export function needsYouCount(spaces: TerminalSpace[]): number {
  return spaces.flatMap((s) => s.terminals).filter((t) => sectionOf(t) === "needs_you").length;
}

// A terminal's label: its title if the program set one, otherwise the command.
export function terminalLabel(t: TerminalInfo): string {
  return t.title.trim() || t.name || t.address;
}

const COMMAND_LABEL: Record<string, string> = { shell: "Shell", claude: "Claude Code", codex: "Codex" };
export function commandLabel(command: string): string {
  return COMMAND_LABEL[command] ?? command;
}

// age is a compact "how long since it last changed": now, 4m, 2h, 3d.
export function age(changed: string, now: number = Date.now()): string {
  const seconds = Math.max(0, Math.floor((now - Date.parse(changed)) / 1000));
  if (seconds < 60) return "now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

// The phone's key bar: what each key sends to the program. A held modifier
// (ctrl or alt) applies to the next key typed, and then lets go.
export const KEY_BAR: { label: string; send?: string; modifier?: "ctrl" | "alt" }[] = [
  { label: "esc", send: "\x1b" },
  { label: "ctrl", modifier: "ctrl" },
  { label: "alt", modifier: "alt" },
  { label: "tab", send: "\t" },
  { label: "⇧tab", send: "\x1b[Z" },
  { label: "←", send: "\x1b[D" },
  { label: "↑", send: "\x1b[A" },
  { label: "↓", send: "\x1b[B" },
  { label: "→", send: "\x1b[C" },
  { label: "^C", send: "\x03" },
];

// applyModifier turns typed text into what the held modifier sends: ctrl
// makes a letter its control code, alt prefixes escape.
export function applyModifier(text: string, modifier: "ctrl" | "alt" | null): string {
  if (!modifier || text.length === 0) return text;
  if (modifier === "alt") return "\x1b" + text;
  const ch = text[0];
  const code = ch.toUpperCase().charCodeAt(0);
  if (code >= 64 && code <= 95) return String.fromCharCode(code - 64) + text.slice(1);
  if (ch === " ") return "\x00" + text.slice(1);
  return text;
}

// The compose box sends its text as typed, then Enter. Line breaks inside it
// become carriage returns, which is what a terminal's Enter is.
export function composeBytes(text: string): string {
  return text.replace(/\r?\n/g, "\r") + "\r";
}

export const SPACE_NAME = /^[a-z0-9][a-z0-9._-]{0,47}$/;

// orderedCommands puts Claude Code first: it is what a space is usually for.
export function orderedCommands(commands: string[]): string[] {
  return [...commands].sort((a, b) => (a === "claude" ? -1 : b === "claude" ? 1 : 0));
}

// spaceNote is the line a space shows instead of its terminals, or null when
// it has terminals to show. A problem is drawn as one.
export function spaceNote(space: TerminalSpace): { text: string; problem?: boolean } | null {
  if (space.preparing) return { text: "Copying from GitHub..." };
  if (space.error) return { text: `Could not copy: ${space.error}. Use Fetch from GitHub in the + menu to try again.`, problem: true };
  if (space.terminals.length > 0) return null;
  if (space.repo) return { text: "Press + to start Claude Code or a shell in this repository." };
  return { text: "Nothing running. Press + to start." };
}
