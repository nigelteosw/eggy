import { SchedulesCard } from "./SchedulesCard";

// The Scheduled destination. Schedules are created by asking Eggy in chat, so
// this page only lists what is pending and lets the owner cancel one.
export function SchedulesPage({ onSessionExpired }: { onSessionExpired: () => void }) {
  return (
    <main className="app-canvas scrollbar-slim h-full min-h-0 min-w-0 overflow-y-auto">
      <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-5 pb-11 pt-6 sm:px-7">
        <header className="flex flex-col gap-1.5 pb-1">
          <h1 className="text-xl font-semibold tracking-tight">Scheduled</h1>
          <p className="max-w-2xl text-sm leading-6 text-neutral-700">
            Tasks Eggy will run later. Ask Eggy in chat to set one up, and cancel it here.
          </p>
        </header>

        <SchedulesCard onSessionExpired={onSessionExpired} />
      </div>
    </main>
  );
}
