import { useEffect, useState } from "react";
import { MCPToolPreview, RequestError, SessionExpiredError, previewMCPServer, setMCPApproval } from "../lib/api";
import { Button } from "../components/ui/button";
import { ErrorBanner } from "../components/ui/error-banner";
import { errorMessage } from "../lib/utils";

// Every tool starts ticked: a server from a catalog is asked about, not trusted,
// until the owner unticks a tool on purpose.
export function initialApprovals(tools: MCPToolPreview[]): Set<string> {
  return new Set(tools.map((tool) => tool.name));
}

// require_approval is the ticked tools, in the order the server advertised them.
export function approvalPayload(tools: MCPToolPreview[], ticked: Set<string>): string[] {
  return tools.filter((tool) => ticked.has(tool.name)).map((tool) => tool.name);
}

type Phase =
  | { kind: "loading" }
  | { kind: "tools"; tools: MCPToolPreview[] }
  | { kind: "restart" }
  | { kind: "login" }
  | { kind: "saved" }
  | { kind: "error"; message: string };

export function McpEnableDialog({
  server,
  onClose,
  onEnabled,
  onSessionExpired,
}: {
  server: string;
  onClose: () => void;
  onEnabled: () => void;
  onSessionExpired: () => void;
}) {
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });
  const [ticked, setTicked] = useState<Set<string>>(new Set());
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    previewMCPServer(server)
      .then((result) => {
        if (cancelled) return;
        setTicked(initialApprovals(result.tools));
        setPhase({ kind: "tools", tools: result.tools });
      })
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        if (err instanceof RequestError && err.status === 409 && err.body.restart) {
          setPhase({ kind: "restart" });
          return;
        }
        if (err instanceof RequestError && err.status === 409 && err.body.login) {
          setPhase({ kind: "login" });
          return;
        }
        setPhase({ kind: "error", message: errorMessage(err, "Could not reach the server") });
      });
    return () => {
      cancelled = true;
    };
  }, [server, onSessionExpired]);

  function toggle(name: string) {
    setTicked((current) => {
      const next = new Set(current);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }

  async function save(tools: MCPToolPreview[]) {
    setSaving(true);
    try {
      await setMCPApproval(
        server,
        true,
        approvalPayload(tools, ticked),
        tools.map((tool) => tool.name),
      );
      setPhase({ kind: "saved" });
      onEnabled();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setPhase({ kind: "error", message: errorMessage(err, "Could not enable the server") });
    } finally {
      setSaving(false);
    }
  }

  return (
    <div role="dialog" aria-label={`Review ${server}`} className="flex flex-col gap-3 rounded-2xl bg-neutral-100 p-4">
      <h3 className="text-[13px] font-medium">Review {server}</h3>
      {phase.kind === "loading" && <p className="text-xs text-neutral-700">Connecting to read the server's tools...</p>}
      {phase.kind === "restart" && (
        <p className="text-xs leading-relaxed text-neutral-700">
          This server was added after Eggy started. Restart Eggy, then review it again.
        </p>
      )}
      {phase.kind === "login" && (
        <p className="text-xs leading-relaxed text-neutral-700">
          This server needs authorization first. <a className="underline" href={`/auth/mcp/${encodeURIComponent(server)}`}>Authorize</a>, then
          review it again.
        </p>
      )}
      {phase.kind === "tools" && (
        <>
          <p className="text-xs leading-relaxed text-neutral-700">
            Ticked tools ask you before each call. Untick a tool only if you trust it to run without asking.
          </p>
          <ul className="flex flex-col gap-2">
            {phase.tools.map((tool) => (
              <li key={tool.name}>
                <label className="flex items-start gap-2 text-[12.5px]">
                  <input type="checkbox" checked={ticked.has(tool.name)} onChange={() => toggle(tool.name)} className="mt-0.5" />
                  <span>
                    <span className="font-medium">{tool.name}</span>
                    {tool.description && <span className="block text-neutral-700">{tool.description}</span>}
                  </span>
                </label>
              </li>
            ))}
          </ul>
          <Button type="button" disabled={saving} onClick={() => save(phase.tools)}>
            {saving ? "Enabling..." : "Enable server"}
          </Button>
        </>
      )}
      {phase.kind === "saved" && (
        <p role="status" className="text-xs leading-relaxed text-neutral-700">
          Saved. Restart Eggy to connect this server. Only the tools you reviewed are allowed; a tool the server adds later stays off
          until you review it again.
        </p>
      )}
      {phase.kind === "error" && <ErrorBanner>{phase.message}</ErrorBanner>}
      <Button type="button" variant="ghost" size="sm" onClick={onClose} className="self-end">
        Close
      </Button>
    </div>
  );
}
