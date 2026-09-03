import { useCallback, useEffect, useMemo, useState } from "react";
import {
  getTrace,
  listTraces,
  SessionExpiredError,
  TracingDisabledError,
  type TraceDetail,
  type TraceSpan,
  type TraceSummary,
} from "./api";
import { Button } from "./components/ui/button";
import { ChevronLeftIcon, ChevronDownIcon } from "./components/ui/icons";

// The traces view answers one question: what did that turn actually do. The
// transcript already shows what Eggy said, and the config pages show what it
// is allowed to do -- neither shows the prompt that produced a reply or the
// tool output the model was reacting to, which is everything you want the
// moment a turn surprises you.
//
// Turns are a log, so they are a table: one row per turn, newest first,
// expanding in place into that turn's steps. The steps are drawn as a
// waterfall against a shared clock, because the second question after "what
// did it do" is always "where did the 37 seconds go" -- and a stack of
// durations cannot answer that while a bar chart of them can.

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60000)}m ${Math.round((ms % 60000) / 1000)}s`;
}

function formatTime(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return at.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });
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
type PromptRecord = { model?: string; reasoning_effort?: string; messages?: PromptMessage[]; tool_names?: string[]; tools?: unknown[] };

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
      className={`whitespace-nowrap rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide ${
        prompted ? "border-primary/40 bg-primary/10 text-primary" : "border-border text-muted-foreground"
      }`}
    >
      {sourceOf(trace)}
    </span>
  );
}

function Body({ label, text }: { label: string; text: string }) {
  if (!text) return null;
  return (
    <div className="flex flex-col gap-1">
      <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{label}</span>
      <pre className="scrollbar-slim max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/40 p-3 text-xs leading-relaxed">
        {prettyBody(text)}
      </pre>
    </div>
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
      <div className="flex flex-col gap-1">
        <div className="flex items-center justify-between">
          <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Request</span>
          {prompt && (
            <button type="button" className="text-[11px] text-muted-foreground underline-offset-2 hover:underline" onClick={() => setRaw(false)}>
              Show as conversation
            </button>
          )}
        </div>
        <pre className="scrollbar-slim max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/40 p-3 text-xs leading-relaxed">
          {prettyBody(request)}
        </pre>
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
          Prompt · {prompt.messages?.length ?? 0} messages · {prompt.tool_names?.length ?? 0} tools offered
        </span>
        <button type="button" className="text-[11px] text-muted-foreground underline-offset-2 hover:underline" onClick={() => setRaw(true)}>
          Show raw JSON
        </button>
      </div>
      <div className="flex flex-col gap-2">
        {prompt.messages?.map((message, index) => (
          <div key={index} className="rounded-md border border-border bg-muted/30 p-2.5">
            <div className="mb-1 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
              <span>{message.role ?? "message"}</span>
              {message.name && <span className="font-normal normal-case text-foreground/70">{message.name}</span>}
              {Array.isArray(message.tool_calls) && message.tool_calls.length > 0 && (
                <span className="font-normal normal-case">{message.tool_calls.length} tool call(s)</span>
              )}
            </div>
            <pre className="scrollbar-slim max-h-64 overflow-auto whitespace-pre-wrap break-words text-xs leading-relaxed">
              {message.content || (Array.isArray(message.tool_calls) ? JSON.stringify(message.tool_calls, null, 2) : "")}
            </pre>
          </div>
        ))}
      </div>
      {prompt.tool_names && prompt.tool_names.length > 0 && (
        <p className="text-[11px] text-muted-foreground">Tools offered: {prompt.tool_names.join(", ")}</p>
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
  const step = tickStep(window);
  const ticks: number[] = [];
  for (let at = step; at < window; at += step) ticks.push(at);
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
        <div key={at} className="absolute top-0 bottom-0 w-px bg-border/70" style={{ left: `${(at / window) * 100}%` }} />
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
        className={`absolute top-0 flex h-full items-center whitespace-nowrap text-[10px] tabular-nums ${
          placement === "inside" ? "px-2 font-medium text-primary-foreground" : "px-1.5 text-muted-foreground"
        }`}
        style={labelStyle}
      >
        {label}
      </span>
    </div>
  );
}

function SpanRow({ item, window }: { item: Placed; window: number }) {
  const [open, setOpen] = useState(false);
  const span = item.span;
  const model = span.kind === "model_call";
  return (
    <div className="border-t border-border first:border-t-0">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-3 px-3 py-2 text-left transition-colors hover:bg-muted/40"
      >
        <span className="flex w-[15rem] shrink-0 items-center gap-2 sm:w-[19rem]">
          <span className={`h-2 w-2 shrink-0 rounded-full ${barColor(span)}`} />
          <span className="w-5 shrink-0 text-xs tabular-nums text-muted-foreground">{span.sequence}</span>
          <span className="shrink-0 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {model ? "model" : "tool"}
          </span>
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{span.name}</span>
        </span>
        <span className="relative flex min-w-0 flex-1 items-center">
          <WaterfallBar item={item} window={window} />
        </span>
        <span className="w-16 shrink-0 text-right text-xs tabular-nums text-muted-foreground">
          {span.total_tokens ? `${formatTokens(span.total_tokens)} tok` : ""}
        </span>
        <span className={`inline-flex shrink-0 text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`}>
          <ChevronDownIcon />
        </span>
      </button>
      {open && (
        <div className="flex flex-col gap-3 border-t border-border bg-muted/20 p-4">
          {span.error && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2.5 text-xs text-destructive">{span.error}</div>
          )}
          <div className="flex flex-wrap gap-x-6 gap-y-1 text-[11px] text-muted-foreground">
            <span>Started +{formatDuration(item.offset)} into the turn</span>
            <span>Took {formatDuration(item.duration)}</span>
            {span.prompt_tokens ? <span>Prompt {formatTokens(span.prompt_tokens)} tok</span> : null}
            {span.cached_prompt_tokens ? <span>Cached {formatTokens(span.cached_prompt_tokens)} tok</span> : null}
            {span.completion_tokens ? <span>Completion {formatTokens(span.completion_tokens)} tok</span> : null}
          </div>
          {model ? <Prompt request={span.request} /> : <Body label="Arguments" text={span.request} />}
          <Body label={model ? "Response" : "Output"} text={span.response} />
        </div>
      )}
    </div>
  );
}

function Waterfall({ trace, spans }: { trace: TraceSummary; spans: TraceSpan[] }) {
  const { placed, window, ticks } = useMemo(() => layoutSpans(trace, spans), [trace, spans]);
  if (spans.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border px-4 py-6 text-center text-sm text-muted-foreground">
        This turn recorded no steps.
      </div>
    );
  }
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      {/* The axis header carries the tick labels; the same tick positions are
          repeated as hairlines behind every bar so a bar can be read against
          the ruler without tracing back up to it. */}
      <div className="flex items-center gap-3 border-b border-border bg-muted/40 px-3 py-1.5">
        <span className="w-[15rem] shrink-0 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground sm:w-[19rem]">
          Step
        </span>
        <span className="relative h-4 min-w-0 flex-1">
          {ticks.map((at) => (
            <span
              key={at}
              className="absolute top-0 -translate-x-1/2 text-[10px] tabular-nums text-muted-foreground"
              style={{ left: `${(at / window) * 100}%` }}
            >
              {formatDuration(at)}
            </span>
          ))}
          <span className="absolute top-0 right-0 text-[10px] tabular-nums text-muted-foreground">{formatDuration(window)}</span>
        </span>
        <span className="w-16 shrink-0" />
        <span className="w-4 shrink-0" />
      </div>
      <div className="relative">
        {/* Hairlines are inset to match the track column, which starts after
            the step label and stops before the token and chevron columns. */}
        <div className="pointer-events-none absolute inset-y-0 left-[16.5rem] right-[7.25rem] sm:left-[20.5rem]">
          <TickLines ticks={ticks} window={window} />
        </div>
        {placed.map((item) => (
          <SpanRow key={item.span.sequence} item={item} window={window} />
        ))}
      </div>
      <div className="flex items-center gap-4 border-t border-border bg-muted/20 px-3 py-1.5 text-[10px] text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="h-2 w-3 rounded-sm bg-primary" /> LLM generation</span>
        <span className="flex items-center gap-1.5"><span className="h-2 w-3 rounded-sm bg-sky-500 dark:bg-sky-400" /> Tool call</span>
        <span className="flex items-center gap-1.5"><span className="h-2 w-3 rounded-sm bg-destructive" /> Failed</span>
      </div>
    </div>
  );
}

// --- The expanded turn ---------------------------------------------------

function TraceDetailPanel({ detail }: { detail: TraceDetail }) {
  const { trace, spans } = detail;
  return (
    <div className="flex flex-col gap-3 border-l-2 border-primary/60 bg-muted/20 px-4 py-4 sm:px-6">
      {trace.error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{trace.error}</div>
      )}
      {/* The turn's own message and reply are not repeated here: the row above
          already shows the message, and the transcript is where the reply is
          read. What the transcript cannot show is the shape of the run, so
          that is all this panel is. */}
      <Waterfall trace={trace} spans={spans} />
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
        <span>Replied on {trace.channel || "an unknown surface"}</span>
        {trace.model && <><span className="text-border">/</span><span>{trace.model}</span></>}
        {trace.effort && <><span className="text-border">/</span><span>{trace.effort} effort</span></>}
        <span className="text-border">/</span>
        <span>{formatTokens(trace.prompt_tokens)} tok prompt</span>
        {trace.cached_prompt_tokens ? <><span className="text-border">/</span><span>{formatTokens(trace.cached_prompt_tokens)} tok cached</span></> : null}
        <span className="text-border">/</span>
        <span>{formatTokens(trace.completion_tokens)} tok completion</span>
        {!trace.complete && (
          <span className="rounded border border-amber-500/50 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-amber-600 dark:text-amber-400">
            incomplete
          </span>
        )}
      </div>
    </div>
  );
}

// The expanded turn loads its own detail. Fetching on expand rather than on
// selection keeps a collapsed table cheap however many turns are listed.
function TraceRow({
  trace,
  open,
  onToggle,
  onSessionExpired,
}: {
  trace: TraceSummary;
  open: boolean;
  onToggle: () => void;
  onSessionExpired: (reason: unknown) => void;
}) {
  const [detail, setDetail] = useState<TraceDetail | null>(null);
  const [failed, setFailed] = useState("");

  useEffect(() => {
    if (!open || detail) return;
    let current = true;
    getTrace(trace.id)
      .then((loaded) => {
        if (current) setDetail(loaded);
      })
      .catch((reason) => {
        if (current) setFailed(reason instanceof Error ? reason.message : "Could not load this turn");
        onSessionExpired(reason);
      });
    return () => {
      current = false;
    };
  }, [open, detail, trace.id, onSessionExpired]);

  return (
    <tbody className="border-t border-border">
      <tr
        onClick={onToggle}
        className={`cursor-pointer transition-colors ${open ? "bg-muted/50" : "hover:bg-muted/30"}`}
      >
        <td className="w-8 pl-3 align-middle">
          <span className={`inline-flex text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`}>
            <ChevronDownIcon />
          </span>
        </td>
        <td className="px-3 py-2.5 align-middle"><SourceBadge trace={trace} /></td>
        <td className="whitespace-nowrap px-3 py-2.5 align-middle text-xs text-muted-foreground">{formatTime(trace.started_at)}</td>
        <td className="w-full max-w-0 px-3 py-2.5 align-middle">
          <div className="truncate text-sm text-foreground">{trace.input || trace.output || "(no message)"}</div>
        </td>
        <td className="whitespace-nowrap px-3 py-2.5 text-right align-middle text-xs tabular-nums text-muted-foreground">{trace.spans}</td>
        <td className="whitespace-nowrap px-3 py-2.5 text-right align-middle text-xs tabular-nums text-muted-foreground">
          {formatDuration(trace.duration_ms)}
        </td>
        <td className="whitespace-nowrap px-3 py-2.5 pr-4 text-right align-middle text-xs tabular-nums text-muted-foreground">
          {formatTokens(trace.total_tokens)}
        </td>
        <td className="whitespace-nowrap px-3 py-2.5 pr-4 align-middle text-xs">
          {trace.error ? <span className="text-destructive">error</span> : null}
        </td>
      </tr>
      {open && (
        <tr>
          <td colSpan={8} className="p-0">
            {failed ? (
              <div className="border-l-2 border-destructive/60 bg-destructive/10 px-4 py-3 text-sm text-destructive">{failed}</div>
            ) : detail ? (
              <TraceDetailPanel detail={detail} />
            ) : (
              <div className="border-l-2 border-border px-4 py-4 text-sm text-muted-foreground">Loading this turn...</div>
            )}
          </td>
        </tr>
      )}
    </tbody>
  );
}

export function TracesPage({
  onSessionExpired,
  onBackToChat,
}: {
  onSessionExpired: () => void;
  onBackToChat: () => void;
}) {
  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [expanded, setExpanded] = useState<string | null>(null);
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

  // A row that fails on its own reports the failure inline; only an expired
  // session has to reach the app, so the row handler passes through the rest.
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
  }, [fail]);

  useEffect(reload, [reload]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b border-border p-2">
        <button
          type="button"
          onClick={onBackToChat}
          className="flex h-9 items-center gap-2 rounded-md px-2.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <ChevronLeftIcon />
          Back to chat
        </button>
        <div className="ml-auto">
          <Button variant="ghost" onClick={reload} disabled={loading}>
            {loading ? "Loading..." : "Refresh"}
          </Button>
        </div>
      </div>
      <div className="scrollbar-slim min-h-0 flex-1 overflow-y-auto bg-card/40 px-4 py-6 sm:px-8">
        <div className="mx-auto flex max-w-6xl flex-col gap-4">
          <header className="flex flex-col gap-1">
            <h1 className="text-2xl font-semibold tracking-tight">Traces</h1>
            <p className="text-sm text-muted-foreground">
              Every turn as it ran: open one for the prompt behind each model call, the arguments and output of every tool call, and
              where its time went.
            </p>
          </header>
          {error && <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>}
          {traces.length === 0 ? (
            !error && (
              <div className="rounded-md border border-dashed border-border px-4 py-10 text-center text-sm text-muted-foreground">
                {loading ? "Loading turns..." : "No turns recorded yet. Send a message and it will appear here."}
              </div>
            )
          ) : (
            <div className="overflow-hidden rounded-lg border border-border bg-card">
              <table className="w-full border-collapse text-left">
                <thead className="bg-muted/60">
                  <tr className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                    <th className="w-8" />
                    <th className="px-3 py-2">Source</th>
                    <th className="px-3 py-2">Started</th>
                    <th className="px-3 py-2">Turn</th>
                    <th className="px-3 py-2 text-right">Steps</th>
                    <th className="px-3 py-2 text-right">Duration</th>
                    <th className="px-3 py-2 pr-4 text-right">Tokens</th>
                    <th className="px-3 py-2 pr-4" />
                  </tr>
                </thead>
                {traces.map((trace) => (
                  <TraceRow
                    key={trace.id}
                    trace={trace}
                    open={expanded === trace.id}
                    onToggle={() => setExpanded((current) => (current === trace.id ? null : trace.id))}
                    onSessionExpired={rowFailed}
                  />
                ))}
              </table>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
