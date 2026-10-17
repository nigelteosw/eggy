import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { ErrorBanner } from "../../components/ui/error-banner";
import { Select } from "../../components/ui/select";
import { handleFailure, installItem, listItems, listSources, MarketplaceItem, MarketplaceSource } from "./marketplace";
import { InstallReview } from "./InstallReview";

// Discover lists what the configured sources offer. Choosing an item opens its
// review; nothing is installed from this list directly.
export function DiscoverView({ onSessionExpired, onInstalled }: { onSessionExpired: () => void; onInstalled: () => void }) {
  const [sources, setSources] = useState<MarketplaceSource[]>([]);
  const [source, setSource] = useState("");
  const [items, setItems] = useState<MarketplaceItem[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    listSources()
      .then((list) => {
        setSources(list);
        if (list[0]) setSource(list[0].name);
      })
      .catch((err) => handleFailure(err, onSessionExpired, setError, "Could not list sources"));
  }, [onSessionExpired]);

  useEffect(() => {
    if (!source) return;
    let current = true;
    setItems([]);
    setError(null);
    listItems(source)
      .then((list) => {
        if (current) setItems(list);
      })
      .catch((err) => {
        if (current) handleFailure(err, onSessionExpired, setError, "Could not list items");
      });
    return () => {
      current = false;
    };
  }, [source, onSessionExpired]);

  if (selected) {
    return (
      <InstallReview
        request={{ source, item: selected }}
        approveLabel="Install"
        onBack={() => setSelected(null)}
        onSessionExpired={onSessionExpired}
        onApprove={async (approval) => {
          await installItem(approval);
          setSelected(null);
          onInstalled();
        }}
      />
    );
  }

  return (
    <section className="flex flex-col gap-3">
      {error && <ErrorBanner>{error}</ErrorBanner>}
      <label className="flex items-center gap-2 text-[12.5px]">
        Source
        <Select value={source} onChange={(e) => setSource(e.target.value)} aria-label="Source" className="bg-background">
          {sources.map((s) => (
            <option key={s.name} value={s.name}>
              {s.name} ({s.repo})
            </option>
          ))}
        </Select>
      </label>
      {sources.length === 0 && <p className="text-xs text-neutral-700">No sources yet. Add one in Sources.</p>}
      <ul className="flex flex-col gap-2">
        {items.map((item) => (
          <li key={item.name} className="flex items-center justify-between gap-3 rounded-xl bg-neutral-100 p-3">
            <span className="min-w-0">
              <span className="block text-[12.5px] font-medium">{item.name}</span>
              <span className="block text-xs text-neutral-700">{item.unsupported ? `Cannot install: ${item.unsupported}` : item.description}</span>
            </span>
            <Button type="button" variant="ghost" size="sm" disabled={!!item.unsupported} onClick={() => setSelected(item.name)}>
              Review
            </Button>
          </li>
        ))}
      </ul>
    </section>
  );
}

