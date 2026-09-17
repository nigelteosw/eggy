import { useEffect, useState } from "react";
import { SessionExpiredError, getWatchList, saveWatchList } from "./api";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { PRIMARY_BUTTON } from "./components/ui/form";
import { errorMessage } from "./lib/utils";

// The heartbeat's watch list, sitting directly above the heartbeat itself
// because an interval with an empty list is a heartbeat that never beats.
//
// Until this card the list could only be written by asking Eggy to write it
// down for itself, which meant an owner whose heartbeat was silent had no way
// to see that the reason was an empty file on a volume they cannot reach.
//
// Eggy edits this document too -- a beat annotates what it has already
// reported so it does not repeat itself -- so the textarea is seeded on load
// and not written back until Save. Reopening the page shows whatever the last
// beat left.
export function WatchCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [watch, setWatch] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [detail, setDetail] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    getWatchList()
      .then((result) => setWatch(result.fields?.find((field) => field.label === "watch")?.value ?? ""))
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(errorMessage(err, "Could not read the watch list"));
      })
      .finally(() => setLoading(false));
  }, [onSessionExpired]);

  async function handleSave() {
    setSaving(true);
    setError(null);
    setDetail(null);
    setSaved(false);
    try {
      const result = await saveWatchList(watch);
      setDetail(result.detail ?? null);
      setSaved(true);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Could not save the watch list"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="mb-2">
      <CardHeader
        title="Watch list"
        className="mb-3"
        description={
          <>
            What the heartbeat checks each time it wakes. One thing to look at per line — an item that wants a time of
            its own is a schedule, not a watch entry. Eggy edits this too, noting what it has already told you so a
            later check-in does not repeat itself. An empty list means every beat is skipped.
          </>
        }
      />

      <div className="flex items-baseline justify-between gap-3 px-0.5 pb-2">
        <span className="text-[12.5px] font-medium">watch.md</span>
        <span className="text-[11.5px] text-neutral-700">An empty list means every beat is skipped</span>
      </div>
      <textarea
        spellCheck={false}
        value={watch}
        placeholder={"# Watch\n\n- Unread mail from real people older than a day\n- Calendar events in the next 12 hours I have not accepted"}
        onChange={(event) => {
          setWatch(event.target.value);
          setSaved(false);
          setDetail(null);
        }}
        disabled={loading}
        className="min-h-[14rem] w-full whitespace-pre rounded-2xl bg-neutral-100 px-4 py-3.5 font-mono text-[12.5px] leading-relaxed text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30"
      />
      <div className="mt-3 flex flex-wrap gap-2">
        <button
          type="button"
          onClick={handleSave}
          disabled={loading || saving}
          className={PRIMARY_BUTTON}
        >
          {saving ? "Saving..." : "Save watch list"}
        </button>
      </div>
      {saved && !detail && (
        <p className="mt-3 text-xs leading-relaxed text-neutral-700" role="status">
          Saved. The next heartbeat reads it — no restart needed.
        </p>
      )}
      {detail && (
        <p className="mt-3 text-xs leading-relaxed text-neutral-700" role="status">
          {detail}
        </p>
      )}
      {error && (
        <ErrorBanner className="mt-3">
          {error}
        </ErrorBanner>
      )}
    </div>
  );
}
