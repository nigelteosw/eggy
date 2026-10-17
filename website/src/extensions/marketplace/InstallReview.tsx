import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { ErrorBanner } from "../../components/ui/error-banner";
import { Input } from "../../components/ui/input";
import { handleFailure, Approval, approvalFor, canInstall, planItem, planRequestFor, Plan, PlanRequest, refusalMessage } from "./marketplace";

// The review every install and update goes through. The owner reads what will
// be written, renames anything that collides, and approves exactly this plan.
export function InstallReview({
  request,
  approveLabel,
  editedSkills = [],
  onApprove,
  onBack,
  onSessionExpired,
}: {
  request: PlanRequest;
  approveLabel: string;
  editedSkills?: string[];
  onApprove: (approval: Approval, replaceEdited: string[]) => Promise<void>;
  onBack: () => void;
  onSessionExpired: () => void;
}) {
  const [plan, setPlan] = useState<Plan | null>(null);
  const [renames, setRenames] = useState<Record<string, string>>({});
  const [draftRename, setDraftRename] = useState<Record<string, string>>({});
  const [replace, setReplace] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function load(nextRenames: Record<string, string>) {
    setPlan(null);
    planItem(planRequestFor(request.source, request.item, nextRenames, request.install_id))
      .then(setPlan)
      .catch((err) => handleFailure(err, onSessionExpired, setError, "Could not read the plan"));
  }

  useEffect(() => {
    load(renames);
    // The plan is keyed by the request, not the renames: a rename reloads it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request.source, request.item, request.install_id]);

  function applyRename(upstream: string) {
    const next = { ...renames, [upstream]: draftRename[upstream] ?? "" };
    setRenames(next);
    load(next);
  }

  async function approve() {
    if (!plan) return;
    setBusy(true);
    setError(null);
    try {
      await onApprove(approvalFor(plan, renames), [...replace]);
    } catch (err) {
      const refusal = refusalMessage(err);
      if (refusal) {
        setError(refusal);
        load(renames);
      } else {
        handleFailure(err, onSessionExpired, setError, "Could not install");
      }
    } finally {
      setBusy(false);
    }
  }

  function toggleReplace(name: string) {
    setReplace((current) => {
      const next = new Set(current);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }

  return (
    <section className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-[14px] font-medium">Review {request.item}</h2>
        <Button type="button" variant="ghost" size="sm" onClick={onBack}>
          Back
        </Button>
      </div>
      {!plan && !error && <p className="text-xs text-neutral-700">Reading the plan...</p>}
      {error && <ErrorBanner>{error}</ErrorBanner>}
      {plan && (
        <>
          <p className="text-xs leading-relaxed text-neutral-700">
            From {plan.repo} at <code>{plan.sha.slice(0, 7)}</code>. Skills are shared by every account. Their instructions reach the model on
            any turn where they match.
          </p>
          <p className="text-xs text-neutral-700">
            The skills add {plan.index_bytes} bytes to the index. Each one's name stays in the index even if its description has to be left out.
          </p>

          {plan.conflicts.length > 0 && (
            <div className="flex flex-col gap-2 rounded-xl bg-neutral-100 p-3">
              <p className="text-xs font-medium">These names already exist. Rename them to install.</p>
              {plan.conflicts.map((upstream) => (
                <div key={upstream} className="flex items-center gap-2">
                  <span className="text-[12.5px]">{upstream}</span>
                  <Input
                    aria-label={`Rename ${upstream}`}
                    placeholder="new name"
                    value={draftRename[upstream] ?? ""}
                    onChange={(e) => setDraftRename({ ...draftRename, [upstream]: e.target.value })}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") applyRename(upstream);
                    }}
                    onBlur={() => {
                      if (draftRename[upstream]) applyRename(upstream);
                    }}
                    className="h-8 max-w-[220px]"
                  />
                </div>
              ))}
            </div>
          )}

          <div className="flex flex-col gap-2">
            <h3 className="text-[13px] font-medium">Skills ({plan.skills.length})</h3>
            {plan.skills.map((skill) => (
              <details key={skill.name} className="rounded-xl bg-neutral-100 p-3">
                <summary className="cursor-pointer text-[12.5px]">
                  <span className="font-medium">{skill.name}</span>
                  {skill.upstream_name !== skill.name && <span className="text-neutral-700"> (from {skill.upstream_name})</span>}
                  <span className="block text-neutral-700">{skill.description}</span>
                </summary>
                {skill.warnings?.map((warning) => (
                  <p key={warning} className="mt-2 text-xs text-amber-800">
                    {warning}
                  </p>
                ))}
                <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap text-[12px]">{skill.body}</pre>
                {Object.entries(skill.contents ?? {}).map(([file, text]) => (
                  <details key={file} className="mt-2">
                    <summary className="cursor-pointer text-[12px]">{file}</summary>
                    <pre className="max-h-60 overflow-auto whitespace-pre-wrap text-[12px]">{text}</pre>
                  </details>
                ))}
                {editedSkills.includes(skill.name) && (
                  <label className="mt-2 flex items-center gap-2 text-[12.5px]">
                    <input type="checkbox" checked={replace.has(skill.name)} onChange={() => toggleReplace(skill.name)} />
                    Replace my local edits to {skill.name}
                  </label>
                )}
              </details>
            ))}
          </div>

          {plan.mcp.length > 0 && (
            <div className="flex flex-col gap-1">
              <h3 className="text-[13px] font-medium">Remote servers (added disabled)</h3>
              {plan.mcp.map((server) => (
                <p key={server.name} className="text-[12.5px]">
                  {server.name} <span className="text-neutral-700">{server.url}</span>
                </p>
              ))}
            </div>
          )}

          {plan.skipped.length > 0 && (
            <div className="flex flex-col gap-1">
              <h3 className="text-[13px] font-medium">Left out</h3>
              {plan.skipped.map((item) => (
                <p key={`${item.path}-${item.reason}`} className="text-[12.5px] text-neutral-700">
                  {item.path}: {item.reason}
                </p>
              ))}
            </div>
          )}

          <Button type="button" disabled={busy || !canInstall(plan)} onClick={approve}>
            {busy ? "Working..." : approveLabel}
          </Button>
        </>
      )}
    </section>
  );
}
