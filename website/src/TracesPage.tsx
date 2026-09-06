import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getTrace,
  listThreads,
  listTraces,
  SessionExpiredError,
  TracingDisabledError,
  type TraceDetail,
  type TraceSpan,
  type TraceSummary,
} from "./api";
import { Button } from "./components/ui/button";
import { ChevronLeftIcon, ChevronDownIcon } from "./components/ui/icons";

// Browse conversations, inspect one turn, then select a step without moving the timeline.

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60000)}m ${Math.round((ms % 60000) / 1000)}s`;
}

function formatTime(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return at.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function formatTokens(count: number): string {
  if (count < 1000) return `${count}`;
  return `${(count / 1000).toFixed(1)}k`;
}

// Bodies are stored as the text that actually crossed the boundary. Anything
// that parses as JSON is pretty-printed because that is how it is read;
// anything else is shown exactly as recorded rather than guessed at.
function prettyBody(body: string): string {
  if (!body) return "";
  try {
    return JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    return body;
  }
}

type PromptMessage = { role?: string; content?: string; name?: string; tool_call_id?: string; tool_calls?: unknown[] };
type PromptRecord = {
  model?: string;
  reasoning_effort?: string;
  messages?: PromptMessage[];
  tool_names?: string[];
  tools?: unknown[];
};

function parsePrompt(request: string): PromptRecord | null {
  try {
    const parsed = JSON.parse(request) as PromptRecord;
    return Array.isArray(parsed.messages) ? parsed : null;
  } catch {
    return null;
  }
}

// What set a turn off is one question, and a trace answers it with two
// fields: kind is why it ran and source is where the owner typed. "Owner" on
// its own is not the answer anyone wants -- every owner turn is from the
// owner, and what distinguishes them is whether it arrived from Telegram or
// from this panel. So an owner turn is labelled by its surface and an
// unprompted turn by what woke it, which makes one column that always names
// the thing that started the turn.
const SOURCE_LABEL: Record<string, string> = {
  telegram: "Telegram",
  web: "Web",
  scheduled: "Scheduled",
  heartbeat: "Heartbeat",
};

function sourceOf(trace: TraceSummary): string {
  const origin = trace.kind === "owner" ? trace.source || trace.channel : trace.kind;
  return SOURCE_LABEL[origin] ?? origin ?? "turn";
}

// Owner turns are the ones somebody is waiting on, so they carry the primary
// tint; the unprompted ones stay muted. The colour is the fastest way to skim
// the column for "which of these did I ask for".
function SourceBadge({ trace }: { trace: TraceSummary }) {
  const prompted = trace.kind === "owner";
  return (
    <span
      className={`whitespace-nowrap rounded border px-1.5 py-0.5 text-xs font-medium uppercase tracking-wide ${
        prompted ? "border-primary/40 bg-primary/10 text-primary" : "border-border text-muted-foreground"
      }`}
    >
      {sourceOf(trace)}
    </span>
  );
}

// A prompt is a conversation, so it is rendered as one. The raw JSON stays one
// click away: the rendered view is what a prompt is for, and the raw view is
// what you check it against.
function Prompt({ request }: { request: string }) {
  const [raw, setRaw] = useState(false);
  const prompt = parsePrompt(request);
  if (!prompt || raw) {
    return (
      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <span className="text-xs font-medium text-muted-foreground">Request</span>
          {prompt && (
            <button
              type="button"
              className="text-xs text-muted-foreground underline-offset-2 hover:underline"
              onClick={() => setRaw(false)}
            >
              Show as conversation
            </button>
          )}
        </div>
        <pre className="code-panel max-h-96">{prettyBody(request)}</pre>
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">
          Prompt · {prompt.messages?.length ?? 0} messages · {prompt.tool_names?.length ?? 0} tools offered
        </span>
        <button
          type="button"
          className="text-xs text-muted-foreground underline-offset-2 hover:underline"
          onClick={() => setRaw(true)}
        >
          Show raw JSON
        </button>
      </div>
      <div className="flex flex-col gap-2.5">
        {prompt.messages?.map((message, index) => (
          <div key={index} className="rounded-md border bg-card p-3.5">
            <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.1em] text-muted-foreground">
              <span>{message.role ?? "message"}</span>
              {message.name && <span className="font-normal normal-case text-foreground/70">{message.name}</span>}
              {Array.isArray(message.tool_calls) && message.tool_calls.length > 0 && (
                <span className="font-normal normal-case">{message.tool_calls.length} tool call(s)</span>
              )}
            </div>
            <pre className="scrollbar-slim max-h-64 overflow-auto whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-foreground/90">
              {message.content ||
                (Array.isArray(message.tool_calls) ? JSON.stringify(message.tool_calls, null, 2) : "")}
            </pre>
          </div>
        ))}
      </div>
      {prompt.tool_names && prompt.tool_names.length > 0 && (
        <p className="text-xs text-muted-foreground">Tools offered: {prompt.tool_names.join(", ")}</p>
      )}
    </div>
  );
}

// --- The waterfall -------------------------------------------------------
//
// Spans carry a wall-clock start and a duration, so they can be laid against
// one shared axis the way a browser's network panel lays out requests. Model
// calls and tool calls are drawn in different colours because the whole point
// of the picture is which of the two is eating the turn.

type Placed = { span: TraceSpan; offset: number; duration: number };

type Layout = { placed: Placed[]; window: number; ticks: number[] };

function spanStart(span: TraceSpan): number {
  const at = new Date(span.started_at).getTime();
  return Number.isNaN(at) ? 0 : at;
}

// Ticks land on a 1/2/5 x 10^n step so the axis reads in round numbers at any
// zoom, the same rule a chart axis uses.
function tickStep(window: number): number {
  const rough = window / 5;
  const magnitude = 10 ** Math.floor(Math.log10(Math.max(rough, 1)));
  for (const factor of [1, 2, 5]) {
    if (magnitude * factor >= rough) return magnitude * factor;
  }
  return magnitude * 10;
}

export function timelineTicks(window: number): number[] {
  const step = tickStep(window);
  const ticks: number[] = [];
  // The duration is always printed at the far edge. Leave at least half one
  // tick interval before it so a near-round duration (15.2s) does not print
  // on top of the final round tick (15.0s).
  for (let at = step; at <= window - step / 2; at += step) ticks.push(at);
  return ticks;
}

function layoutSpans(trace: TraceSummary, spans: TraceSpan[]): Layout {
  const starts = spans.map(spanStart).filter((at) => at > 0);
  const traceStart = new Date(trace.started_at).getTime();
  const origin = Math.min(...(Number.isNaN(traceStart) ? starts : [traceStart, ...starts]));
  const placed = spans.map((span) => {
    const at = spanStart(span);
    return {
      span,
      offset: at > 0 && origin > 0 ? Math.max(at - origin, 0) : 0,
      duration: Math.max(span.duration_ms, 0),
    };
  });
  // The turn is wider than its steps -- there is orchestration either side of
  // them -- so the axis runs to whichever ends last.
  const spanEnd = placed.reduce((furthest, item) => Math.max(furthest, item.offset + item.duration), 0);
  const window = Math.max(spanEnd, trace.duration_ms, 1);
  const ticks = timelineTicks(window);
  return { placed, window, ticks };
}

function barColor(span: TraceSpan): string {
  if (span.error) return "bg-destructive";
  return span.kind === "model_call" ? "bg-primary" : "bg-sky-500 dark:bg-sky-400";
}

function TickLines({ ticks, window }: { ticks: number[]; window: number }) {
  return (
    <div className="pointer-events-none absolute inset-0">
      {ticks.map((at) => (
        <div
          key={at}
          className="absolute top-0 bottom-0 w-px bg-border/70"
          style={{ left: `${(at / window) * 100}%` }}
        />
      ))}
    </div>
  );
}

// A duration has three places it can go and only one of them is right for a
// given bar: inside it when the bar is wide enough to hold the text, after it
// when there is room to the right, and before it when the bar ends at the far
// edge of the axis. Anything simpler puts a number on top of a bar.
function WaterfallBar({ item, window }: { item: Placed; window: number }) {
  const left = (item.offset / window) * 100;
  const width = Math.max((item.duration / window) * 100, 0.6);
  const label = formatDuration(item.duration);
  const placement = width > 14 ? "inside" : left + width > 82 ? "before" : "after";
  const labelStyle =
    placement === "inside"
      ? { left: `${left}%` }
      : placement === "after"
        ? { left: `${left + width}%` }
        : { right: `${100 - left}%` };
  return (
    <div className="relative h-5 flex-1">
      <div
        className={`absolute top-0.5 h-4 rounded-sm ${barColor(item.span)}`}
        style={{ left: `${left}%`, width: `${Math.min(width, 100 - left)}%` }}
        title={`${item.span.name}: ${label} at +${formatDuration(item.offset)}`}
      />
      <span
        className={`absolute top-0 flex h-full items-center whitespace-nowrap text-xs tabular-nums ${
          placement === "inside" ? "px-2 font-medium text-primary-foreground" : "px-1.5 text-muted-foreground"
        }`}
        style={labelStyle}
      >
        {label}
      </span>
    </div>
  );
}

function SpanRow({
  item,
  window,
  ticks,
  selected,
  onSelect,
}: {
  item: Placed;
  window: number;
  ticks: number[];
  selected: boolean;
  onSelect: () => void;
}) {
  const span = item.span;
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={selected}
      aria-label={`Inspect step ${span.sequence}: ${span.name}`}
      className={`trace-step ${selected ? "bg-accent border-l-primary" : "border-l-transparent hover:bg-muted/60"}`}
    >
      <span className="flex min-w-0 items-center gap-3">
        <span className="w-5 shrink-0 text-xs tabular-nums text-muted-foreground">{span.sequence}</span>
        <span className={`h-2 w-2 shrink-0 rounded-full ${barColor(span)}`} />
        <span className="min-w-0">
          <span className="block truncate text-sm font-medium">{span.name}</span>
          <span className="block text-xs text-muted-foreground">
            {span.kind === "model_call" ? "Model generation" : "Tool call"}
            {span.error ? " · Failed" : ""}
          </span>
        </span>
      </span>
      <span className="relative flex min-w-0 items-center">
        <TickLines ticks={ticks} window={window} />
        <WaterfallBar item={item} window={window} />
      </span>
      <ChevronDownIcon className={`h-4 w-4 text-muted-foreground ${selected ? "" : "-rotate-90"}`} />
    </button>
  );
}

function StepInspector({ item }: { item: Placed }) {
  const span = item.span;
  const model = span.kind === "model_call";
  return (
    <section aria-label="Step inspector" className="mt-5 border-t pt-5">
      <div className="mb-4 flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="text-sm font-semibold">
          Step {span.sequence} · {span.name}
        </h3>
        <span className="text-xs text-muted-foreground">
          Started +{formatDuration(item.offset)} · {formatDuration(item.duration)}
        </span>
      </div>
      {span.error && (
        <p role="alert" className="mb-4 text-sm text-destructive">
          {span.error}
        </p>
      )}
      {model && (
        <p className="mb-4 text-xs text-muted-foreground">
          {formatTokens(span.prompt_tokens || 0)} prompt · {formatTokens(span.cached_prompt_tokens || 0)} cached ·{" "}
          {formatTokens(span.completion_tokens || 0)} completion tokens
        </p>
      )}
      <div className="grid min-w-0 gap-5 xl:grid-cols-2">
        <div className="min-w-0">
          <h4 className="mb-3 text-sm font-medium">{model ? "Request" : "Arguments"}</h4>
          {model ? (
            <Prompt request={span.request} />
          ) : (
            <pre className="code-panel max-h-96">{prettyBody(span.request) || "No arguments recorded."}</pre>
          )}
        </div>
        <div className="min-w-0">
          <h4 className="mb-3 text-sm font-medium">{model ? "Response" : "Output"}</h4>
          <pre className="code-panel max-h-96">{prettyBody(span.response) || "No output recorded."}</pre>
        </div>
      </div>
    </section>
  );
}

export function Waterfall({ trace, spans }: { trace: TraceSummary; spans: TraceSpan[] }) {
  const { placed, window, ticks } = useMemo(() => layoutSpans(trace, spans), [trace, spans]);
  const [sequence, setSequence] = useState<number | null>(null);
  const selected = placed.find((item) => item.span.sequence === sequence);
  if (!spans.length) return <p className="py-6 text-sm text-muted-foreground">This turn recorded no steps.</p>;
  return (
    <div>
      <div className="overflow-hidden rounded-md border bg-card">
        <div className="trace-step trace-axis bg-muted/50 text-xs text-muted-foreground">
          <span>Step</span>
          <span className="relative h-4">
            {ticks.map((at) => (
              <span
                key={at}
                className="absolute -translate-x-1/2 text-xs tabular-nums"
                style={{ left: `${(at / window) * 100}%` }}
              >
                {formatDuration(at)}
              </span>
            ))}
            <span className="absolute right-0 text-xs tabular-nums">{formatDuration(window)}</span>
          </span>
          <span />
        </div>
        {placed.map((item) => (
          <SpanRow
            key={item.span.sequence}
            item={item}
            window={window}
            ticks={ticks}
            selected={sequence === item.span.sequence}
            onSelect={() => setSequence(item.span.sequence)}
          />
        ))}
      </div>
      {selected ? (
        <StepInspector key={selected.span.sequence} item={selected} />
      ) : (
        <p className="mt-3 text-xs text-muted-foreground">Select a step to inspect its request and response.</p>
      )}
    </div>
  );
}

export function TraceDetailPanel({ detail }: { detail: TraceDetail }) {
  const { trace, spans } = detail;
  return (
    <article className="min-w-0 space-y-7">
      <header>
        <div className="mb-3 flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
          <SourceBadge trace={trace} />
          <span>{formatTime(trace.started_at)}</span>
          <TraceStatus trace={trace} />
        </div>
        <h2 className="whitespace-pre-wrap break-words text-lg font-semibold leading-relaxed">
          {trace.input || "Unprompted turn"}
        </h2>
        {trace.output && (
          <details className="mt-3">
            <summary className="cursor-pointer text-sm text-muted-foreground">View reply</summary>
            <p className="max-h-64 overflow-auto whitespace-pre-wrap break-words text-sm leading-7">{trace.output}</p>
          </details>
        )}
      </header>
      {trace.error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
        >
          {trace.error}
        </p>
      )}
      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 border-y py-4 2xl:grid-cols-4">
        {[
          ["Duration", formatDuration(trace.duration_ms)],
          ["Tokens", formatTokens(trace.total_tokens)],
          ["Steps", trace.spans],
          ["Model", trace.model || "Unknown"],
        ].map(([label, value]) => (
          <div key={label}>
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className="mt-1 break-words text-sm font-medium tabular-nums">{value}</dd>
          </div>
        ))}
      </dl>
      <section aria-label="Execution timeline">
        <div className="mb-4 flex flex-wrap items-baseline justify-between gap-2">
          <h3 className="text-sm font-semibold">Execution timeline</h3>
          <span className="text-xs text-muted-foreground">
            {formatTokens(trace.prompt_tokens)} prompt · {formatTokens(trace.completion_tokens)} completion ·{" "}
            {formatTokens(trace.cached_prompt_tokens || 0)} cached tokens
            {trace.effort ? ` · ${trace.effort} effort` : ""}
          </span>
        </div>
        <Waterfall key={trace.id} trace={trace} spans={spans} />
      </section>
    </article>
  );
}

function TraceStatus({ trace }: { trace: TraceSummary }) {
  return (
    <span className={`text-xs ${trace.error ? "text-destructive" : "text-muted-foreground"}`}>
      {trace.error ? "Failed" : trace.complete ? "Completed" : "Incomplete"}
    </span>
  );
}

function TraceInspector({ id, onSessionExpired }: { id: string; onSessionExpired: (reason: unknown) => void }) {
  const [detail, setDetail] = useState<TraceDetail | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let current = true;
    setError("");
    getTrace(id)
      .then((value) => {
        if (current) setDetail(value);
      })
      .catch((reason) => {
        if (!current) return;
        setError(reason instanceof Error ? reason.message : "Could not load this turn");
        onSessionExpired(reason);
      });
    return () => {
      current = false;
    };
  }, [id, attempt, onSessionExpired]);
  if (error)
    return (
      <div role="alert">
        <p className="mb-3 text-sm text-destructive">{error}</p>
        <Button variant="outline" onClick={() => setAttempt((value) => value + 1)}>
          Try again
        </Button>
      </div>
    );
  if (!detail)
    return (
      <p role="status" className="text-sm text-muted-foreground">
        Loading turn…
      </p>
    );
  return <TraceDetailPanel detail={detail} />;
}

export type TraceGroup = {
  // key identifies the group; conversationId only names it. /clear splits one
  // conversation into consecutive groups that share a conversationId, so the
  // two are not the same string.
  key: string;
  conversationId: string;
  traces: TraceSummary[];
  startedAt: string;
  lastAt: string;
  durationMs: number;
  totalTokens: number;
  spans: number;
  errors: number;
};

export function groupTracesByConversation(traces: TraceSummary[]): TraceGroup[] {
  const groups: TraceGroup[] = [];
  const byConversation = new Map<string, TraceGroup>();
  for (const trace of traces) {
    // Clearing a conversation ends one line of work and starts another, and
    // Telegram's conversation ID never changes -- without the session in the
    // key its every turn, forever, would be one group.
    const conversationId = trace.conversation_id || "";
    const key = `${conversationId}\u0000${trace.session || ""}`;
    let group = byConversation.get(key);
    if (!group) {
      group = {
        key,
        conversationId,
        traces: [],
        startedAt: trace.started_at,
        lastAt: trace.started_at,
        durationMs: 0,
        totalTokens: 0,
        spans: 0,
        errors: 0,
      };
      byConversation.set(key, group);
      groups.push(group);
    }
    group.traces.push(trace);
    // The list arrives newest-first, but a group's span is read off whatever
    // it actually holds rather than off that assumption.
    if (trace.started_at < group.startedAt) group.startedAt = trace.started_at;
    if (trace.started_at > group.lastAt) group.lastAt = trace.started_at;
    group.durationMs += trace.duration_ms;
    group.totalTokens += trace.total_tokens;
    group.spans += trace.spans;
    if (trace.error) group.errors += 1;
  }
  return groups;
}

// A conversation is named by its thread title where there is one. Telegram's
// thread is a fixed, reserved ID rather than a titled thread, and a turn whose
// thread has since been deleted still has its channel -- so the fallbacks name
// the surface before they fall back to the raw ID.
export function conversationLabel(group: TraceGroup, titles: Record<string, string>): string {
  const title = titles[group.conversationId];
  if (title) return title;
  if (group.conversationId === "telegram") return "Telegram";
  if (!group.conversationId) return "Unassigned turns";
  const channel = group.traces[0]?.channel;
  return channel ? `${SOURCE_LABEL[channel] ?? channel} conversation` : group.conversationId;
}

export function TraceBrowser({
  traces,
  titles = {},
  onSessionExpired,
}: {
  traces: TraceSummary[];
  titles?: Record<string, string>;
  onSessionExpired: (reason: unknown) => void;
}) {
  const groups = useMemo(() => groupTracesByConversation(traces), [traces]);
  const [groupKey, setGroupKey] = useState<string | null>(null);
  const [traceId, setTraceId] = useState<string | null>(null);
  const group = groups.find((item) => item.key === groupKey) ?? groups[0];
  const selected = group?.traces.find((trace) => trace.id === traceId);
  const browserRef = useRef<HTMLDivElement>(null);
  const inspectorRef = useRef<HTMLElement>(null);
  const turnControl = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    if (selected) {
      inspectorRef.current?.focus({ preventScroll: true });
      if (inspectorRef.current) inspectorRef.current.scrollTop = 0;
      if (browserRef.current) browserRef.current.scrollTop = 0;
    } else if (turnControl.current?.isConnected) {
      turnControl.current.focus({ preventScroll: true });
    }
  }, [selected?.id]);
  return (
    <div ref={browserRef} className="trace-browser">
      <aside aria-label="Conversations" className={`trace-conversations ${selected ? "hidden lg:block" : ""}`}>
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Conversations</h2>
          <span className="text-xs text-muted-foreground">{groups.length}</span>
        </div>
        <div className="trace-conversation-list">
          {groups.map((item) => (
            <button
              key={item.key}
              type="button"
              aria-label={`Select conversation ${conversationLabel(item, titles)}`}
              aria-pressed={group?.key === item.key}
              onClick={() => {
                setGroupKey(item.key);
                setTraceId(null);
              }}
              className={`w-full rounded-md border px-3 py-3 text-left transition-colors ${group?.key === item.key ? "border-border bg-card shadow-sm" : "border-transparent hover:bg-muted"}`}
            >
              <span className="block truncate text-sm font-medium">{conversationLabel(item, titles)}</span>
              <span className="mt-1.5 flex flex-wrap gap-x-2 text-xs text-muted-foreground">
                <span>
                  {item.traces.length} {item.traces.length === 1 ? "turn" : "turns"}
                </span>
                {item.errors > 0 && <span className="text-destructive">{item.errors} failed</span>}
              </span>
              <span className="mt-1 block text-xs text-muted-foreground">{formatTime(item.lastAt)}</span>
            </button>
          ))}
        </div>
      </aside>
      <section aria-label="Turns" className={`trace-turns ${selected ? "hidden lg:block" : ""}`}>
        <div className="border-b px-5 py-4">
          <h2 className="truncate text-sm font-semibold">{group ? conversationLabel(group, titles) : "Turns"}</h2>
          <p className="mt-1 text-xs text-muted-foreground">Select a turn to inspect</p>
        </div>
        {group?.traces.map((trace) => (
          <button
            key={trace.id}
            type="button"
            aria-label={`Inspect turn ${trace.input || "Unprompted turn"}`}
            aria-pressed={selected?.id === trace.id}
            onClick={(event) => {
              turnControl.current = event.currentTarget;
              setGroupKey(group.key);
              setTraceId(trace.id);
            }}
            className={`w-full border-b border-l-2 px-5 py-4 text-left transition-colors ${selected?.id === trace.id ? "border-l-primary bg-accent/60" : "border-l-transparent hover:bg-muted/50"}`}
          >
            <span className="mb-2 flex items-center justify-between gap-2">
              <span className="text-xs text-muted-foreground">{formatTime(trace.started_at)}</span>
              <TraceStatus trace={trace} />
            </span>
            <span className="line-clamp-2 break-words text-sm font-medium leading-6">
              {trace.input || "Unprompted turn"}
            </span>
            <span className="mt-3 flex items-center gap-3 text-xs text-muted-foreground">
              <span>{formatDuration(trace.duration_ms)}</span>
              <span>
                {trace.spans} {trace.spans === 1 ? "step" : "steps"}
              </span>
              <span className="ml-auto">Inspect →</span>
            </span>
          </button>
        ))}
      </section>
      <section
        ref={inspectorRef}
        tabIndex={-1}
        aria-label="Turn inspector"
        className={`trace-inspector ${selected ? "" : "hidden lg:block"}`}
      >
        {selected ? (
          <>
            <button
              type="button"
              onClick={() => setTraceId(null)}
              className="mb-5 flex min-h-11 items-center gap-2 text-sm text-muted-foreground lg:hidden"
            >
              <ChevronLeftIcon />
              Back to turns
            </button>
            <TraceInspector key={selected.id} id={selected.id} onSessionExpired={onSessionExpired} />
          </>
        ) : (
          <div className="flex min-h-64 flex-col justify-center gap-2 text-center">
            <h2 className="text-base font-medium">Select a turn to inspect</h2>
            <p className="text-sm text-muted-foreground">Review its outcome, timing, and individual steps.</p>
          </div>
        )}
      </section>
    </div>
  );
}

export function TracesPage({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [titles, setTitles] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  const fail = useCallback(
    (reason: unknown) => {
      if (reason instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(reason instanceof Error ? reason.message : "Could not load traces");
    },
    [onSessionExpired],
  );

  // Detail failures stay in the inspector; expired sessions return to login.
  const rowFailed = useCallback(
    (reason: unknown) => {
      if (reason instanceof SessionExpiredError) onSessionExpired();
    },
    [onSessionExpired],
  );

  const reload = useCallback(() => {
    setLoading(true);
    listTraces()
      .then((rows) => {
        setTraces(rows);
        setError("");
      })
      .catch((reason) => {
        // The routes are absent, not empty, when tracing is off in
        // config.yaml. Saying "no turns recorded yet" there would send the
        // owner looking for a turn that was never going to appear.
        if (reason instanceof TracingDisabledError) {
          setTraces([]);
          setError("Tracing is switched off. Set tracing.enabled to true in config.yaml and restart to record turns.");
          return;
        }
        fail(reason);
      })
      .finally(() => setLoading(false));
    // Thread titles name the conversations. They are a nicety, not part of a
    // trace, so a failure here leaves the groups labelled by their surface
    // rather than taking the page down with it.
    listThreads()
      .then((threads) => setTitles(Object.fromEntries(threads.map((thread) => [thread.id, thread.title]))))
      .catch(() => setTitles({}));
  }, [fail]);

  useEffect(reload, [reload]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b bg-card px-5 py-5 sm:px-7">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Traces</h1>
          <p className="mt-1 text-sm text-muted-foreground">Follow a conversation from request to execution.</p>
        </div>
        <div className="flex items-center gap-4">
          <span className="text-xs text-muted-foreground">{traces.length} recent turns</span>
          <Button variant="outline" onClick={reload} disabled={loading}>
            {loading ? "Loading…" : "Refresh"}
          </Button>
        </div>
      </header>
      {error && (
        <p role="alert" className="m-5 rounded-md border border-destructive/30 p-4 text-sm text-destructive">
          {error}
        </p>
      )}
      {traces.length ? (
        <TraceBrowser traces={traces} titles={titles} onSessionExpired={rowFailed} />
      ) : (
        !error && (
          <div role="status" className="p-12 text-center text-sm text-muted-foreground">
            {loading ? "Loading turns…" : "No turns recorded yet. Send a message and it will appear here."}
          </div>
        )
      )}
    </div>
  );
}
