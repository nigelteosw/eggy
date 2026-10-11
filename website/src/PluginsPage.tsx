import { FinanceCard } from "./FinanceCard";
import { RestartCard } from "./RestartCard";
import { pathForView, type View } from "./routing";

export function PluginsPage({
  features,
  onSessionExpired,
  onNavigate,
}: {
  features: string[];
  onSessionExpired: () => void;
  onNavigate: (view: View) => void;
}) {
  const financeRunning = features.includes("finance");

  return (
    <main className="app-canvas scrollbar-slim min-h-0 min-w-0 flex-1 overflow-y-auto">
      <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-5 pb-11 pt-6 sm:px-7">
        <header className="flex flex-col gap-1.5 pb-1">
          <h1 className="text-xl font-semibold tracking-tight">Plugins</h1>
          <p className="max-w-2xl text-sm leading-6 text-neutral-700">
            Manage Eggy&apos;s built-in feature plugins. Changes to a plugin take effect after a restart.
          </p>
        </header>

        <FinanceCard onSessionExpired={onSessionExpired} running={financeRunning} />

        {financeRunning && (
          <a
            href={pathForView("finance")}
            onClick={(event) => {
              if (event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) {
                event.preventDefault();
                onNavigate("finance");
              }
            }}
            className="inline-flex min-h-10 w-fit items-center rounded-xl px-3.5 text-sm font-medium text-primary transition-colors hover:bg-neutral-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            Open Finance ledger
          </a>
        )}

        <RestartCard onSessionExpired={onSessionExpired} />
      </div>
    </main>
  );
}
