import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { ErrorBanner } from "../../components/ui/error-banner";
import {
  Approval,
  checkInstall,
  handleFailure,
  InstallStatus,
  listInstalls,
  Plan,
  removeInstall,
  removePayload,
  setInstallDisabled,
  updateInstall,
} from "./marketplace";
import { InstallReview } from "./InstallReview";

const STATUS_LABEL: Record<string, string> = {
  up_to_date: "Up to date",
  update_available: "Update available",
  removed_upstream: "Removed upstream",
  unknown: "Not checked",
};

// Installed lists what the owner installed from a source, with the state of each
// install. Every action that changes files goes through a review first.
export function InstalledView({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [installs, setInstalls] = useState<InstallStatus[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [updating, setUpdating] = useState<InstallStatus | null>(null);
  const [removing, setRemoving] = useState<InstallStatus | null>(null);
  const [ticked, setTicked] = useState<Set<string>>(new Set());
  const [checked, setChecked] = useState<Record<string, Plan>>({});

  function load() {
    listInstalls()
      .then(setInstalls)
      .catch((err) => handleFailure(err, onSessionExpired, setError, "Could not list installs"));
  }

  useEffect(load, []);

  async function check(status: InstallStatus) {
    setError(null);
    try {
      const result = await checkInstall(status.install.id);
      setChecked({ ...checked, [status.install.id]: result.plan });
      setInstalls(installs.map((s) => (s.install.id === status.install.id ? result.status : s)));
    } catch (err) {
      handleFailure(err, onSessionExpired, setError, "Could not check for updates");
    }
  }

  async function toggle(status: InstallStatus) {
    setError(null);
    try {
      await setInstallDisabled(status.install.id, !status.install.disabled);
      load();
    } catch (err) {
      handleFailure(err, onSessionExpired, setError, "Could not change the install");
    }
  }

  async function confirmRemove(status: InstallStatus) {
    setError(null);
    try {
      await removeInstall(status.install.id, removePayload(status.edited, ticked));
      setRemoving(null);
      setTicked(new Set());
      load();
    } catch (err) {
      handleFailure(err, onSessionExpired, setError, "Could not remove the install");
    }
  }

  if (updating) {
    return (
      <InstallReview
        request={{ source: updating.install.source, item: updating.install.item, install_id: updating.install.id }}
        approveLabel="Update"
        editedSkills={updating.edited}
        onBack={() => setUpdating(null)}
        onSessionExpired={onSessionExpired}
        onApprove={async (approval: Approval, replaceEdited: string[]) => {
          await updateInstall(updating.install.id, { ...approval, replace_edited: replaceEdited });
          setUpdating(null);
          load();
        }}
      />
    );
  }

  return (
    <section className="flex flex-col gap-3">
      {error && <ErrorBanner>{error}</ErrorBanner>}
      {installs.length === 0 && <p className="text-xs text-neutral-700">Nothing installed from a marketplace yet.</p>}
      <ul className="flex flex-col gap-2">
        {installs.map((status) => {
          const { install } = status;
          const plan = checked[install.id];
          return (
            <li key={install.id} className="flex flex-col gap-2 rounded-xl bg-neutral-100 p-3">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <span className="text-[12.5px] font-medium">
                  {install.item} <span className="font-normal text-neutral-700">from {install.source}</span>
                </span>
                <span className="text-xs text-neutral-700">
                  {STATUS_LABEL[status.status] ?? status.status}
                  {install.disabled ? " · disabled" : ""}
                </span>
              </div>
              <p className="text-xs text-neutral-700">
                <code>{install.commit_sha.slice(0, 7)}</code> · installed by {install.installed_by} on{" "}
                {new Date(install.installed_at).toLocaleDateString()}
              </p>
              {status.edited.length > 0 && <p className="text-xs text-amber-800">Edited here: {status.edited.join(", ")}</p>}
              {plan && status.status === "update_available" && (
                <p className="text-xs text-neutral-700">A new version is ready to review.</p>
              )}
              <div className="flex flex-wrap gap-1">
                <Button type="button" variant="ghost" size="sm" onClick={() => check(status)}>
                  Check for updates
                </Button>
                {status.status === "update_available" && (
                  <Button type="button" variant="ghost" size="sm" onClick={() => setUpdating(status)}>
                    Update
                  </Button>
                )}
                <Button type="button" variant="ghost" size="sm" onClick={() => toggle(status)}>
                  {install.disabled ? "Enable" : "Disable"}
                </Button>
                <Button type="button" variant="ghost" size="sm" onClick={() => {
                    setTicked(new Set());
                    setRemoving(status);
                  }}>
                  Remove
                </Button>
              </div>
              {removing?.install.id === install.id && (
                <div className="flex flex-col gap-2 rounded-lg bg-background p-2">
                  <p className="text-xs">Removing deletes what this install wrote.</p>
                  {status.edited.map((name) => (
                    <label key={name} className="flex items-center gap-2 text-[12.5px]">
                      <input
                        type="checkbox"
                        checked={ticked.has(name)}
                        onChange={() => {
                          const next = new Set(ticked);
                          if (next.has(name)) next.delete(name);
                          else next.add(name);
                          setTicked(next);
                        }}
                      />
                      Delete my edits to {name}
                    </label>
                  ))}
                  <div className="flex gap-1">
                    <Button type="button" size="sm" onClick={() => confirmRemove(status)}>
                      Confirm remove
                    </Button>
                    <Button type="button" variant="ghost" size="sm" onClick={() => {
                        setTicked(new Set());
                        setRemoving(null);
                      }}>
                      Cancel
                    </Button>
                  </div>
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
