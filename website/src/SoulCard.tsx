import { useEffect, useState } from "react";
import { SessionExpiredError, getSoul, saveSoul } from "./api";
import { CardHeader } from "./components/ui/card-header";
import { ErrorBanner } from "./components/ui/error-banner";
import { PRIMARY_BUTTON } from "./components/ui/form";
import { errorMessage } from "./lib/utils";

// SOUL.md, Eggy's identity, as a setting rather than a file on a volume. It
// is shared: every turn for every person starts from it. Eggy rewrites it too
// when someone asks it to change how it behaves, so the textarea is seeded on
// load and written back only on Save.
export function SoulCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [soul, setSoul] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);

  useEffect(() => {
    getSoul()
      .then((result) => setSoul(result.fields?.find((field) => field.label === "soul")?.value ?? ""))
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(errorMessage(err, "Could not read the soul"));
      })
      .finally(() => setLoading(false));
  }, [onSessionExpired]);

  async function save(content: string) {
    setSaving(true);
    setError(null);
    setStatus(null);
    try {
      const result = await saveSoul(content);
      if (content.trim() === "") {
        const reloaded = await getSoul();
        setSoul(reloaded.fields?.find((field) => field.label === "soul")?.value ?? "");
      }
      setStatus(result.detail ?? "Saved. The next message uses it — no restart needed.");
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Could not save the soul"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="mb-2">
      <CardHeader
        title="Soul"
        className="mb-3"
        description={
          <>
            Who Eggy is and how it sounds — tone, directness, personality. It applies to everyone who uses this Eggy.
            You can also just tell Eggy how you would like it to behave, and it rewrites this itself.
          </>
        }
      />
      <div className="flex items-baseline justify-between gap-3 px-0.5 pb-2">
        <span className="text-[12.5px] font-medium">SOUL.md</span>
        <span className="text-[11.5px] text-neutral-700">Up to 4 KB</span>
      </div>
      <textarea
        spellCheck={false}
        value={soul}
        onChange={(event) => {
          setSoul(event.target.value);
          setStatus(null);
        }}
        disabled={loading}
        className="min-h-[14rem] w-full whitespace-pre-wrap rounded-2xl bg-neutral-100 px-4 py-3.5 font-mono text-[12.5px] leading-relaxed text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30"
      />
      <div className="mt-3 flex flex-wrap gap-2">
        <button type="button" onClick={() => save(soul)} disabled={loading || saving} className={PRIMARY_BUTTON}>
          {saving ? "Saving..." : "Save soul"}
        </button>
        <button
          type="button"
          onClick={() => save("")}
          disabled={loading || saving}
          className="text-xs text-neutral-700 underline-offset-4 hover:underline"
        >
          Reset to built-in soul
        </button>
      </div>
      {status && (
        <p className="mt-3 text-xs leading-relaxed text-neutral-700" role="status">
          {status}
        </p>
      )}
      {error && <ErrorBanner className="mt-3">{error}</ErrorBanner>}
    </div>
  );
}
