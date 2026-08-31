import { useEffect, useState } from "react";
import { SessionExpiredError, getWatchList, saveWatchList } from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";

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
        setError(err instanceof Error ? err.message : "Could not read the watch list");
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
      setError(err instanceof Error ? err.message : "Could not save the watch list");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Watch list</CardTitle>
        <CardDescription>
          What the heartbeat checks each time it wakes. One thing to look at per line — an item that wants a time of
          its own is a schedule, not a watch entry. Eggy edits this too, noting what it has already told you so a
          later check-in does not repeat itself. An empty list means every beat is skipped.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
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
          className="min-h-[14rem] w-full whitespace-pre rounded-md border border-border bg-background px-3 py-2 font-mono text-[13px] leading-relaxed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        />
        <div>
          <Button type="button" onClick={handleSave} disabled={loading || saving}>
            {saving ? "Saving..." : "Save watch list"}
          </Button>
        </div>
        {saved && !detail && (
          <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">
            Saved. The next heartbeat reads it — no restart needed.
          </p>
        )}
        {detail && (
          <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">
            {detail}
          </p>
        )}
        {error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
