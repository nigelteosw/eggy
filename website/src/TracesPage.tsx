import { useCallback, useEffect, useState } from "react";
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
// It is a two-pane layout rather than a card, because the payloads are
// prompt-sized: a list narrow enough to scan on the left, and the whole
// remaining width for one turn's steps on the right.

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

const KIND_LABEL: Record<string, string> = {
  owner: "Owner",
  scheduled: "Scheduled",
  heartbeat: "Heartbeat",
};

function KindBadge({ kind }: { kind: string }) {
  return (
    <span className="rounded border border-border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
      {KIND_LABEL[kind] ?? kind ?? "turn"}
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

function SpanRow({ span }: { span: TraceSpan }) {
  const [open, setOpen] = useState(false);
  const model = span.kind === "model_call";
  return (
    <div className="rounded-lg border border-border bg-card">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors hover:bg-muted/40"
      >
        <span className={`h-2 w-2 shrink-0 rounded-full ${span.error ? "bg-destructive" : model ? "bg-primary" : "bg-muted-foreground"}`} />
        <span className="w-6 shrink-0 text-xs tabular-nums text-muted-foreground">{span.sequence}</span>
        <span className="shrink-0 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
          {model ? "model" : "tool"}
        </span>
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{span.name}</span>
        {span.total_tokens ? (
          <span className="shrink-0 text-xs text-muted-foreground">{formatTokens(span.total_tokens)} tok</span>
        ) : null}
        <span className="shrink-0 text-xs text-muted-foreground">{formatDuration(span.duration_ms)}</span>
        <span className={`shrink-0 transition-transform ${open ? "rotate-180" : ""}`}>
          <ChevronDownIcon />
        </span>
      </button>
      {open && (
        <div className="flex flex-col gap-3 border-t border-border p-4">
          {span.error && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2.5 text-xs text-destructive">{span.error}</div>
          )}
          {model ? <Prompt request={span.request} /> : <Body label="Arguments" text={span.request} />}
          <Body label={model ? "Response" : "Output"} text={span.response} />
        </div>
      )}
    </div>
  );
}

function TraceDetailPane({ detail }: { detail: TraceDetail }) {
  const { trace, spans } = detail;
  return (
    <div className="flex flex-col gap-4">
      <header className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <KindBadge kind={trace.kind} />
          <span className="text-xs text-muted-foreground">{formatTime(trace.started_at)}</span>
          <span className="text-xs text-muted-foreground">· {trace.channel || "unknown surface"}</span>
          {trace.model && <span className="text-xs text-muted-foreground">· {trace.model}</span>}
          {trace.effort && <span className="text-xs text-muted-foreground">· {trace.effort} effort</span>}
          {!trace.complete && (
            <span className="rounded border border-amber-500/50 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-amber-600 dark:text-amber-400">
              incomplete
            </span>
          )}
        </div>
        <dl className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs text-muted-foreground sm:grid-cols-4">
          <div><dt className="inline">Steps: </dt><dd className="inline text-foreground">{spans.length}</dd></div>
          <div><dt className="inline">Duration: </dt><dd className="inline text-foreground">{formatDuration(trace.duration_ms)}</dd></div>
          <div><dt className="inline">Prompt: </dt><dd className="inline text-foreground">{formatTokens(trace.prompt_tokens)} tok</dd></div>
          <div><dt className="inline">Completion: </dt><dd className="inline text-foreground">{formatTokens(trace.completion_tokens)} tok</dd></div>
        </dl>
      </header>
      {trace.error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{trace.error}</div>
      )}
      <Body label="Owner message" text={trace.input} />
      <Body label="Reply" text={trace.output} />
      <div className="flex flex-col gap-2">
        <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Timeline</span>
        {spans.length === 0 ? (
          <div className="rounded-md border border-dashed border-border px-4 py-6 text-center text-sm text-muted-foreground">
            This turn recorded no steps.
          </div>
        ) : (
          spans.map((span) => <SpanRow key={span.sequence} span={span} />)
        )}
      </div>
    </div>
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
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<TraceDetail | null>(null);
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

  const reload = useCallback(() => {
    setLoading(true);
    listTraces()
      .then((rows) => {
        setTraces(rows);
        setError("");
        // Landing on the newest turn is the common case: the trace you want
        // is almost always the one that just ran.
        setSelected((current) => current ?? rows[0]?.id ?? null);
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

  useEffect(() => {
    if (!selected) {
      setDetail(null);
      return;
    }
    let current = true;
    getTrace(selected)
      .then((loaded) => {
        if (current) setDetail(loaded);
      })
      .catch(fail);
    return () => {
      current = false;
    };
  }, [selected, fail]);

  return (
    <div className="flex h-full min-h-0">
      <div className="flex w-72 shrink-0 flex-col border-r border-border bg-card/60 md:w-80">
        <div className="flex items-center gap-2 border-b border-border p-2">
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
        <div className="scrollbar-slim min-h-0 flex-1 overflow-y-auto p-2">
          {traces.length === 0 && !loading ? (
            <p className="px-2 py-6 text-center text-sm text-muted-foreground">
              No turns recorded yet. Send a message and it will appear here.
            </p>
          ) : (
            traces.map((trace) => (
              <button
                key={trace.id}
                type="button"
                onClick={() => setSelected(trace.id)}
                className={`mb-1 flex w-full flex-col gap-1 rounded-md px-2.5 py-2 text-left transition-colors ${
                  trace.id === selected ? "bg-muted text-foreground" : "text-muted-foreground hover:bg-muted/60"
                }`}
              >
                <div className="flex items-center gap-2">
                  <KindBadge kind={trace.kind} />
                  <span className="text-[11px]">{formatTime(trace.started_at)}</span>
                  {trace.error && <span className="ml-auto text-[11px] text-destructive">error</span>}
                </div>
                <span className="truncate text-sm text-foreground">{trace.input || trace.output || "(no message)"}</span>
                <span className="text-[11px]">
                  {trace.spans} steps · {formatDuration(trace.duration_ms)} · {formatTokens(trace.total_tokens)} tok
                </span>
              </button>
            ))
          )}
        </div>
      </div>
      <div className="scrollbar-slim min-w-0 flex-1 overflow-y-auto bg-card/40 px-4 py-6 sm:px-8">
        <div className="mx-auto flex max-w-3xl flex-col gap-4">
          <header className="flex flex-col gap-1">
            <h1 className="text-2xl font-semibold tracking-tight">Traces</h1>
            <p className="text-sm text-muted-foreground">
              Every turn as it ran: the prompt behind each model call, and the arguments and output of every tool call.
            </p>
          </header>
          {error && <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>}
          {detail ? (
            <TraceDetailPane detail={detail} />
          ) : (
            !error && <p className="text-sm text-muted-foreground">Select a turn to see what it did.</p>
          )}
        </div>
      </div>
    </div>
  );
}
